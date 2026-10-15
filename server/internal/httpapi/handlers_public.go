package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// handlePublicStore 客人端门店页数据（无需登录）。
// ★ WiFi 为空时不返回，前端条件渲染（兜底不依赖 WiFi）。
func (s *Server) handlePublicStore(w http.ResponseWriter, r *http.Request) {
	st, err := s.repo.GetStoreBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}

	dishes, _ := s.repo.ListDishes(r.Context(), st.ID)
	tpl, _ := s.repo.GetTemplateByStore(r.Context(), st.ID)
	_ = s.repo.TrackEvent(r.Context(), st.ID, "g_scan", map[string]any{"ref": r.URL.Query().Get("ref")})

	storePub := map[string]any{
		"id":             st.ID,
		"name":           st.Name,
		"slug":           st.Slug,
		"coupon_enabled": st.CouponEnabled,
		"service_tier":   st.ServiceTier,
	}
	if st.WifiSSID != "" {
		storePub["wifi_ssid"] = st.WifiSSID
		storePub["wifi_pwd"] = st.WifiPwd
	}
	// 分类名多语种表：客人端据此把区块标题一起切到目标语言
	if len(st.CategoryNames) > 0 {
		storePub["category_names"] = st.CategoryNames
	}
	resp := map[string]any{"ok": true, "store": storePub, "dishes": dishes}
	if tpl != nil {
		resp["coupon_template"] = tpl
	}
	ok(w, resp)
}

func clampScore(v int) int {
	if v < 1 {
		return 1
	}
	if v > 5 {
		return 5
	}
	return v
}

// handleSubmitRating 客人提交三维度评价；★ 评价即领券（发券免费）。
func (s *Server) handleSubmitRating(w http.ResponseWriter, r *http.Request) {
	st, err := s.repo.GetStoreBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}

	var req struct {
		Ref          string `json:"ref"`
		ScoreDish    int    `json:"score_dish"`
		ScoreService int    `json:"score_service"`
		ScoreReturn  int    `json:"score_return"`
		Comment      string `json:"comment"`
		Lang         string `json:"lang"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	if req.ScoreDish < 1 || req.ScoreService < 1 || req.ScoreReturn < 1 {
		fail(w, http.StatusBadRequest, CodeValidation, "请完成三维度打分")
		return
	}

	rt := &domain.Rating{
		StoreID:       st.ID,
		Ref:           strings.TrimSpace(req.Ref),
		ScoreDish:     clampScore(req.ScoreDish),
		ScoreService:  clampScore(req.ScoreService),
		ScoreReturn:   clampScore(req.ScoreReturn),
		CommentNative: strings.TrimSpace(req.Comment),
	}
	// 客人母语评论 → 中文译文（商家端看中文；翻译不可用则留空，不编造）
	if rt.CommentNative != "" && req.Lang != "" && req.Lang != "zh" {
		if out, err := s.tr.Translate(r.Context(), []string{rt.CommentNative}, req.Lang, "zh"); err == nil && len(out) > 0 {
			rt.CommentZh = out[0]
		}
	}
	if err := s.repo.CreateRating(r.Context(), rt); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "提交评价失败")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "t_rate_submit", map[string]any{"ref": rt.Ref})

	// ★ 评价领券：发券免费，是「满意度 + 复购」唯一客观数据源，不卡付费墙后
	var coupon *domain.Coupon
	giftLocalized := ""
	if st.CouponEnabled {
		coupon, _ = s.repo.IssueCoupon(r.Context(), st.ID, "", req.Lang)
		if coupon != nil {
			_ = s.repo.TrackEvent(r.Context(), st.ID, "t_coupon_issue", nil)
		}
		// 券面文案本地化：券模板只存 zh/en，其它语种用同一条翻译链补齐。
		// 券面是短固定串，走 TM 缓存后基本零成本；未命中则留空，前端回落英文。
		if coupon != nil && req.Lang != "" && req.Lang != "zh" {
			if tpl, err := s.repo.GetTemplateByStore(r.Context(), st.ID); err == nil && tpl != nil {
				if zhText, _ := tpl.Content["zh"].(string); strings.TrimSpace(zhText) != "" {
					if out, terr := s.tr.Translate(r.Context(), []string{zhText}, "zh", req.Lang); terr == nil &&
						len(out) > 0 && out[0] != "" {
						giftLocalized = out[0]
					}
				}
			}
		}
	}

	ok(w, map[string]any{
		"ok": true, "rating": rt, "coupon": coupon, "coupon_gift": giftLocalized,
	})
}

// handlePublicCoupon 按券码查券（核销页预览 / 客人回看）。
func (s *Server) handlePublicCoupon(w http.ResponseWriter, r *http.Request) {
	c, err := s.repo.GetCouponByCode(r.Context(), r.PathValue("code"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "券码不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "查询券失败")
		return
	}
	ok(w, map[string]any{"ok": true, "coupon": c})
}

// handleOCR 菜单识别代理：生产转发 Python PaddleOCR 微服务；
// 无服务时返回 need_manual=true，前端走手动保底（P2d）。
func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageURL    string `json:"image_url"`
		ImageBase64 string `json:"image_base64"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	if req.ImageURL == "" && req.ImageBase64 == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "请上传菜单图片")
		return
	}

	res, err := s.ocr.Extract(r.Context(), req.ImageURL, req.ImageBase64)
	if err != nil {
		fail(w, http.StatusBadGateway, CodeInternal, "识别服务暂时不可用，请手动录入")
		return
	}
	ok(w, map[string]any{"ok": true, "result": res})
}

