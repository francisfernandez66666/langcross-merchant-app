package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

const (
	otpTTL     = 5 * time.Minute
	tokenTTL   = 30 * 24 * time.Hour
	devOTPCode = "000000"
)

// handleSendOTP 发送手机验证码。开发模式回显 dev_code，便于自测。
func (s *Server) handleSendOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if len(phone) < 6 {
		fail(w, http.StatusBadRequest, CodeValidation, "手机号不正确")
		return
	}

	code := devOTPCode
	if !s.cfg.DevMode {
		code = idgen.Code(6)
	}
	if err := s.repo.UpsertOTP(r.Context(), phone, code, time.Now().Add(otpTTL).Unix()); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "验证码发送失败")
		return
	}

	resp := map[string]any{"ok": true, "ttl_seconds": int(otpTTL.Seconds())}
	if s.cfg.DevMode {
		resp["dev_code"] = code
	}
	ok(w, resp)
}

// handleLogin 手机号 + 验证码登录；账号不存在则自动注册。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)

	stored, exp, err := s.repo.GetOTP(r.Context(), phone)
	if err != nil || stored != strings.TrimSpace(req.Code) || time.Now().Unix() > exp {
		fail(w, http.StatusUnauthorized, CodeValidation, "验证码不正确或已过期")
		return
	}
	_ = s.repo.DeleteOTP(r.Context(), phone)

	acc, err := s.repo.GetAccountByPhone(r.Context(), phone)
	if errors.Is(err, repo.ErrNotFound) {
		acc, err = s.repo.CreateAccount(r.Context(), phone)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "登录失败")
		return
	}

	ok(w, map[string]any{
		"ok":      true,
		"token":   signToken(s.secret, acc.ID, tokenTTL),
		"account": acc,
	})
}

// handleMe 返回当前账号与其门店列表。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc, err := s.repo.GetAccountByID(r.Context(), accountID(r))
	if err != nil {
		fail(w, http.StatusUnauthorized, CodeUnauth, "账号不存在")
		return
	}
	stores, err := s.repo.ListStoresByAccount(r.Context(), acc.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "读取门店失败")
		return
	}
	ok(w, map[string]any{"ok": true, "account": acc, "stores": stores})
}

// handleTrack 收埋点（失败不致命，不阻塞前端）。
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StoreID string         `json:"store_id"`
		Name    string         `json:"name"`
		Payload map[string]any `json:"payload"`
	}
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(w, http.StatusBadRequest, CodeValidation, "事件格式错误")
		return
	}
	_ = s.repo.TrackEvent(r.Context(), req.StoreID, req.Name, req.Payload)
	ok(w, map[string]any{"ok": true})
}
