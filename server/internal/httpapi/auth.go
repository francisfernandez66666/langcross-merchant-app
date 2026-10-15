// 本文件：无状态登录 token 的签发与校验。
//
// 用 HMAC-SHA256 自签自验，不引入 JWT 库 —— 载荷只有账号 ID 与过期时间，
// 自己实现反而更小、更可控（也避免第三方库的算法混淆类漏洞）。
// 无状态的好处是不需要服务端 session 表，多实例部署天然可用；
// 代价是签发后无法单方面吊销，只能等过期（30 天），这是本期接受的取舍。
package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// tokenClaims 是无状态 token 的载荷：仅账号 ID + 过期时间。
//
// 刻意不放手机号等个人信息：token 会落到客户端本地，信息越少越安全。
// sub / exp 沿用 JWT 的字段名，方便日后需要时平滑迁移到标准 JWT。
type tokenClaims struct {
	AccountID string `json:"sub"`
	Exp       int64  `json:"exp"`
}

// signToken 生成 token：base64url(payload) + "." + base64url(HMAC-SHA256(payload))。
//
// 签名只覆盖 payload（不含签名段本身），因此内容改不了、签名也伪造不了。
// 用 base64.RawURLEncoding 是为了让 token 能安全地放进 URL 与 header。
func signToken(secret []byte, accountID string, ttl time.Duration) string {
	raw, _ := json.Marshal(tokenClaims{AccountID: accountID, Exp: time.Now().Add(ttl).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// verifyToken 校验 token 并返回账号 ID。
//
// 校验顺序很重要：先比签名、再解载荷、最后看过期时间。
// 未验签就解析外部输入是不安全的（解析器可能被畸形输入攻击），
// 所以 base64 解码与 JSON 反序列化都放在 hmac.Equal 之后。
// 任一环节失败都返回 error，由 withAuth 统一转成 401 UNAUTH。
func verifyToken(secret []byte, tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return "", errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	// 用 hmac.Equal 做定长比较，避免按字节短路比较带来的时序侧信道。
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return "", errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", err
	}
	if time.Now().Unix() > c.Exp {
		return "", errors.New("token expired")
	}
	return c.AccountID, nil
}

// bearerToken 从 Authorization: Bearer <token> 头中取 token。
//
// 严格区分大小写（必须是大写的 Bearer 前缀），并对取到的值做 TrimSpace；
// 任何不规范的写法都返回空串，交由 verifyToken 判为非法 token。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}
