// 本文件：客人端 AI 点菜助手 + 服务请求的 HTTP 接口。
//
// 与商家端（handlers_assist.go）刻意分开：两边的鉴权、知识源、守卫都不同。
//
//	商家端 handlers_assist.go    需登录；知识 = 平台产品知识 + 本店经营事实
//	客人端 本文件                不登录（扫桌码即用）；知识 = 本店菜品 + 服务（RAG）
//
// ── 为什么客人端不登录 ────────────────────────────────
//
// 外国客人扫个桌码就要他注册，转化直接归零。因此这里全程匿名，
// 用 cookie 派生的会话 ID 隔离多轮状态（见 guestSessionID）。
// 代价是无法按账号限流，所以改用 IP + 门店维度限流（见 allowGuestAssist）。
//
// ── 语音入口 ─────────────────────────────────────────
//
// 语音识别在浏览器端做（Web Speech API / 设备听写），转成文字后走同一个
// chat 接口。服务端不碰音频流：不存音频、不做转写，
// 既省掉一份存储与算力成本，也避免「录下客人说话」这类隐私 liability。
// 客人原话（note）会随服务请求一起带给服务员，方便理解上下文。
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// registerGuestAssist 注册客人端助手与服务请求路由（公开，不登录）。
func (s *Server) registerGuestAssist(mux *http.ServeMux) {
	// ── AI 点菜助手（语音转文字后走这里） ──
	mux.HandleFunc("POST /api/public/store/{slug}/assist", s.handleGuestAssist)
	mux.HandleFunc("POST /api/public/store/{slug}/assist/clear", s.handleGuestAssistClear)

	// ── 服务请求：客人发起、商家处理 ──
	mux.HandleFunc("POST /api/public/store/{slug}/requests", s.handleCreateGuestRequest)
	mux.HandleFunc("GET /api/stores/{id}/requests", s.withAuth(s.handleListGuestRequests))
	mux.HandleFunc("POST /api/stores/{id}/requests/{rid}/done", s.withAuth(s.handleDoneGuestRequest))
}

// guestSessionCookie 是客人匿名会话的 cookie 名。
// 值由服务端随机生成（16 字节 hex），不承载任何身份信息——
// 它的唯一作用是把同一个人的多轮对话归到一起。
const guestSessionCookie = "lc_guest"

// guestAssistQuestionLimit 问题长度上限（与商家端一致：语音转写可能很长，截断而非报错）。
const guestAssistQuestionLimit = 500

// guestAssistWindow / guestAssistMaxPerWindow 是单 IP 的限流参数。
//
// 为什么必须限流：这个接口不登录，却要花真金白银调模型。
// 不限流的话，一个脚本就能把门店的模型额度打欠费。
// 30 次 / 分钟对真人足够（想想没人会一分钟问 30 个问题）。
var (
	guestAssistWindow       = time.Minute
	guestAssistMaxPerWindow = 30
	guestAssistMu           sync.Mutex
	guestAssistHits         = map[string][]time.Time{}
)

