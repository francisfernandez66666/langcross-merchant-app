package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// requireStore 校验门店存在且属于当前账号。
func (s *Server) requireStore(w http.ResponseWriter, r *http.Request) (*domain.Store, bool) {
	st, err := s.repo.GetStoreByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return nil, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return nil, false
	}
	if st.AccountID != accountID(r) {
		fail(w, http.StatusForbidden, CodeForbidden, "无权访问该门店")
		return nil, false
	}
	return st, true
}

// storeLinks 返回门店页与两种码的链接（门口码 ref=door / 桌码 ref=t01）。
func (s *Server) storeLinks(st *domain.Store) map[string]string {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	return map[string]string{
		"store_page": fmt.Sprintf("%s/s/%s", base, st.Slug),
		"door":       fmt.Sprintf("%s/s/%s?ref=door", base, st.Slug),
		"table":      fmt.Sprintf("%s/s/%s?ref=t01", base, st.Slug),
		"qr_door":    fmt.Sprintf("/api/stores/%s/qr?ref=door", st.ID),
		"qr_table":   fmt.Sprintf("/api/stores/%s/qr?ref=t01", st.ID),
	}
}

func nilIfEmpty(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

// handleListStores 列出当前账号的门店。
func (s *Server) handleListStores(w http.ResponseWriter, r *http.Request) {
	stores, err := s.repo.ListStoresByAccount(r.Context(), accountID(r))
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}
	ok(w, map[string]any{"ok": true, "stores": stores})
}

// handleCreateStore 创建门店；★ 注册后自动配好复购券模板（发券免费）+ 生成双码链接。
func (s *Server) handleCreateStore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Addr     string `json:"addr"`
		WifiSSID string `json:"wifi_ssid"`
		WifiPwd  string `json:"wifi_pwd"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "店名不能为空")
		return
	}

	st, err := s.repo.CreateStore(r.Context(), accountID(r), strings.TrimSpace(req.Name), idgen.Slug())
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "创建门店失败")
		return
	}

	// 注册后补填（WiFi 可选，不设必填）
	if req.Addr != "" || req.WifiSSID != "" || req.WifiPwd != "" {
		if updated, err := s.repo.UpdateStore(r.Context(), st.ID, repo.StorePatch{
			Addr:     nilIfEmpty(req.Addr),
			WifiSSID: nilIfEmpty(req.WifiSSID),
			WifiPwd:  nilIfEmpty(req.WifiPwd),
		}); err == nil {
			st = updated
		}
	}

	// ★ 注册即自动配好复购券模板（券是「满意度 + 复购」两个一期核心指标唯一客观数据源）
	tpl, _ := s.repo.CreateTemplate(r.Context(), st.ID, nil, 14)
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_register", nil)

	ok(w, map[string]any{
		"ok":              true,
		"store":           st,
		"coupon_template": tpl,
		"links":           s.storeLinks(st),
	})
}

// handleGetStore 返回门店详情 + 菜单 + 确认稿 + 链接。
func (s *Server) handleGetStore(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	dishes, _ := s.repo.ListDishes(r.Context(), st.ID)
	draft, err := s.repo.GetLatestDraft(r.Context(), st.ID)
	resp := map[string]any{
		"ok": true, "store": st, "dishes": dishes, "links": s.storeLinks(st),
	}
	if err == nil {
		resp["draft"] = draft
	}
	ok(w, resp)
}

// handlePatchStore 局部更新门店（含档位开关 service_tier / 点单模式 order_mode）。
func (s *Server) handlePatchStore(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		Addr          *string `json:"addr"`
		WifiSSID      *string `json:"wifi_ssid"`
		WifiPwd       *string `json:"wifi_pwd"`
		BusinessHours *string `json:"business_hours"`
		CouponEnabled *bool   `json:"coupon_enabled"`
		ServiceTier   *string `json:"service_tier"`
		OrderMode     *string `json:"order_mode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	if req.ServiceTier != nil && *req.ServiceTier != "pure" && *req.ServiceTier != "service" {
		fail(w, http.StatusBadRequest, CodeValidation, "档位只能是 pure / service")
		return
	}
	if req.OrderMode != nil && *req.OrderMode != "assist" && *req.OrderMode != "replace" {
		fail(w, http.StatusBadRequest, CodeValidation, "点单模式只能是 assist / replace")
		return
	}

	updated, err := s.repo.UpdateStore(r.Context(), st.ID, repo.StorePatch{
		Name: req.Name, Addr: req.Addr, WifiSSID: req.WifiSSID, WifiPwd: req.WifiPwd,
		BusinessHours: req.BusinessHours, CouponEnabled: req.CouponEnabled,
		ServiceTier: req.ServiceTier, OrderMode: req.OrderMode,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "更新门店失败")
		return
	}
	ok(w, map[string]any{"ok": true, "store": updated})
}

