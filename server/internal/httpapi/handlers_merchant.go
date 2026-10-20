// 本文件：商家端接口 —— 门店、二维码、原文确认稿、多语翻译、菜单状态、评价、经营洞察与券。
//
// 贯穿全文件的几条硬规则（改代码前请先确认没违反）：
//
//   - 每个 handler 的第一件事都是 requireStore：门店必须存在且属于当前账号；
//   - 「原文确认稿」（source_draft）是唯一真值，未确认不允许翻译（硬规则）；
//   - 译文一律落 dish.names_json，name_zh 永远是原文真值，不可被翻译覆盖；
//   - 任何未命中的译文都留空，由前端回落显示原文并标注「未翻译」——绝不编造。
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// requireStore 校验门店存在且当前账号有权访问。
//
// 返回 (nil, false) 时错误响应已经写好，调用方直接 return 即可。
// 这是商家端唯一的越权防线：路径里的 {id} 完全由客户端控制，
// 若不校验归属，任何人拿到一个门店 ID 就能读到别家的菜单、评价与经营数据。
//
// 三种失败刻意分成三档：门店不存在 → 404，读库失败 → 500，不是你的 → 403。
//
// ★ 访问规则由 repo.CanAccessStore 单点判定（与 ListStoresForViewer 同一套规则）：
//   - 门店 owner：任何角色都能碰自己的店；
//   - org_admin：本组织及其子组织下的门店；
//   - tenant_admin：本租户全部门店；
//   - super_admin：默认租户（平台自营）的门店；跨租户查看走超管接口。
func (s *Server) requireStore(w http.ResponseWriter, r *http.Request) (*domain.Store, bool) {
	return s.authorizeStore(w, r, r.PathValue("id"))
}

// authorizeStore 按显式 ID 校验门店（存在 + 有权访问）。
//
// 与 requireStore 的差别只在于 ID 来源：requireStore 取路径参数
// （/api/stores/{id}/... 形态），本函数取请求体字段。
// ★ 为什么要拆出来：AI 经营助手的 store_id 在 JSON body 里，
// 若硬套 requireStore，PathValue("id") 会拿到空串，报错变成「门店不存在」——
// 一个与真实原因（参数位置不同）毫无关系的提示，排障时会被带偏。
func (s *Server) authorizeStore(w http.ResponseWriter, r *http.Request, id string) (*domain.Store, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "缺少门店 ID")
		return nil, false
	}
	st, err := s.repo.GetStoreByID(r.Context(), id)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return nil, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return nil, false
	}
	if !s.repo.CanAccessStore(r.Context(), currentAccount(r), st) {
		fail(w, http.StatusForbidden, CodeForbidden, "无权访问该门店")
		return nil, false
	}
	return st, true
}

// storeLinks 返回门店页与两种码的链接（门口码 ref=door / 桌码 ref=t01）。
//
// ref 会随扫码一并上报，是区分「路过扫门口码」与「坐下扫桌码」的归因依据，
// 也是经营洞察里扫码数（g_scan 事件）的唯一来源。
// store_page / door / table 是绝对地址（可直接印刷或分享），
// qr_door / qr_table 是相对路径（前端拿它去请求二维码 PNG）。
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

// nilIfEmpty 把「空串或纯空白」转成 nil，否则返回原值指针。
//
// repo.StorePatch 用指针区分两种语义：nil = 不更新该字段，非 nil = 更新为这个值。
// 这里把前端传来的空串统一成 nil，避免把「用户没填」误当成「清空原值」。
func nilIfEmpty(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

// handleListStores 列出当前账号可见的门店。
//
// manager / staff 只看自己名下的店；tenant_admin 看本租户全部门店；
// 超管看默认租户（平台自营）的门店。见 repo.ListStoresForViewer。
func (s *Server) handleListStores(w http.ResponseWriter, r *http.Request) {
	stores, err := s.repo.ListStoresForViewer(r.Context(), currentAccount(r))
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}
	ok(w, map[string]any{"ok": true, "stores": stores})
}

