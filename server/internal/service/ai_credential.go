// Package service 的 AI 经营助手凭据解析：AICredVault（按租户选 LLM 凭据）。
//
// 与 KeyVault（翻译 Key）是同构设计，刻意保持一致的形态：
//
//	KeyVault     translation_api_key  →  KeyCredential{Key, Mode, DailyLimit}
//	AICredVault  ai_credential        →  LLMCredential{BaseURL, APIKey, Model}
//
// 三条共同约束：
//  1. 明文只在内存里：解密后立即组装成配置对象，不落日志、不进错误信息；
//  2. 缓存要短：后台改了凭据，最多 TTL 之后生效（默认 30s），
//     改完可立即 Invalidate 即时生效（写接口里已调）；
//  3. 解析失败不阻塞业务：拿不到凭据时助手降级到确定性兜底，
//     翻译、菜单、券等其它功能照常。
package service

import (
	"context"
	"sync"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

// AICredStore 是 AICredVault 依赖的存储能力（*repo.Repo 天然满足）。
// AICredCiphertext 的 tenantID 是请求方租户作用域：租户账号经此读「本租户或
// 平台级」凭据的密文（平台默认凭据必须放行，否则助手对租户永远降级）。
type AICredStore interface {
	ActiveAICredentialForTenant(ctx context.Context, tenantID string) (*domain.AICredential, error)
	AICredCiphertext(ctx context.Context, id, tenantID string) ([]byte, error)
}

// AICredVault 解析「这个租户现在该用哪份 LLM 凭据」。
//
// 解析规则（与 repo.ActiveAICredentialForTenant 一致，这里只负责解密与缓存）：
// 优先本租户自己的 active 凭据，否则用平台级（tenant_id = ”）的 active 凭据。
// 都没有 → AssistEnabled() 为 false，助手走确定性兜底。
type AICredVault struct {
	cipher *crypt.Cipher
	store  AICredStore
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]aiVaultEntry
}

// aiVaultEntry 是 LLM 凭据箱里的一条按租户缓存项：凭据 + 过期时间，TTL 内复用、减少查库。
// cred 为 nil 表示「已确认该租户当前无可用凭据」（负缓存），避免反复查库。
type aiVaultEntry struct {
	cred    *domain.LLMCredential // nil = 已确认无可用凭据（负缓存）
	expires time.Time
}

// NewAICredVault 构造凭据箱。cipher 为 nil 时视为「无法解密任何凭据」，
// 此时 Credential 恒返回无凭据——助手自动降级，绝不因为缺 key 而崩。
func NewAICredVault(cipher *crypt.Cipher, store AICredStore) *AICredVault {
	return &AICredVault{cipher: cipher, store: store, ttl: 30 * time.Second, cache: map[string]aiVaultEntry{}}
}

// Cipher 暴露内部加密器（仅供后台加解密 LLM 凭据使用）。
func (v *AICredVault) Cipher() *crypt.Cipher {
	if v == nil {
		return nil
	}
	return v.cipher
}

// Credential 取该租户当前可用的 LLM 凭据。
// 返回 (nil, nil) 表示没有配置——这是正常状态，不是错误。
func (v *AICredVault) Credential(ctx context.Context, tenantID string) (*domain.LLMCredential, error) {
	if v == nil || v.cipher == nil || v.store == nil {
		return nil, nil
	}
	if e, ok := v.lookup(tenantID); ok {
		return e.cred, nil
	}

	c, err := v.store.ActiveAICredentialForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var cred *domain.LLMCredential
	if c != nil {
		// 传请求方租户：repo 的作用域口径放行「本租户 + 平台级」两档，
		// 因此租户解析到平台默认凭据时密文照样读得出来。
		blob, berr := v.store.AICredCiphertext(ctx, c.ID, tenantID)
		if berr != nil {
			return nil, berr
		}
		plain, derr := v.cipher.Decrypt(blob)
		if derr != nil {
			// 解密失败（换了 LC_SECRET / 密文被改）：不缓存，让下次再试，
			// 错误抛出去——后台要能看到「这份凭据读不出来」。
			return nil, derr
		}
		cred = &domain.LLMCredential{
			BaseURL: c.BaseURL, APIKey: plain, Model: c.Model, Name: c.Name,
		}
	}
	v.put(tenantID, cred)
	return cred, nil
}

// Invalidate 让某租户的凭据缓存立即失效（后台增删改凭据后调用）。
func (v *AICredVault) Invalidate(tenantID string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	delete(v.cache, tenantID)
	v.mu.Unlock()
}

// AssistEnabled 判断当前是否具备 LLM 能力（供健康检查与前端提示）。
func (v *AICredVault) AssistEnabled(ctx context.Context, tenantID string) bool {
	cred, err := v.Credential(ctx, tenantID)
	return err == nil && cred != nil && cred.APIKey != ""
}

func (v *AICredVault) lookup(tenantID string) (aiVaultEntry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.cache[tenantID]
	if !ok || time.Now().After(e.expires) {
		return aiVaultEntry{}, false
	}
	return e, true
}

// put 负缓存用较短 TTL：没有凭据的租户可能下一秒就被录入了凭据，
// 缓存 30s 会让超管「明明刚保存却还没生效」。
func (v *AICredVault) put(tenantID string, cred *domain.LLMCredential) {
	ttl := v.ttl
	if cred == nil {
		ttl = 5 * time.Second
	}
	v.mu.Lock()
	v.cache[tenantID] = aiVaultEntry{cred: cred, expires: time.Now().Add(ttl)}
	v.mu.Unlock()
}

// 编译期断言：*repo.Repo 满足凭据存储接口（repo 不依赖 service，这里是单向依赖）。
var _ AICredStore = (*repo.Repo)(nil)