// handleQR 生成门口码 / 桌码 PNG（内容不可变，物料印刷后长期有效）。
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		ref = "door"
	}
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	content := fmt.Sprintf("%s/s/%s?ref=%s", base, st.Slug, ref)
	if err := service.WriteQR(w, content, 360); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "二维码生成失败")
	}
}

// handleGetDraft 取最新原文确认稿。
func (s *Server) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	d, err := s.repo.GetLatestDraft(r.Context(), st.ID)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "暂无确认稿")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取确认稿失败")
		return
	}
	ok(w, map[string]any{"ok": true, "draft": d})
}

// handleSaveDraft 保存原文确认稿（P2 上传/OCR 之后；未确认不允许翻译）。
func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		Lang  string             `json:"lang"`
		Items []domain.DraftItem `json:"items"`
	}
	if err := decodeJSON(r, &req); err != nil || len(req.Items) == 0 {
		fail(w, http.StatusBadRequest, CodeValidation, "菜单内容不能为空")
		return
	}
	if req.Lang == "" {
		req.Lang = "zh"
	}
	d, err := s.repo.CreateDraft(r.Context(), st.ID, req.Lang, req.Items)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "保存确认稿失败")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_ocr_done", nil)
	ok(w, map[string]any{"ok": true, "draft": d})
}

// handleConfirmDraft 确认原文（一次确认即生效，责任主体=商家）。
func (s *Server) handleConfirmDraft(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		DraftID string `json:"draft_id"`
	}
	_ = decodeJSON(r, &req)

	draftID := strings.TrimSpace(req.DraftID)
	if draftID == "" {
		d, err := s.repo.GetLatestDraft(r.Context(), st.ID)
		if err != nil {
			fail(w, http.StatusBadRequest, CodeValidation, "请先上传菜单")
			return
		}
		draftID = d.ID
	}
	if err := s.repo.ConfirmDraft(r.Context(), st.ID, draftID); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "确认失败")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_source_confirmed", nil)
	ok(w, map[string]any{"ok": true, "draft_id": draftID})
}

// handleTranslate 多语化（P4）：只翻译勾选的菜；未确认的确认稿拒绝翻译。
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}

	d, err := s.repo.GetLatestDraft(r.Context(), st.ID)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusBadRequest, CodeValidation, "请先上传菜单")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取确认稿失败")
		return
	}
	if d.ConfirmedAt == nil {
		fail(w, http.StatusBadRequest, CodeValidation, "原文确认稿未确认，不允许翻译")
		return
	}

	// 只翻勾选的菜；一个都没勾 ⇒ 视为全选
	picked := make([]domain.DraftItem, 0, len(d.Items))
	for _, it := range d.Items {
		if it.Selected {
			picked = append(picked, it)
		}
	}
	if len(picked) == 0 {
		picked = d.Items
	}

	names := make([]string, len(picked))
	for i, it := range picked {
		names[i] = it.Name
	}
	translated, err := s.tr.Translate(r.Context(), names, d.Lang, "en")
	if err != nil {
		fail(w, http.StatusBadGateway, CodeInternal, "翻译服务不可用")
		return
	}

	dishes := make([]domain.Dish, 0, len(picked))
	for i, it := range picked {
		dish := domain.Dish{
			NameZh: it.Name, Price: it.Price, Category: it.Category, SortOrder: i,
			Attrs: map[string]any{"spicy": 0, "allergens": []string{}},
		}
		if i < len(translated) && translated[i] != "" {
			dish.NameEn = translated[i]
		}
		dishes = append(dishes, dish)
	}
	if err := s.repo.ReplaceDishes(r.Context(), st.ID, dishes); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "写入菜单失败")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_translate_done", nil)

	saved, _ := s.repo.ListDishes(r.Context(), st.ID)
	ok(w, map[string]any{"ok": true, "dishes": saved, "translated_count": len(dishes)})
}

