// 本文件：AI 经营助手的 HTTP 层 —— 对话问答、对话历史，以及超管侧的 LLM 凭据维护。
//
// 设计来源：/Users/zhangzifei/Desktop/翻译助手 的 internal/assist（接待引擎）。
// 逻辑机制一致，服务对象不同：那边接待 C 端访客，这边接待商家。
//
// ── 一条问答的完整链路 ───────────────────────────────────
//
//	POST /api/assist/chat {store_id, question}
//	  1. requireStore    校验门店归属（越权 FORBIDDEN）
//	  2. storeFacts      ★ 由服务端从库里算出事实（数字的唯一合法来源）
//	  3. engine.Respond  四道主体 + 出站守卫（数字核验 / 承诺边界）
//	  4. 落库            用户问题 + 助手回答各一条，便于回看与排障
//	  5. 返回            content + actions + source（source 供前端标注「规则/模型」）
//
// ── 为什么事实必须服务端算 ───────────────────────────────
//
// 模型会把「最近 20 条评价」讲成「所有客人」，会把 4.2 分讲成 4.5 分。
// 数字一旦由模型生成，就没有任何机制能验证它对不对。因此事实只能我们算，
// 模型只负责组织语言；引擎侧还有一道数字核验兜底（见 assist/engine.go）。
//
// ── 凭据维护权限（与翻译 Key 完全一致） ──────────────────
//
// 写操作只归平台超管（用户明确要求「不是商家维护 key，是超管维护 key」）：
// LLM 调用是真金白银的成本，key 泄露影响的是平台侧额度。
// 列表接口对租户管理员只读开放（看脱敏状态，便于判断助手是否可用）。
package httpapi

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

// 对话长度的上限：问题是给人看的闲聊，不是 prompt 注入的载体，
// 但截断仍是必要的——超长输入会稀释提示词里的【事实】权重。
const assistQuestionLimit = 500

// registerAssist 注册 AI 经营助手路由（在 router.go 的 Routes 中调用）。
func (s *Server) registerAssist(mux *http.ServeMux) {
	// ── 商家侧对话（需登录，门店作用域） ──
	mux.HandleFunc("POST /api/assist/chat", s.withAuth(s.handleAssistChat))
	mux.HandleFunc("GET /api/assist/history", s.withAuth(s.handleAssistHistory))
	mux.HandleFunc("POST /api/assist/clear", s.withAuth(s.handleAssistClear))

	// ── 超管侧 LLM 凭据维护 ──
	// 列表对租户管理员只读开放（脱敏），写操作仅超管（见 superAICredAdmin）。
	mux.HandleFunc("GET /api/admin/ai-credentials", s.withAuth(s.handleListAICredentials))
	mux.HandleFunc("POST /api/admin/ai-credentials", s.withAuth(s.handleCreateAICredential))
	mux.HandleFunc("POST /api/admin/ai-credentials/{id}/status", s.withAuth(s.handleSetAICredentialStatus))
	mux.HandleFunc("POST /api/admin/ai-credentials/{id}/rotate", s.withAuth(s.handleRotateAICredential))
	mux.HandleFunc("POST /api/admin/ai-credentials/{id}/reveal", s.withAuth(s.handleRevealAICredential))
	mux.HandleFunc("DELETE /api/admin/ai-credentials/{id}", s.withAuth(s.handleDeleteAICredential))
	mux.HandleFunc("POST /api/admin/ai-credentials/{id}/test", s.withAuth(s.handleTestAICredential))
}

// handleAssistChat 一次问答。
//
// store_id 允许为空：不传时是「平台级问答」（只答产品怎么用，不含本店数字）。
// 传了就必须有权访问该门店——否则任何账号都能拿别家经营数据提问。
func (s *Server) handleAssistChat(w http.ResponseWriter, r *http.Request) {
	if s.assist == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "经营助手未启用")
		return
	}
	var req struct {
		StoreID  string `json:"store_id"`
		Question string `json:"question"`
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
	// 超长输入直接截断而不是报错：商家粘贴一整段菜单进来问「这翻得对吗」是合理场景。
	if len([]rune(q)) > assistQuestionLimit {
		q = string([]rune(q)[:assistQuestionLimit])
	}

	storeID := strings.TrimSpace(req.StoreID)
	storeName := ""
	facts := []assist.Fact{}
	if storeID != "" {
		st, allowed := s.authorizeStore(w, r, storeID)
		if !allowed {
			return
		}
		storeID, storeName = st.ID, st.Name
		f, err := s.storeFacts(r.Context(), st.ID, st.Name)
		if err != nil {
			// 事实算不出来不影响问答：降级成「无事实」模式，助手只说产品能力。
			// 但要把错误打进日志——统计失败本身就是故障信号。
			logAssistError("统计门店事实失败", err)
		} else {
			facts = f
		}
	}

	acc := currentAccount(r)
	llm := s.llmConfig(r.Context(), acc)

	rep := s.assist.Respond(r.Context(), assist.AskInput{
		AccountID: acc.ID,
		Question:  q,
		UI:        "zh", // 商家端本期固定中文（预留多语）
		Facts:     facts,
		StoreName: storeName,
		LLM:       llm,
	})
	if rep == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "助手没有给出回答")
		return
	}

	// 落库：用户问题与助手回答各一条。写失败不阻塞问答（历史是附属价值，不是前提）。
	_ = s.repo.AppendAssistMessage(r.Context(), acc.ID, storeID, repo.AssistRoleUser, q, "")
	_ = s.repo.AppendAssistMessage(r.Context(), acc.ID, storeID, repo.AssistRoleAssistant, rep.Content, rep.Source)
	_ = s.repo.TrimAssistMessages(r.Context(), acc.ID, repo.AssistHistoryLimit)

	ok(w, map[string]any{
		"ok":       true,
		"reply":    rep,
		"store_id": storeID,
		// 助手能力提示：前端据此在无 LLM 时显示「当前为规则模式」
		"assist_llm": llm.Enabled(),
	})
}