// handleCreateStore 创建门店；★ 注册后自动配好复购券模板（发券免费）+ 生成双码链接。
//
// 这是「注册 → 建店 → 拿到物料」主链路的收口：商家只需输入一个店名，
// slug、券模板与二维码链接全部自动生成。刻意不让商家做任何配置决策——
// 决策越多，从注册到「门口贴上码」的转化越低。
//
// ★ 角色门禁：只有 manager 及以上能建店（staff 是来核销券的，不该能开新店）。
// ★ 租户继承：新门店归属创建者所在租户，这是数据隔离的起点。
//
// slug 由 idgen.Slug() 生成且此后不可变：它被印在物料上，改一次全部作废。
func (s *Server) handleCreateStore(w http.ResponseWriter, r *http.Request) {
	acc, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleManager))
	if !allowed {
		return
	}
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

	// ★ 归属口径走 effectiveTenant 而不是 acc.TenantID（2026-10-19 Q3 修订）：
	// 平台超管的 tenant_id 是空串（平台级账号），直接拿它当门店归属会写出
	// 一条「无主门店」——它不属于任何租户，所有按租户的查询都看不见它，
	// 而它又占着 slug 和二维码。0007 给 store.tenant_id 加了外键后，空串
	// 会直接被数据库拒掉变成 500，所以这里必须落到真实租户：超管显式传
	// ?tenant_id 时用之，否则落默认租户。
	// 普通商家不受影响：effectiveTenant 对非超管就是返回 acc.TenantID。
	st, err := s.repo.CreateStore(r.Context(), acc.ID, effectiveTenant(r, acc), acc.OrgID,
		strings.TrimSpace(req.Name), idgen.Slug())
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "创建门店失败")
		return
	}

	// 注册后补填（WiFi 可选，不设必填）
	// 地址与 WiFi 都是「有则更好」的信息：填了就顺手存下；
	// 补填失败不阻断建店主流程，st 保持创建时的结果即可。
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
	// 发券免费：券不卡在付费墙之后，否则这两个指标就失去了客观数据源。
	// 模板创建失败也照样返回成功——门店已经建好了，模板缺失可由后续请求补建。
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
//
// 一次请求喂饱商家端首页，避免前端为拼一个页面打三四个接口。
// 确认稿采用「有则带上」的返回方式（draft 字段可缺省），
// 前端据此区分「还没上传菜单」与「已上传待确认」两种状态。
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
//
// 所有字段都用指针接收，语义是「传了才改」，前端可以只提交变更的那几个键。
// 两个枚举字段在这里做白名单校验：它们是产品的核心分档，
// 一旦写入未知值，前端渲染与后续统计都会失准，故宁可在入口挡掉。
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
	// service_tier：pure 纯翻译档 / service 外客服务档
	if req.ServiceTier != nil && *req.ServiceTier != "pure" && *req.ServiceTier != "service" {
		fail(w, http.StatusBadRequest, CodeValidation, "档位只能是 pure / service")
		return
	}
	// order_mode：assist 辅助型 / replace 替代型
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
//
// ref 缺省为 door。二维码内容就是门店页地址，因此即便将来路由规则演进，
// 已印刷的旧码依然指向同一个页面——这是「物料不可变」承诺的技术保障。
// 直接把 PNG 写进响应体（不落盘、不返回对象存储 URL），
// 保证「扫到的就是当下生成的内容」，也省掉一层存储依赖。
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
// 无确认稿时返回 NOT_FOUND（而不是空对象），让前端能明确区分「没有」与「有但内容为空」。
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
//
// 确认稿是「唯一真值」：OCR 结果、商家手工修正后的菜单都存这里，
// 只有它被确认（handleConfirmDraft）之后才允许翻译。
// lang 缺省 zh —— 本期原文固定中文，但字段保留，架构上给多原文语种留了口子。
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
//
// 这是翻译链路的闸门：确认后原文被冻结为真值，所有译文都以它为准，
// 因此责任主体明确落在商家身上（确认稿错了，译文自然跟着错）。
// draft_id 可省略，省略时取最新一份——前端通常不关心具体版本号。
//
// ★ Q1（2026-10-19 用户拍板）：确认即把原文菜品落库。原先 dish 行只在
// 翻译时才写，「已确认但未翻译」的店在客人端是**空菜单**——商家以为
// 客人已能看到菜，实际客人只看到一个空页面。现在确认就落原文行
// （names_json 为空），客人端按既有 L3 纪律渲染「原文 + 未翻译标」，
// 翻译再在同一批行上补 names_json（见 handleTranslate 的同样装配）。
func (s *Server) handleConfirmDraft(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		DraftID string `json:"draft_id"`
	}
	_ = decodeJSON(r, &req) // 允许空 body：不传 draft_id 就取最新稿

	// 取「要被确认的那一稿」：显式指定按 ID 取（读错稿会落错菜单），否则取最新
	var draft *domain.SourceDraft
	if id := strings.TrimSpace(req.DraftID); id != "" {
		d, err := s.repo.GetDraftByID(r.Context(), st.ID, id)
		if err != nil {
			fail(w, http.StatusBadRequest, CodeValidation, "确认稿不存在")
			return
		}
		draft = d
	} else {
		d, err := s.repo.GetLatestDraft(r.Context(), st.ID)
		if err != nil {
			fail(w, http.StatusBadRequest, CodeValidation, "请先上传菜单")
			return
		}
		draft = d
	}

	if err := s.repo.ConfirmDraft(r.Context(), st.ID, draft.ID); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "确认失败")
		return
	}
	// 落库原文菜品（未翻译态）：客人端立即有菜单可看，商家端也能据此提醒「还没翻译」
	if err := s.repo.ReplaceDishes(r.Context(), st.ID, s.dishesFromDraft(r.Context(), st.ID, pickedDraftItems(draft.Items))); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "写入菜单失败")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_source_confirmed", nil)
	ok(w, map[string]any{"ok": true, "draft_id": draft.ID})
}