// allowGuestAssist 判断该 IP 是否可以继续提问（滑动窗口，进程内计数）。
//
// ★ 进程内计数意味着多实例部署下各算各的：它是「防脚本打欠费」的护栏，
// 不是严格的配额系统。真要严格，得靠 Redis 或数据库计数——那是下一步的事，
// 现在的量级还用不上，先保证不会被一台机器的脚本打爆。
func allowGuestAssist(ip string) bool {
	now := time.Now()
	guestAssistMu.Lock()
	defer guestAssistMu.Unlock()
	hits := guestAssistHits[ip]
	kept := hits[:0]
	for _, t := range hits {
		if now.Sub(t) <= guestAssistWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= guestAssistMaxPerWindow {
		guestAssistHits[ip] = kept
		return false
	}
	guestAssistHits[ip] = append(kept, now)
	return true
}

// guestSessionID 取（或发）客人会话 ID。
//
// 没有 cookie 时现场生成一个并种下：客人第一次提问就开始计时，
// 流程态（多轮引导）与对话历史都挂在这个 ID 上。
func guestSessionID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(guestSessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	setGuestCookie(w, id)
	return id
}

func setGuestCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     guestSessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// handleGuestAssist 客人提问一次（语音转文字后也走这里）。
func (s *Server) handleGuestAssist(w http.ResponseWriter, r *http.Request) {
	if s.assist == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "点菜助手未启用")
		return
	}
	st, err := s.repo.GetStoreBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return
	}
	if !allowGuestAssist(clientIP(r)) {
		fail(w, http.StatusTooManyRequests, CodeRateLimit, "问得太快啦，稍等一下再问我")
		return
	}

	var req struct {
		Question string `json:"question"`
		Lang     string `json:"lang"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式不正确")
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "请先输入问题")
		return
	}
	if len([]rune(q)) > guestAssistQuestionLimit {
		q = string([]rune(q)[:guestAssistQuestionLimit])
	}

	// 语种校验：只认登记在册的语种（与 /api/translation/langs 同源）。
	// 不校验的话，传入任意字符串会让 names[lang] 永远查不到，
	// 表现为「整份菜单都没译名」，而真实原因只是语种码写错了。
	lang := strings.TrimSpace(req.Lang)
	if lang == "" {
		lang = service.SourceLang
	}
	if !service.IsSupportedLang(lang) {
		fail(w, http.StatusBadRequest, CodeValidation, "不支持的语种")
		return
	}
	langName := langCN(lang)

	// 装载 RAG 知识库：菜单 + 服务（★ 每次现取，不缓存——缓存旧菜单
	// 让助手推荐已下架的菜，比多查一次库严重得多）
	dishes, _ := s.repo.ListDishes(r.Context(), st.ID)
	tpl, _ := s.repo.GetTemplateByStore(r.Context(), st.ID)
	kb := assist.NewStoreKB(st, dishes, lang, langName, s.cfg.Currency)
	if tpl != nil && tpl.Enabled {
		kb.Service.Coupon = couponText(tpl, lang)
	}

	sid := guestSessionID(w, r)
	rep := s.assist.RespondGuest(r.Context(), assist.GuestAskInput{
		SessionID: sid,
		Question:  q,
		KB:        kb,
		LLM:       s.llmConfig(r.Context(), nil), // 租户维度：超管维护的平台级 / 租户级凭据
	})
	if rep == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "助手没有给出回答")
		return
	}

	// 埋点：客人用了 AI 助手（经营洞察里「AI 使用率」的来源）
	_ = s.repo.TrackEvent(r.Context(), st.ID, "g_assist", map[string]any{"lang": lang, "source": rep.Source})

	ok(w, map[string]any{
		"ok":       true,
		"reply":    rep,
		"lang":     lang,
		"store_id": st.ID,
	})
}

// handleGuestAssistClear 结束当前会话（清流程 + 历史）。
func (s *Server) handleGuestAssistClear(w http.ResponseWriter, r *http.Request) {
	sid := guestSessionID(w, r)
	if s.assist != nil {
		s.assist.ClearSession(r.Context(), sid)
	}
	ok(w, map[string]any{"ok": true})
}

/* ── 服务请求 ─────────────────────────────────────── */

// handleCreateGuestRequest 客人发起一条服务请求（服务铃 / 加水 / 加菜 / 买单）。
//
// ★ 这是 AI 助手所有动作的落点，也是「提议—确认」边界的执行处：
// 助手只给按钮，客人点一下才真正生成请求。助手永远不会代替客人下单。
func (s *Server) handleCreateGuestRequest(w http.ResponseWriter, r *http.Request) {
	st, err := s.repo.GetStoreBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "门店不存在")
		return
	}
	var req struct {
		Kind   string `json:"kind"`
		DishID string `json:"dish_id"`
		Qty    int    `json:"qty"`
		Note   string `json:"note"`
		Lang   string `json:"lang"`
		Ref    string `json:"ref"` // 桌码来源标记（t01 等）
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式不正确")
		return
	}
	if !domain.ValidRequestKind(req.Kind) {
		fail(w, http.StatusBadRequest, CodeValidation, "不支持的请求类型")
		return
	}
	if req.Qty <= 0 || req.Qty > 99 {
		req.Qty = 1
	}
	// note 截断：语音转写可能很长，服务员只需要理解意图，不需要全文
	if len([]rune(req.Note)) > 200 {
		req.Note = string([]rune(req.Note)[:200])
	}

	gr := &domain.GuestRequest{
		StoreID:  st.ID,
		TableRef: strings.TrimSpace(req.Ref),
		Kind:     req.Kind,
		Lang:     strings.TrimSpace(req.Lang),
		Note:     strings.TrimSpace(req.Note),
	}
	if req.Kind == domain.ReqAddDish {
		if strings.TrimSpace(req.DishID) == "" {
			fail(w, http.StatusBadRequest, CodeValidation, "加菜请求必须带上菜品 ID")
			return
		}
		// ★ 菜品必须属于本店：否则拿别家菜品 ID 就能给本店塞请求，
		// 服务员看到的是一个本店没有的菜，等于噪音。
		if _, derr := s.repo.GetDishByID(r.Context(), st.ID, strings.TrimSpace(req.DishID)); derr != nil {
			fail(w, http.StatusBadRequest, CodeValidation, "菜品不存在或不属于本店")
			return
		}
		gr.Payload = map[string]any{"dish_id": strings.TrimSpace(req.DishID), "qty": req.Qty}
	}
	if err := s.repo.CreateGuestRequest(r.Context(), gr); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "提交失败，请再试一次或直接叫服务员")
		return
	}
	ok(w, map[string]any{"ok": true, "request_id": gr.ID,
		"message": "已通知服务员，请稍等"})
}

// handleListGuestRequests 商家看服务请求（默认只看待办）。
func (s *Server) handleListGuestRequests(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.authorizeStore(w, r, r.PathValue("id"))
	if !ok2 {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !domain.ValidRequestStatus(status) {
		fail(w, http.StatusBadRequest, CodeValidation, "状态取值不正确")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	reqs, err := s.repo.ListGuestRequestsWithDish(r.Context(), st.ID, status, limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取请求失败")
		return
	}
	open, _ := s.repo.CountOpenGuestRequests(r.Context(), st.ID)
	ok(w, map[string]any{"ok": true, "requests": reqs, "open_count": open})
}

// handleDoneGuestRequest 标记请求完成（或取消）。
func (s *Server) handleDoneGuestRequest(w http.ResponseWriter, r *http.Request) {
	st, ok2 := s.authorizeStore(w, r, r.PathValue("id"))
	if !ok2 {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil || !domain.ValidRequestStatus(req.Status) {
		fail(w, http.StatusBadRequest, CodeValidation, "状态只能是 open / done / cancelled")
		return
	}
	if err := s.repo.SetGuestRequestStatus(r.Context(), st.ID, r.PathValue("rid"), req.Status); err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "请求不存在")
		return
	}
	ok(w, map[string]any{"ok": true, "status": req.Status})
}

/* ── 小工具 ───────────────────────────────────────── */

// langCN 语种码 → 中文名（提示词里指定「用哪种语言回答」用）。
// 取不到时回落语种码本身，不让提示词出现空值。
func langCN(code string) string {
	for _, l := range service.SupportedLangs {
		if l.Code == code {
			return l.NameZh
		}
	}
	return code
}

// couponText 券规则的人话说明（给提示词用，让助手能答「有什么优惠」）。
//
// ★ 刻意只给「规则」不给「券码」：助手是介绍优惠的工具，不是发券渠道。
// 真正的领券在评价之后由系统发（见 handleSubmitRating）。
func couponText(tpl *domain.CouponTemplate, lang string) string {
	if content, ok := tpl.Content[lang].(map[string]any); ok {
		if txt, ok := content["text"].(string); ok && txt != "" {
			return "店内优惠：" + txt
		}
	}
	return "完成评价后可领取店内优惠券。"
}

// clientIP 取客户端 IP（限流维度）。
// 只取 IP 不解析 X-Forwarded-For 的全链：反代是否可信由部署决定，
// 这里刻意保守——直接用 RemoteAddr，宁可放过几个分布式请求，
// 也不让一个伪造头就把限流整个绕过。
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	return host
}
