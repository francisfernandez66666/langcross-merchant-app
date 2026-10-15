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
type tokenClaims struct {
	AccountID string `json:"sub"`
	Exp       int64  `json:"exp"`
}

func signToken(secret []byte, accountID string, ttl time.Duration) string {
	raw, _ := json.Marshal(tokenClaims{AccountID: accountID, Exp: time.Now().Add(ttl).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func verifyToken(secret []byte, tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return "", errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}