// pickedDraftItems 取出「要落库的条目」：只收勾选的；一个都没勾视为全选。
//
// 与 handleTranslate 的勾选口径必须一致——确认落库的集合与翻译落库的集合
// 如果是两套规则，客人端会在「确认后」与「翻译后」看到不同的菜单。
func pickedDraftItems(items []domain.DraftItem) []domain.DraftItem {
	picked := make([]domain.DraftItem, 0, len(items))
	for _, it := range items {
		if it.Selected {
			picked = append(picked, it)
		}
	}
	if len(picked) == 0 {
		return items
	}
	return picked
}

// dishesFromDraft 把确认稿条目组装成菜品行，并从既有菜品继承动态状态与译文。
//
// 抽成共用函数是因为确认（handleConfirmDraft）与翻译（handleTranslate）走同一
// 条装配路径：确认落原文行，翻译在同一批行上补 names_json。两边逻辑一致才能
// 保证「确认后看到的菜单」与「翻译后看到的菜单」身份与状态不错位。
// 继承项：售罄 / 每日剩余 / 属性 / 已有译文（重新确认不该毁掉已完成的翻译）。
func (s *Server) dishesFromDraft(ctx context.Context, storeID string, items []domain.DraftItem) []domain.Dish {
	existing, _ := s.repo.ListDishes(ctx, storeID)
	prev := map[string]domain.Dish{}
	for _, x := range existing {
		prev[x.NameZh] = x
	}
	dishes := make([]domain.Dish, 0, len(items))
	for i, it := range items {
		dish := domain.Dish{
			NameZh: it.Name, Price: it.Price, Category: it.Category, SortOrder: i,
			// 默认属性：不辣 + 无过敏原（attrs 为空时前端也要能正常渲染）
			Attrs: map[string]any{"spicy": 0, "allergens": []string{}},
			Names: map[string]string{},
		}
		// 同名的旧菜：把动态状态与既有译文继承过来，避免「确认一次就丢售罄/译文」
		if old, ok := prev[it.Name]; ok {
			dish.SoldOut = old.SoldOut
			dish.Remaining = old.Remaining
			if len(old.Attrs) > 0 {
				dish.Attrs = old.Attrs
			}
			for k, v := range old.Names {
				dish.Names[k] = v
			}
		}
		dishes = append(dishes, dish)
	}
	return dishes
}

