// 本文件：AI 经营助手的 LLM 凭据维护接口（超管侧）。
//
// 权限模型（与翻译 Key 的 handlers_admin.go 一致，用户明确要求「不是商家维护 key，
// 是超管维护 key」）：
//
//	平台超管      全部可写：新增 / 轮换 / 启停 / 删除 / 查看明文 / 试译
//	租户管理员    只读：只能看「平台级 + 本租户」的脱敏状态（判断助手能不能用）
//	manager / staff  无权限
//
// 为什么 LLM 凭据也不给商家自维护：调用是真金白银的成本，
// key 泄露影响的是平台侧额度；且一个店手滑填错 base_url 会让整租户助手失灵。
// 商家只需要知道「现在助手是规则模式还是模型模式」，这从 /api/assist/chat
// 的 assist_llm 字段与这里的脱敏列表都能看到。
//
// 密文存储：复用同一把 LC_SECRET 派生的 AES-256-GCM（见 crypt 包）。
// 明文只在「创建后 / 轮换后 / reveal」这三个一次性响应里出现，
// 列表、审计 detail 一律只有脱敏串。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/assist"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

// aiCredReady 检查当前进程是否具备 LLM 凭据的加解密能力。
// 没配 LC_SECRET（或密钥过短）时凭据管理整体不可用——明确报错，不留半截状态。
func (s *Server) aiCredReady(w http.ResponseWriter) bool {
	if s.aiCred == nil || s.aiCred.Cipher() == nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "服务未启用 AI 凭据管理（缺少 LC_SECRET）")
		return false
	}
	return true
}

// aiCredViewer 凭据列表的准入门槛：超管全量，租户管理员只读（自己租户 + 平台级）。
// 返回 (acc, tenantID, isSuper, ok)；ok=false 时响应已写好（且只写一次）。
//
// ★ 注意：这里不能复用 requireRole —— requireRole 在「等级不够」时会直接写一份
// 403 响应。若先查超管再查租户管理员，两次都不达标就会把两份 403 拼进同一个
// body（非法 JSON，前端解析失败）。所以这里自己按等级判一次、只写一次 403。
func (s *Server) aiCredViewer(w http.ResponseWriter, r *http.Request) (*domain.Account, string, bool, bool) {
	acc := currentAccount(r)
	if acc == nil {
		fail(w, http.StatusUnauthorized, CodeUnauth, "请先登录")
		return nil, "", false, false
	}
	lvl := domain.RoleLevel(acc.Role)
	if lvl < domain.RoleLevel(domain.RoleTenantAdmin) {
		fail(w, http.StatusForbidden, CodeForbidden, "权限不足：需要 "+domain.RoleNameByLevel(domain.RoleLevel(domain.RoleTenantAdmin))+"及以上")
		return nil, "", false, false
	}
	isSuper := lvl >= domain.RoleLevel(domain.RoleSuperAdmin)
	// 最高等级额外要求平台级归属（与 requireRole 的平台级双条件同义）
	if isSuper && !domain.IsPlatformSuperAdmin(acc) {
		fail(w, http.StatusForbidden, CodeForbidden, "权限不足：该操作仅限平台级超管")
		return nil, "", false, false
	}
	return acc, effectiveTenant(r, acc), isSuper, true
}

