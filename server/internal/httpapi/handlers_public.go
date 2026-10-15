// 本文件：客人端（公开，无需登录）与工具接口。
//
// 客人链路的完整闭环在这里收口：
//
//	扫桌码/门口码 → GET /api/public/store/{slug}（看菜单）
//	→ POST /api/public/store/{slug}/rating（提交三维度评价）
//	→ ★ 自动领券（发券免费）→ 到店由店员核销
//
// 之所以全部公开且不带登录，是因为客人是外国游客、扫一下码就要能用，
// 任何注册门槛都会直接掐断这条链路。
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
//
// 这里刻意手工拼 map 而不是直接序列化 domain.Store：门店表里有 AccountID
// 这类内部字段，绝不能泄露给公开的客人端。WiFi 字段也只有在填了 SSID 时才下发，
// 前端据此决定是否渲染「WiFi 卡片」——所以该字段「不出现」是有语义的，不能补空串。
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
	// 扫码即埋点：ref 区分门口码（door）与桌码（t01），是经营洞察里扫码数的来源
	_ = s.repo.TrackEvent(r.Context(), st.ID, "g_scan", map[string]any{"ref": r.URL.Query().Get("ref")})

	storePub := map[string]any{
		"id":             st.ID,
		"name":           st.Name,
		"slug":           st.Slug,
		"coupon_enabled": st.CouponEnabled,
		"service_tier":   st.ServiceTier,
	}
	// ★ WiFi 未填时该字段不能出现在 JSON 里（前端据此条件渲染，不靠空串判断）
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

// clampScore 把评分收敛到 1–5 的合法区间（只处理上界与下界）。
//
// 注意：本函数只负责「收敛」，不负责「拒绝」。
// 调用方 handleSubmitRating 已先行拒绝 <=0 的分数，因此这里 v<1 的分支
// 实际不会在评分路径上被触发，保留它是为了让函数自身语义完整、可被独立复用。
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
//
// 三个维度：菜品（score_dish）/ 服务（score_service）/ 会再来吗（score_return）。
// 评价提交成功后自动发券——券是「满意度 + 复购」两个核心指标唯一客观的数据源，
// 因此发券不设任何付费墙。
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
	// 评分契约（有意的不对称，两条边界语义完全不同）：
	//   <=0  → 视为「客人根本没点星星」，直接 400 拒绝；绝不伪造 1 星。
	//   >5   → 客户端越界（UI 是 5 星控件，正常不会发生），收敛到 5；
	//          宁可收敛也不丢客人评价，故不拒绝。
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
	// 原文语种为 zh 时无需翻译，直接跳过（省一次无意义的调用）
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
	// 发券失败不影响评价本身（评价已落库），故 coupon 允许为 nil。
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
//
// 公开且不做归属校验：券码本身就是凭证，客人拿码来看券是正常场景；
// 真正的归属与状态校验发生在核销环节（见 handleRedeem）。
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
//
// 这里不直接暴露 OCR 微服务的地址与协议，前端只认这一个入口；
// 识别失败返回 502 并提示手动录入，让商家永远有一条能走通的路（手动兜底）。
func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageURL    string `json:"image_url"`
		ImageBase64 string `json:"image_base64"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	// 两种传图方式至少给一种：URL 适合已有图床的场景，base64 适合前端直传
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
//
// 输入时只需 name / price / category；输出时额外带上 Names（多语种结果）。
// NameEn 是给旧代码的兼容字段，由 Names["en"] 派生，两者必须保持一致。
type PreviewItem struct {
	Name     string            `json:"name"`
	Price    *float64          `json:"price,omitempty"`
	Category string            `json:"category,omitempty"`
	Selected bool              `json:"selected"`
	NameEn   string            `json:"name_en,omitempty"`
	Names    map[string]string `json:"names,omitempty"` // 多语种结果：{"en":"...","ja":"..."}
}

// handleLangs 返回产品支持的语言、原文语种与当前翻译引擎。
//
// 语种清单直接读 service.SupportedLangs，本处不硬编码任何语种：
// 新增一门语言只需在 service 登记一行，前端自动多出一个选项，业务代码零改动。
// 前端据此渲染语言选择器（含 native 母语写法与 tier 质量分层）。
func (s *Server) handleLangs(w http.ResponseWriter, _ *http.Request) {
	ok(w, map[string]any{
		"ok":     true,
		"source": service.SourceLang,
		"langs":  service.SupportedLangs,
		"engine": s.tr.Name(),
	})
}

// normalizeTargets 清洗目标语种列表：去空、去重、剔除原文语种与不支持项。
//
// 规则（顺序即语义，任何放宽都要先想清楚后果）：
//  1. targets 为空时：优先回落到 fallback（兼容旧的单语种参数 to），
//     再兜底为 en —— 绝不能返回空列表，否则前端无从渲染；
//  2. 逐项 TrimSpace，空串直接丢；
//  3. 剔除原文语种（zh）：翻译 zh→zh 无意义，且会污染 names_json；
//  4. 剔除未登记语种：只有 service.SupportedLangs 里的才允许通过，
//     防止前端传入质量很差的语种（如意大利语）污染客人端页面；
//  5. 去重且保序（用户勾选顺序即前端展示顺序）；
//  6. 全部非法时同样兜底为 en。
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
//
// 这是转化漏斗最关键的一步：先让商家「看到效果」，再让他注册。
// 因此它必须零门槛（不鉴权、不落库），但也要能返回与正式链路一致的结果形状。
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
	// from 缺省为原文语种 zh；to 是旧的单语种参数，仍由 normalizeTargets 兼容
	if req.From == "" {
		req.From = service.SourceLang
	}
	targets := normalizeTargets(req.Targets, req.To)

	// 先给每项建好空 Names，保证即使一个语种都没翻出来，前端也能拿到稳定的结构
	for i := range req.Items {
		req.Items[i].Names = map[string]string{}
	}
	for _, tg := range targets {
		texts := make([]string, len(req.Items))
		for i, it := range req.Items {
			texts[i] = it.Name
		}
		translated, err := s.tr.Translate(r.Context(), texts, req.From, tg)
		// 该语种整批失败且无结果：跳过，不影响其它语种
		if err != nil && len(translated) == 0 {
			continue
		}
		for i := range req.Items {
			if i < len(translated) && translated[i] != "" {
				req.Items[i].Names[tg] = translated[i]
			}
		}
	}
	// name_en 是给旧代码的兼容字段，必须与 names["en"] 保持一致
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
//
// 与 distinctCategories 逻辑同构，但作用于预览项（PreviewItem）而非成品菜品
// （Dish）——预览阶段还没有落库的 Dish，故单独实现，避免为凑复用强行转换类型。
// 空分类（含纯空白）一律跳过。
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