// handleListDishes 列出菜单。
func (s *Server) handleListDishes(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	dishes, err := s.repo.ListDishes(r.Context(), st.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取菜单失败")
		return
	}
	ok(w, map[string]any{"ok": true, "dishes": dishes})
}

// handlePatchDish 更新售罄 / 每日剩余（唯一动态字段）。
func (s *Server) handlePatchDish(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		SoldOut   bool `json:"sold_out"`
		Remaining *int `json:"remaining"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	if err := s.repo.UpdateDishStatus(r.Context(), st.ID, r.PathValue("dishId"), req.SoldOut, req.Remaining); err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "菜品不存在")
		return
	}
	ok(w, map[string]any{"ok": true})
}

// handleListRatings 列出评价（商家端看中文译文）。
func (s *Server) handleListRatings(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	ratings, err := s.repo.ListRatings(r.Context(), st.ID, 100)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取评价失败")
		return
	}
	ok(w, map[string]any{"ok": true, "ratings": ratings})
}

// handleInsights 经营洞察：★ 只统计「用我们工具的外国客数据」，不是全店经营数据；
// 只做趋势·占比·排序，不编精确百分比。
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	sum, err := s.repo.RatingSummaryOf(r.Context(), st.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "统计失败")
		return
	}
	scans, _ := s.repo.CountEvents(r.Context(), st.ID, "g_scan")
	coupons, _ := s.repo.ListCoupons(r.Context(), st.ID, 200)
	redeemed := 0
	for _, c := range coupons {
		if c.Status == "redeemed" {
			redeemed++
		}
	}
	ratings, _ := s.repo.ListRatings(r.Context(), st.ID, 20)

	ok(w, map[string]any{
		"ok":             true,
		"rating":         sum,
		"scans":          scans,
		"coupons_issued": len(coupons),
		"coupons_redeemed": redeemed,
		"recent_ratings": ratings,
		"scope_note":     "只统计用我们工具的外国客数据，不是全店经营数据",
	})
}

// handleGetTemplate 取券模板。
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	tpl, err := s.repo.GetTemplateByStore(r.Context(), st.ID)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "暂无券模板")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取券模板失败")
		return
	}
	ok(w, map[string]any{"ok": true, "template": tpl})
}

// handleListCoupons 列出本店券（核销台）。
func (s *Server) handleListCoupons(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	coupons, err := s.repo.ListCoupons(r.Context(), st.ID, 100)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取券失败")
		return
	}
	ok(w, map[string]any{"ok": true, "coupons": coupons})
}

// handleRedeem 核销券（店员操作；校验券归属本店）。
func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Code) == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "请输入券码")
		return
	}

	existing, err := s.repo.GetCouponByCode(r.Context(), strings.TrimSpace(req.Code))
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "券码不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "查询券失败")
		return
	}
	if existing.StoreID != st.ID {
		fail(w, http.StatusForbidden, CodeForbidden, "该券不属于本店")
		return
	}

	c, err := s.repo.RedeemCoupon(r.Context(), existing.Code, "staff")
	if err != nil {
		fail(w, http.StatusBadRequest, CodeConflict, err.Error())
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "t_coupon_redeem", nil)
	ok(w, map[string]any{"ok": true, "coupon": c})
}