// distinctCategories 取出菜品里出现过的分类名（去重、保序、剔除空值）。
//
// 保序很重要：分类顺序要与菜单呈现顺序一致，前端不做二次排序。
// 空分类被跳过——没有分类的菜不该凭空多出一个空标题区块。
func distinctCategories(dishes []domain.Dish) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range dishes {
		c := strings.TrimSpace(d.Category)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// translateCategories 把分类名翻成各目标语种，返回 {lang: {原文: 译文}}。
// 分类是每店少量且稳定的词，翻一次长期复用（走 TM 缓存，成本可忽略）；
// 未命中的分类留空，前端回落显示原文——不编造。
//
// 为什么分类要单独翻：若菜名多语化了、分类标题还是中文，客人端会呈现
// 「外语菜名 + 中文分类」的割裂页面，体验反而比全中文更差。
func (s *Server) translateCategories(ctx context.Context, from string, cats, targets []string) map[string]map[string]string {
	if len(cats) == 0 {
		return nil
	}
	out := map[string]map[string]string{}
	for _, tg := range targets {
		res, err := s.tr.Translate(ctx, cats, from, tg)
		// 该语种整批失败且无任何结果：跳过，不影响其它语种
		if err != nil && len(res) == 0 {
			continue
		}
		m := map[string]string{}
		for i, c := range cats {
			if i < len(res) && res[i] != "" {
				m[c] = res[i]
			}
		}
		// 该语种一个都没翻出来就不建空表，保持响应干净
		if len(m) > 0 {
			out[tg] = m
		}
	}
	return out
}

// mergeCategoryNames 把新翻到的分类译文并入既有表（同语种内后写覆盖，便于人工校正生效）。
//
// 采用增量合并而非整体替换：已确认的旧译文不能因为「这次只翻了几道菜的分类」而丢失。
// 后写覆盖是为了让商家的人工校正能生效——新结果总是更新的一方。
func mergeCategoryNames(dst map[string]map[string]string, add map[string]map[string]string) map[string]map[string]string {
	if len(add) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]map[string]string{}
	}
	for lang, m := range add {
		if dst[lang] == nil {
			dst[lang] = map[string]string{}
		}
		for k, v := range m {
			dst[lang][k] = v
		}
	}
	return dst
}

// handleTranslate 多语化（P4）：只翻译勾选的菜；未确认的确认稿拒绝翻译。
// 支持一次翻多个语种（targets），并保留已有的售罄/剩余状态与既有译文。
//
// ★ 不变量：
//   - 未确认的确认稿必须返回 VALIDATION，这是硬规则，不允许绕过；
//   - name_zh 始终是原文真值，翻译只写 names_json，绝不改 name_zh；
//   - name_en 是给旧代码的兼容字段，必须与 names["en"] 保持一致；
//   - 已有译文默认跳过，既省配额，也避免覆盖人工校对过的结果。
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	var req struct {
		Targets []string `json:"targets"`
	}
	_ = decodeJSON(r, &req) // 允许空 body：targets 为空时由 normalizeTargets 兜底为 en
	targets := normalizeTargets(req.Targets, "")

	d, err := s.repo.GetLatestDraft(r.Context(), st.ID)
	if errors.Is(err, repo.ErrNotFound) {
		fail(w, http.StatusBadRequest, CodeValidation, "请先上传菜单")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取确认稿失败")
		return
	}
	// ★ 硬规则：原文确认稿未确认，不允许翻译（真值未定，译文必然不可信）
	if d.ConfirmedAt == nil {
		fail(w, http.StatusBadRequest, CodeValidation, "原文确认稿未确认，不允许翻译")
		return
	}

	// 只翻勾选的菜；一个都没勾 ⇒ 视为全选
	// （「没勾」多数是前端还没实现勾选，此时全选比什么都不翻更符合预期）
	picked := pickedDraftItems(d.Items)

	// 与确认落库同一套装配：原文行 + 继承动态状态/既有译文（见 dishesFromDraft）
	dishes := s.dishesFromDraft(r.Context(), st.ID, picked)

	// 逐语种翻译：已有译文的跳过（省配额，也避免覆盖人工校对过的结果）
	translatedTotal := 0
	for _, tg := range targets {
		// 只把该语种缺失的菜名送去翻译，并记下它们在 dishes 中的下标以便回填
		var idx []int
		var texts []string
		for i := range dishes {
			if dishes[i].Names[tg] == "" {
				idx = append(idx, i)
				texts = append(texts, dishes[i].NameZh)
			}
		}
		if len(texts) == 0 {
			continue
		}
		out, terr := s.tr.Translate(r.Context(), texts, d.Lang, tg)
		// 整批失败且无结果：跳过该语种（其余语种与主流程不受影响）
		if terr != nil && len(out) == 0 {
			continue
		}
		// 按下标回填；空串表示未命中，留空由前端回落显示原文——不编造
		for j, i := range idx {
			if j < len(out) && out[j] != "" {
				dishes[i].Names[tg] = out[j]
				translatedTotal++
			}
		}
	}
	// name_en 是给旧代码的兼容字段，必须与 names["en"] 保持一致
	for i := range dishes {
		dishes[i].NameEn = dishes[i].Names["en"]
	}

	if err := s.repo.ReplaceDishes(r.Context(), st.ID, dishes); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "写入菜单失败")
		return
	}

	// 分类名同批多语化：否则客人端会出现「外语菜名 + 中文分类标题」的割裂页面
	cats := distinctCategories(dishes)
	catNames := mergeCategoryNames(st.CategoryNames, s.translateCategories(r.Context(), d.Lang, cats, targets))
	if err := s.repo.UpdateStoreCategoryNames(r.Context(), st.ID, catNames); err != nil {
		// 分类翻译失败不阻断主流程：菜名已多语化，分类回落原文即可
		_ = err
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "e_translate_done", nil)

	// 回读落库后的成品再返回，保证前端拿到的就是「已持久化的事实」
	saved, _ := s.repo.ListDishes(r.Context(), st.ID)
	ok(w, map[string]any{
		"ok":               true,
		"dishes":           saved,
		"translated_count": translatedTotal,
		"targets":          targets,
		"category_names":   catNames,
		"engine":           s.tr.Name(),
	})
}

