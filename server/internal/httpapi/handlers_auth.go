// 本文件：认证与账号相关接口 —— 发送验证码、登录（兼自动注册）、当前账号、埋点上报。
//
// 认证方式是「手机号 + 短信验证码」：商家只需一个手机号即可进入产品，
// 不设密码、不设邮箱，把注册摩擦降到最低。开发期验证码固定为 000000。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

// 认证相关常量。
const (
	otpTTL     = 5 * time.Minute     // 验证码有效期：5 分钟（足够输入，又不易被暴力枚举）
	tokenTTL   = 30 * 24 * time.Hour // 登录态有效期：30 天（商家是长期使用者，不宜频繁踢登录）
	devOTPCode = "000000"            // 开发期固定验证码，仅 DevMode 下使用
)

// handleSendOTP 发送手机验证码。开发模式回显 dev_code，便于自测。
//
// 开发期（cfg.DevMode）验证码固定为 000000 且随响应回显，目的是让联调不依赖
// 真实短信通道；生产环境必须走随机码，且绝不把验证码放进响应体。
// 验证码落 otp 表并带过期时间，而不是放内存——多实例部署时行为依然一致。
func (s *Server) handleSendOTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, CodeValidation, "请求格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	// 只做最宽松的长度校验（>=6）：具体号码规则交给短信通道，
	// 本地写死过严的正则反而会挡住海外号段（本产品的目标客户本就含外籍商家）。
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
//
// 「登录即注册」是刻意的产品决策：来华商家第一次进来只想快点把店建起来，
// 不该被「先注册再登录」的两步流程劝退（转化率优先于流程纯洁）。
// 验证码校验通过后立即删除，保证一次性使用、防重放。
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

	// 三个条件任一不满足都算失败，且统一返回同一句话：
	// 不区分「验证码错」与「已过期」，避免给爆破者提供额外信息。
	stored, exp, err := s.repo.GetOTP(r.Context(), phone)
	if err != nil || stored != strings.TrimSpace(req.Code) || time.Now().Unix() > exp {
		fail(w, http.StatusUnauthorized, CodeValidation, "验证码不正确或已过期")
		return
	}
	_ = s.repo.DeleteOTP(r.Context(), phone)

	// 先查后建：老账号直接登录，新手机号自动注册，对前端是同一条路径。
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
//
// 前端启动时用它做「恢复登录态 + 拉首页数据」的一次性调用。
// 若 token 有效但账号已被删，返回 UNAUTH 让前端退回登录页（而不是报 500）。
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
//
// 埋点写库失败一律忽略：埋点丢了可以接受，但绝不能因为埋点异常
// 让用户真正的操作（提交评价、核销券）跟着失败。
// name 为空视为无意义事件直接拒绝；store_id 允许为空（注册前的事件还没有门店）。
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