// handleListAICredentials 列出 LLM 凭据（脱敏）。
func (s *Server) handleListAICredentials(w http.ResponseWriter, r *http.Request) {
	_, tid, isSuper, allowed := s.aiCredViewer(w, r)
	if !allowed || !s.aiCredReady(w) {
		return
	}
	all, err := s.repo.ListAICredentials(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	// 租户管理员只看得见「平台级 + 本租户」：别人的凭据名都可能带商业信息，不露。
	visible := make([]domain.AICredential, 0, len(all))
	for _, c := range all {
		if isSuper || c.TenantID == "" || c.TenantID == tid {
			visible = append(visible, c)
		}
	}
	ok(w, map[string]any{"ok": true, "credentials": visible, "can_write": isSuper})
}

// handleCreateAICredential 新增 LLM 凭据（密文落库）。
//
// tenant_id 传空串 = 平台级默认（所有租户共用）；传具体租户 = 该租户专用。
// 为什么允许一条租户覆盖：大客户往往要用自己账号下的模型额度，
// 这与翻译 Key「一个租户一把」是同构诉求。
func (s *Server) handleCreateAICredential(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	var req struct {
		Name     string `json:"name"`
		TenantID string `json:"tenant_id"`
		BaseURL  string `json:"base_url"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "请给凭据起个名字，便于日后识别")
		return
	}
	if !domain.ValidLLMBaseURL(req.BaseURL) {
		fail(w, http.StatusBadRequest, CodeValidation, "Base URL 形如 https://api.deepseek.com/v1")
		return
	}
	if !domain.ValidLLMModel(req.Model) {
		fail(w, http.StatusBadRequest, CodeValidation, "请填写模型名，如 deepseek-chat")
		return
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if len(apiKey) < 8 {
		fail(w, http.StatusBadRequest, CodeValidation, "API Key 长度看起来不完整，请重新复制")
		return
	}

	enc, err := s.aiCred.Cipher().Encrypt(apiKey)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "加密失败")
		return
	}
	tid := strings.TrimSpace(req.TenantID)
	// 指定了租户就必须真的存在：否则凭据挂在幽灵租户下，永远解析不到又查不出问题。
	if tid != "" {
		if _, terr := s.repo.GetTenant(r.Context(), tid); terr != nil {
			fail(w, http.StatusBadRequest, CodeValidation, "指定的租户不存在")
			return
		}
	}
	c := &domain.AICredential{
		ID: idgen.New(), TenantID: tid, Name: name,
		BaseURL: strings.TrimRight(req.BaseURL, "/"), Model: req.Model,
		KeyMasked: crypt.Mask(apiKey), Status: domain.AICredActive,
	}
	if err := s.repo.CreateAICredential(r.Context(), repo.AICredRow{
		ID: c.ID, TenantID: tid, Name: name, BaseURL: c.BaseURL, Model: c.Model,
		KeyEnc: enc, KeyMask: c.KeyMasked, Status: domain.AICredActive,
		CreatedBy: currentAccount(r).ID,
	}); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "保存凭据失败")
		return
	}
	s.invalidateAICred(tid)
	s.repo.LogAudit(r.Context(), tid, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_create", c.ID, "别名="+name+" 模型="+c.Model+" 脱敏="+c.KeyMasked)
	ok(w, map[string]any{"ok": true, "credential": c,
		"note": "Key 已加密保存；此后只能通过「查看明文」复制"})
}

// handleRotateAICredential 轮换 API Key（新密文覆盖，旧的立即失效）。
func (s *Server) handleRotateAICredential(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := decodeJSON(r, &req); err != nil || len(strings.TrimSpace(req.APIKey)) < 8 {
		fail(w, http.StatusBadRequest, CodeValidation, "新 Key 长度看起来不完整，请重新复制")
		return
	}
	key := strings.TrimSpace(req.APIKey)
	enc, err := s.aiCred.Cipher().Encrypt(key)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "加密失败")
		return
	}
	id := r.PathValue("id")
	tid := currentAccount(r).TenantID // 超管专属路径：平台超管为空串（可见全部）
	c, gerr := s.repo.GetAICredential(r.Context(), id, tid)
	if errors.Is(gerr, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	if gerr != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	if err := s.repo.RotateAICredential(r.Context(), id, tid, enc, crypt.Mask(key)); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "轮换失败")
		return
	}
	s.invalidateAICred(c.TenantID)
	s.repo.LogAudit(r.Context(), c.TenantID, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_rotate", id, "脱敏="+crypt.Mask(key))
	ok(w, map[string]any{"ok": true, "key_masked": crypt.Mask(key)})
}

// handleSetAICredentialStatus 启用 / 停用凭据。
// 停用是「即时止损」手段：把 base_url 填错导致助手连续失败时，先停再改。
func (s *Server) handleSetAICredentialStatus(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil || !domain.ValidAICredStatus(req.Status) {
		fail(w, http.StatusBadRequest, CodeValidation, "状态只能是 active / disabled")
		return
	}
	id := r.PathValue("id")
	tid := currentAccount(r).TenantID // 超管专属路径：平台超管为空串（可见全部）
	c, gerr := s.repo.GetAICredential(r.Context(), id, tid)
	if errors.Is(gerr, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	if gerr != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	if err := s.repo.SetAICredentialStatus(r.Context(), id, tid, req.Status); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "更新状态失败")
		return
	}
	s.invalidateAICred(c.TenantID)
	s.repo.LogAudit(r.Context(), c.TenantID, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_status", id, "状态="+req.Status)
	ok(w, map[string]any{"ok": true, "status": req.Status})
}

// handleRevealAICredential 查看 API Key 明文（一次性返回 + 落审计）。
func (s *Server) handleRevealAICredential(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	id := r.PathValue("id")
	tid := currentAccount(r).TenantID // 超管专属路径：平台超管为空串（可见全部）
	c, gerr := s.repo.GetAICredential(r.Context(), id, tid)
	if errors.Is(gerr, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	if gerr != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	blob, err := s.repo.AICredCiphertext(r.Context(), id, tid)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	plain, err := s.aiCred.Cipher().Decrypt(blob)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "解密失败，可能已更换服务密钥")
		return
	}
	s.repo.LogAudit(r.Context(), c.TenantID, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_reveal", id, "")
	ok(w, map[string]any{"ok": true, "api_key": plain})
}

// handleDeleteAICredential 删除凭据。
func (s *Server) handleDeleteAICredential(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	id := r.PathValue("id")
	tid := currentAccount(r).TenantID // 超管专属路径：平台超管为空串（可见全部）
	c, gerr := s.repo.GetAICredential(r.Context(), id, tid)
	if errors.Is(gerr, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	if gerr != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	if err := s.repo.DeleteAICredential(r.Context(), id, tid); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
			return
		}
		fail(w, http.StatusInternalServerError, CodeInternal, "删除失败")
		return
	}
	s.invalidateAICred(c.TenantID)
	s.repo.LogAudit(r.Context(), c.TenantID, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_delete", id, "别名="+c.Name)
	ok(w, map[string]any{"ok": true})
}

// handleTestAICredential 试译一次，验证凭据可用。
//
// ★ 这是「保存前就能发现问题」的唯一手段：base_url 多一个空格、模型名拼错、
// key 复制截断，只有真发一次请求才会暴露。返回里带模型名，
// 超管可据此确认「到底是哪个模型在答」。
func (s *Server) handleTestAICredential(w http.ResponseWriter, r *http.Request) {
	if !s.superOnly(w, r) || !s.aiCredReady(w) {
		return
	}
	id := r.PathValue("id")
	tid := currentAccount(r).TenantID // 超管专属路径：平台超管为空串（可见全部）
	c, gerr := s.repo.GetAICredential(r.Context(), id, tid)
	if errors.Is(gerr, repo.ErrNotFound) {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	if gerr != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取凭据失败")
		return
	}
	blob, err := s.repo.AICredCiphertext(r.Context(), id, tid)
	if err != nil {
		fail(w, http.StatusNotFound, CodeNotFound, "凭据不存在")
		return
	}
	plain, err := s.aiCred.Cipher().Decrypt(blob)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "解密失败，可能已更换服务密钥")
		return
	}
	cfg := assist.LLMConfig{BaseURL: c.BaseURL, APIKey: plain, Model: c.Model}
	if !cfg.Enabled() {
		fail(w, http.StatusBadRequest, CodeValidation, "凭据信息不完整，无法试译")
		return
	}
	// 用引擎自己的客户端发一次最小请求（问一句固定的话），
	// 成功即说明「地址 / 模型 / key」三件事都对。
	eng := assist.New()
	rep := eng.Respond(r.Context(), assist.AskInput{
		AccountID: "self-test", Question: "你好，请做一次自我介绍",
		UI: "zh", LLM: cfg,
	})
	s.repo.LogAudit(r.Context(), c.TenantID, currentAccount(r).ID, currentAccount(r).Role,
		"ai_cred_test", id, "来源="+rep.Source)
	ok(w, map[string]any{
		"ok":      true,
		"source":  rep.Source,
		"model":   c.Model,
		"content": rep.Content,
		"note":    "source=llm 表示凭据可用；fallback 表示模型未响应（请检查地址与 Key）",
	})
}

// superOnly 平台超管专属写操作门槛（双条件校验见 domain.IsPlatformSuperAdmin）。
// ok=false 时响应已由 requireRole 写好。
func (s *Server) superOnly(w http.ResponseWriter, r *http.Request) bool {
	_, allowed := s.requireRole(w, r, domain.RoleLevel(domain.RoleSuperAdmin))
	return allowed
}

// invalidateAICred 让凭据缓存失效。
// 不能只失效 tid：从「有租户凭据」改成「只剩平台凭据」时，
// 也要把平台级（tid=""）的解析结果刷新，否则切换会在 TTL 内不生效。
func (s *Server) invalidateAICred(tid string) {
	if s.aiCred == nil {
		return
	}
	s.aiCred.Invalidate(tid)
	s.aiCred.Invalidate("")
}