// handleListDishes 列出菜单。
// 返回的是多语化成品（含 names / sold_out / remaining），客人端与商家端共用同一份数据。
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
//
// 菜名、价格、分类在翻译时已定型，日常只需维护「今天还有没有」这类动态状态，
// 因此这里只开这两个字段的口子，避免误改已多语化的内容。
// 找不到菜品返回 NOT_FOUND（repo 层把「影响 0 行」也视为不存在）。
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

// handleUploadDishImage 上传菜品图片（multipart，字段名 image，≤5MB）。
//
// 为什么走 multipart 而不是 base64 JSON：图片大了之后 base64 会让体积膨胀 1/3，
// 且 multipart 天然流式，配合 http.MaxBytesReader 可以在读取阶段就截断超大文件，
// 不必先把整个恶意文件读进内存。
//
// 流程：requireStore → 取文件 → 落盘（service 负责限流与魔数校验）→ 写库 →
// 删旧文件。删旧文件放在写库成功之后，避免「库没改成、图先没了」的中间态。
// 旧文件删除失败不影响主流程（孤儿文件可由运维清理），故只记日志不阻断。
func (s *Server) handleUploadDishImage(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	dishID := r.PathValue("dishId")

	// 在读 body 之前先设上限：超过上限的请求在读文件阶段就会被截断，
	// multipart 解析随后会报错，等价于拒绝（而不是把 5GB 读进内存再判断）。
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxImageBytes+64<<10) // +64KB 给 multipart 头部留余量
	fh, _, err := r.FormFile("image")
	if err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请上传图片文件")
		return
	}
	defer func() { _ = fh.Close() }()

	old, _ := s.repo.GetDishImage(r.Context(), st.ID, dishID)

	url, err := s.images.Save(fh)
	if err != nil {
		// 区分两种失败给不同人话：超限让人去压图，格式不对让人换个图
		switch {
		case errors.Is(err, service.ErrTooLarge):
			fail(w, http.StatusBadRequest, CodeValidation, "图片不能超过 5MB，请压缩后重试")
		case errors.Is(err, service.ErrBadType):
			fail(w, http.StatusBadRequest, CodeValidation, "仅支持 PNG / JPEG / WebP / GIF 图片")
		default:
			log.Printf("保存菜品图片失败 dish=%s: %v", dishID, err)
			fail(w, http.StatusInternalServerError, CodeInternal, "图片保存失败")
		}
		return
	}

	// 写库失败要顺手删掉刚落的文件，否则又会多一个孤儿文件
	if err := s.repo.SetDishImage(r.Context(), st.ID, dishID, url); err != nil {
		if derr := s.images.Delete(url); derr != nil {
			log.Printf("回滚菜品图片失败 url=%s: %v", url, derr)
		}
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "菜品不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "保存图片失败")
		return
	}

	// 旧文件与新旧不同才删：重复上传同一地址时 url 必然不同（文件名随机），
	// 因此这里做一次不等式保护只是防御，实际总会成立。
	if old != "" && old != url {
		if err := s.images.Delete(old); err != nil {
			log.Printf("删除旧菜品图片失败 url=%s: %v", old, err)
		}
	}
	ok(w, map[string]any{"ok": true, "image_url": url})
}