// PreviewItem 是 P4 预览的输入/输出项。
type PreviewItem struct {
	Name     string            `json:"name"`
	Price    *float64          `json:"price,omitempty"`
	Category string            `json:"category,omitempty"`
	Selected bool              `json:"selected"`
	NameEn   string            `json:"name_en,omitempty"`
	Names    map[string]string `json:"names,omitempty"` // 多语种结果：{"en":"...","ja":"..."}
}

// handleLangs 返回产品支持的语言、原文语种与当前翻译引擎。
func (s *Server) handleLangs(w http.ResponseWriter, _ *http.Request) {
	ok(w, map[string]any{
		"ok":     true,
		"source": service.SourceLang,
		"langs":  service.SupportedLangs,
		"engine": s.tr.Name(),
	})
}

// normalizeTargets 清洗目标语种列表：去空、去重、剔除原文语种与不支持项。
func normalizeTargets(targets []string, fallback string) []string {
	if len(targets) == 0 {
		if strings.TrimSpace(fallback) != "" {
			targets = []string{fallback}
		} else {
			targets = []string{"en"}
		}
	}
	out := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if t == "" || t == service.SourceLang || seen[t] || !service.IsSupportedLang(t) {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		out = []string{"en"}
	}
	return out
}

// handlePreviewTranslate 是引导流 P4 用的「注册前多语预览」：
// 不落库、不需要登录，让商家在注册前就看到自己的多语菜单（价值高潮）。
// 支持一次翻多个语种；未命中的语种留空，由前端回落显示原文——不编造。
func (s *Server) handlePreviewTranslate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items   []PreviewItem `json:"items"`
		From    string        `json:"from"`
		To      string        `json:"to"`
		Targets []string      `json:"targets"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Items) == 0 {
		fail(w, http.StatusBadRequest, CodeValidation, "菜单内容不能为空")
		return
	}
	if req.From == "" {
		req.From = service.SourceLang
	}
	targets := normalizeTargets(req.Targets, req.To)

	for i := range req.Items {
		req.Items[i].Names = map[string]string{}
	}
	for _, tg := range targets {
		texts := make([]string, len(req.Items))
		for i, it := range req.Items {
			texts[i] = it.Name
		}
		translated, err := s.tr.Translate(r.Context(), texts, req.From, tg)
		if err != nil && len(translated) == 0 {
			continue
		}
		for i := range req.Items {
			if i < len(translated) && translated[i] != "" {
				req.Items[i].Names[tg] = translated[i]
			}
		}
	}
	for i := range req.Items {
		req.Items[i].NameEn = req.Items[i].Names["en"]
	}

	// 分类名同批预览：让商家在注册前就看到「分类标题 + 菜名」整体多语化后的样子
	cats := distinctPreviewCategories(req.Items)
	catNames := s.translateCategories(r.Context(), req.From, cats, targets)

	ok(w, map[string]any{
		"ok":          true,
		"items":       req.Items,
		"categories":  catNames,
		"source_cats": cats,
		"targets":     targets,
		"engine":      s.tr.Name(),
	})
}

// distinctPreviewCategories 取出预览项里出现过的分类名（去重、保序）。
func distinctPreviewCategories(items []PreviewItem) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, it := range items {
		c := strings.TrimSpace(it.Category)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