// handleAssistHistory 取对话历史（按时间正序，前端直接渲染）。
func (s *Server) handleAssistHistory(w http.ResponseWriter, r *http.Request) {
	acc := currentAccount(r)
	storeID := strings.TrimSpace(r.URL.Query().Get("store_id"))
	msgs, err := s.repo.ListAssistMessages(r.Context(), acc.ID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取对话历史失败")
		return
	}
	ok(w, map[string]any{"ok": true, "messages": msgs})
}

// handleAssistClear 清空对话（「重新开始」按钮）。
// 同时结束在途流程：历史都清了，接着追问上半句没有意义。
func (s *Server) handleAssistClear(w http.ResponseWriter, r *http.Request) {
	acc := currentAccount(r)
	storeID := strings.TrimSpace(r.URL.Query().Get("store_id"))
	if err := s.repo.ClearAssistMessages(r.Context(), acc.ID, storeID); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "清空对话失败")
		return
	}
	if s.assist != nil {
		s.assist.ClearSession(r.Context(), acc.ID)
	}
	ok(w, map[string]any{"ok": true})
}

/* ── 事实构造（第一纪律：数字只能来自服务端） ─────────── */

// llmConfig 解析当前请求该用的 LLM 配置（商家端：账号即租户）。
//
// 账号没有租户（平台级超管）时按平台级凭据解析（tenantID 为空串正好命中
// repo 里的 tenant_id = ” 分支），因此这里不需要特判。
// acc 为 nil 时返回零值——引擎走确定性兜底。给客人接租户级凭据是
// FIX-7 的 guestLLMConfig（按门店租户，不走账号）。
// 拿不到凭据时也返回零值——LLMConfig.Enabled() 为 false，
// 引擎自动走确定性兜底，商家仍能得到「话术直配 / 知识库」级别的回答。
func (s *Server) llmConfig(ctx context.Context, acc *domain.Account) assist.LLMConfig {
	if acc == nil {
		return assist.LLMConfig{}
	}
	return s.llmConfigForTenant(ctx, acc.TenantID)
}

// llmConfigForTenant 按租户解析 LLM 配置（先租户后平台；都没有则零值走规则兜底）。
//
// 这是商家端与客人端共用的解析核心：两条路径的差别只在「租户从哪来」
// （商家端取自账号，客人端取自门店），解析规则完全一致。
func (s *Server) llmConfigForTenant(ctx context.Context, tenantID string) assist.LLMConfig {
	if s.aiCred == nil {
		return assist.LLMConfig{}
	}
	cred, err := s.aiCred.Credential(ctx, tenantID)
	if err != nil || cred == nil {
		return assist.LLMConfig{}
	}
	return assist.LLMConfig{BaseURL: cred.BaseURL, APIKey: cred.APIKey, Model: cred.Model}
}

// storeFacts 把门店统计转成助手可引用的事实表。
//
// ★ 口径刻意与 /api/stores/{id}/insights 保持一致（同一批 repo 调用），
// 并在最后追加一条「口径说明」——助手说「扫码 12 次」时，要知道这只是
// 「用我们工具扫的码」，不是全店客流。口径不一致比数字错更危险。
func (s *Server) storeFacts(ctx context.Context, storeID, storeName string) ([]assist.Fact, error) {
	sum, err := s.repo.RatingSummaryOf(ctx, storeID)
	if err != nil {
		return nil, err
	}
	scans, _ := s.repo.CountEvents(ctx, storeID, "g_scan")
	coupons, _ := s.repo.ListCoupons(ctx, storeID, 200)
	redeemed := 0
	for _, c := range coupons {
		if c.Status == "redeemed" {
			redeemed++
		}
	}
	dishes, _ := s.repo.ListDishes(ctx, storeID)
	untranslated := 0
	for _, d := range dishes {
		if len(d.Names) == 0 {
			untranslated++
		}
	}

	st := map[string]any{
		"门店名":           storeName,
		"菜品评分均分":        round1(sum.AvgDish),
		"服务评分均分":        round1(sum.AvgService),
		"复购意愿均分":        round1(sum.AvgReturn),
		"评价条数":          sum.Count,
		"扫码次数（我们工具内）":   scans,
		"发券张数（最近200张内）": len(coupons),
		"已核销张数":         redeemed,
		"菜品总数":          len(dishes),
		"尚未翻译菜品数":       untranslated,
	}
	facts := assist.FactsFromStats(st)
	facts = append(facts,
		assist.Fact{Key: "scope", Label: "口径说明", Value: "以上只统计使用我们工具的外国客数据，不是全店经营数据"},
	)
	return facts, nil
}

// round1 保留一位小数（评分用；整数也不显示 .0，更自然）。
func round1(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// logAssistError 助手内部错误的统一出口（当前打标准错误；后续可埋点）。
func logAssistError(msg string, err error) { log.Printf(msg+": %v", err) }