// handleDeleteDishImage 删除菜品图片（文件与库记录一并清理）。
//
// 先删库还是先删文件：先删文件。若先删库而删文件失败，前端会认为已无图，
// 但文件还占着磁盘且失去引用；反过来最坏只是「库里还有一条指向已删文件的 URL」，
// 客人端 <img> 加载失败会自然显示占位块。两种错法都选影响面小的那个。
func (s *Server) handleDeleteDishImage(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.requireStore(w, r)
	if !ok2 {
		return
	}
	dishID := r.PathValue("dishId")

	old, _ := s.repo.GetDishImage(r.Context(), st.ID, dishID)
	if err := s.repo.SetDishImage(r.Context(), st.ID, dishID, ""); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "菜品不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "删除图片失败")
		return
	}
	if old != "" {
		if err := s.images.Delete(old); err != nil {
			log.Printf("删除菜品图片文件失败 url=%s: %v", old, err)
		}
	}
	ok(w, map[string]any{"ok": true})
}

// handleListRatings 列出评价（商家端看中文译文）。
//
// 客人用母语写评论、系统翻成中文给商家看（CommentZh），
// 翻译不可用时该字段为空，前端回落显示原文——不编造。
// 固定取最近 100 条：商家端的诉求是「看近期反馈」，不做全量分页。
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
//
// 口径必须诚实：这些数字只覆盖扫了我们二维码、用了我们工具的客人，
// 因此响应里带 scope_note 明确标注口径，避免商家误把它当成全店营业额来决策。
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
	// 扫码数来自埋点事件（g_scan）；券数取最近 200 张用于算核销率
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
		"ok":               true,
		"rating":           sum,
		"scans":            scans,
		"coupons_issued":   len(coupons),
		"coupons_redeemed": redeemed,
		"recent_ratings":   ratings,
		"scope_note":       "只统计用我们工具的外国客数据，不是全店经营数据",
	})
}

// handleGetTemplate 取券模板。
// 模板在建店时自动生成，正常不会缺失；缺失时返回 NOT_FOUND 供前端提示重建。
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
// 核销台要能一眼看到「哪些还没核销」，故按发放时间倒序取最近 100 张。
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
//
// ★ 两条必须守住的不变量：
//   - 券必须属于本店：先按券码查出券，比对 StoreID，跨店核销返回 FORBIDDEN
//     （否则任何店员拿到一个券码就能核销别家的券，等于白送）；
//   - 重复核销必须拒绝：由 repo.RedeemCoupon 在事务内校验状态与有效期后落库，
//     并把冲突原因作为 CONFLICT 返回（重复核销 / 券已过期）。
//
// 这里先查一次再核销，是为了能给出「不属于本店」这种精确错误；
// 真正的状态判定仍以事务内的原子校验为准，避免并发下的双重核销。
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
	// ★ 归属校验：券不属于本店直接拒绝，绝不允许跨店核销
	if existing.StoreID != st.ID {
		fail(w, http.StatusForbidden, CodeForbidden, "该券不属于本店")
		return
	}

	// 原子核销：事务内校验「未核销 + 未过期」，重复核销会返回错误
	c, err := s.repo.RedeemCoupon(r.Context(), existing.Code, "staff")
	if err != nil {
		fail(w, http.StatusBadRequest, CodeConflict, err.Error())
		return
	}
	_ = s.repo.TrackEvent(r.Context(), st.ID, "t_coupon_redeem", nil)
	ok(w, map[string]any{"ok": true, "coupon": c})
}
