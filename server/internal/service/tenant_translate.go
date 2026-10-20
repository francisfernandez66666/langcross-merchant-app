// Package service 的租户翻译引擎编排：KeyVault（凭据解析）+ TenantPrimary（按租户选引擎）。
//
// 链路位置（由外到内）：
//
//	CachedTranslator（TM）
//	  └─ FallbackTranslator{ primary: TenantPrimary, fallback: DictTranslator }
//	                       └─ TenantPrimary：有 Key 走能言 → 不成退既有链/MyMemory → 再不成交给词典
//
// 三条设计约束：
//  1. Key 明文只在内存里：KeyVault 解密后立即用，不落日志、不进错误信息；
//  2. 缓存要短：后台改了 Key / 停了 Key，最多 TTL 之后生效（默认 30s，改了立刻可接受的等待）；
//  3. 单租户故障不拖垮别人：解密失败、能言报错，只影响当前请求的降级路径。
package service

import (
	"context"
	"sync"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/repo"
)

// TranslationKeyStore 是 KeyVault 依赖的存储能力（*repo.Repo 天然满足）。
// 抽成接口是为了测试时可插入假实现，避免为单测拉起数据库。
type TranslationKeyStore interface {
	ActiveTranslationKey(ctx context.Context, tenantID string) (*domain.TranslationKey, error)
	KeyCiphertext(ctx context.Context, tenantID, id string) ([]byte, error)
}

// KeyVault 解析「某个租户现在该用哪把 Key」。
//
// 带 TTL 内存缓存：翻译是高频路径，每次翻译都查库 + 解密会让数据库白忙；
// 但缓存又不能太长——商家在后台停用一把 Key 后，理论上应该尽快生效，
// 30s 是「少查库」与「快生效」之间的折中（真正的即时止损靠停用后重新发布/重启）。
type KeyVault struct {
	cipher *crypt.Cipher
	store  TranslationKeyStore
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]vaultEntry
}

type vaultEntry struct {
	cred    *domain.KeyCredential // nil 表示「确认无可用 Key」（负缓存）
	keyID   string                // 非空 = 缓存的是真实凭据（用于 Touch 使用时间）
	expires time.Time
}

// NewKeyVault 构造凭据箱。cipher 为 nil 时视为「无法解密任何 Key」，
// 此时 Credential 恒返回无凭据——翻译自动降级到其它引擎（宁可不用能言，也不能崩）。
func NewKeyVault(cipher *crypt.Cipher, store TranslationKeyStore) *KeyVault {
	return &KeyVault{cipher: cipher, store: store, ttl: 30 * time.Second, cache: map[string]vaultEntry{}}
}

// Cipher 暴露内部加密器（仅供后台加解密翻译 Key 使用）。
// 返回 nil 表示当前进程没有加密能力（未配置 LC_SECRET）——此时 Key 管理接口应明确报错。
func (v *KeyVault) Cipher() *crypt.Cipher {
	if v == nil {
		return nil
	}
	return v.cipher
}

// Credential 取该租户当前可用的翻译凭据。
// 返回 (nil, nil) 表示该租户没有启用中的 Key——这是正常状态，不是错误。
func (v *KeyVault) Credential(ctx context.Context, tenantID string) (*domain.KeyCredential, error) {
	if v == nil || v.cipher == nil || v.store == nil || tenantID == "" {
		return nil, nil
	}
	if e, ok := v.lookup(tenantID); ok {
		return e.cred, nil
	}

	k, err := v.store.ActiveTranslationKey(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var cred *domain.KeyCredential
	var keyID string
	if k != nil {
		blob, berr := v.store.KeyCiphertext(ctx, tenantID, k.ID)
		if berr != nil {
			return nil, berr
		}
		plain, derr := v.cipher.Decrypt(blob)
		if derr != nil {
			// 解密失败（换了 LC_SECRET / 密文被改）：不缓存，让下次请求再试，
			// 同时把错误抛出去——后台要能看到「这把 Key 读不出来」。
			return nil, derr
		}
		cred = &domain.KeyCredential{Key: plain, Mode: k.Mode, DailyLimit: k.DailyLimit, Name: k.Name}
		keyID = k.ID
	}
	v.put(tenantID, cred, keyID)
	return cred, nil
}

// KeyID 返回当前生效的 Key ID（用于回写 last_used_at；无 Key 时为空串）。
func (v *KeyVault) KeyID(ctx context.Context, tenantID string) string {
	if v == nil || tenantID == "" {
		return ""
	}
	if e, ok := v.lookup(tenantID); ok && e.cred != nil {
		return e.keyID
	}
	_, _ = v.Credential(ctx, tenantID)
	if e, ok := v.lookup(tenantID); ok && e.cred != nil {
		return e.keyID
	}
	return ""
}

// Invalidate 让某租户的凭据缓存立即失效（后台增删改 Key 后调用）。
func (v *KeyVault) Invalidate(tenantID string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	delete(v.cache, tenantID)
	v.mu.Unlock()
}

// HasAnyKey 供健康检查：是否至少有一个租户配了 Key（粗判，用于提示当前引擎形态）。
func (v *KeyVault) HasAnyKey(ctx context.Context, tenantID string) bool {
	cred, err := v.Credential(ctx, tenantID)
	return err == nil && cred != nil && cred.Key != ""
}

func (v *KeyVault) lookup(tenantID string) (vaultEntry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.cache[tenantID]
	if !ok || time.Now().After(e.expires) {
		return vaultEntry{}, false
	}
	return e, true
}

// store_ 负缓存用较短 TTL：没有 Key 的租户可能下一秒就被录入了 Key，
// 缓存 30s 会让商家「明明刚保存却还没生效」。
func (v *KeyVault) put(tenantID string, cred *domain.KeyCredential, keyID string) {
	ttl := v.ttl
	if cred == nil {
		ttl = 5 * time.Second
	}
	v.mu.Lock()
	v.cache[tenantID] = vaultEntry{cred: cred, keyID: keyID, expires: time.Now().Add(ttl)}
	v.mu.Unlock()
}

// TenantPrimary 按请求的租户选择主引擎：有能言 Key 走能言，否则走次级引擎。
//
// 为什么不用 FallbackTranslator 嵌套：FallbackTranslator 只支持「一个主 + 一个兜底」，
// 而这里需要「能言 → 既有链/MyMemory」两段降级 + 按请求动态决定主引擎，
// 语义不同，硬套会让 Name()/Ready() 的报失真。故单独实现，复用同一套
// normalizeLen / hasEmpty 契约，行为与 FallbackTranslator 严格一致。
type TenantPrimary struct {
	Vault *KeyVault  // 凭据来源；nil 时恒走次级引擎
	Inner Translator // 次级引擎：既有翻译链或 MyMemory
	Base  string     // 能言平台地址（默认 LexicornDefaultBaseURL）

	mu    sync.Mutex
	usage map[string]*dailyUsage // 进程内日计数（key = tenantID|UTC 日期）
}

type dailyUsage struct {
	day   string
	count int
}

// NewTenantPrimary 构造按租户路由的主引擎。
func NewTenantPrimary(vault *KeyVault, inner Translator) *TenantPrimary {
	return &TenantPrimary{Vault: vault, Inner: inner, Base: LexicornDefaultBaseURL, usage: map[string]*dailyUsage{}}
}

// Ready 恒为 true：至少还有次级引擎（MyMemory / 既有链 / 词典）可用。
// 真正的「能言是否可用」由 HasAnyKey 单独回答，不混在这里。
func (t *TenantPrimary) Ready() bool { return true }

// TranslateEngine 报次级引擎（既有链 / MyMemory）。
//
// ★ 诚实边界：能言是按「请求里的租户」解析 Key 后才用的（见 Translate），
// 健康检查没有租户上下文，无法判断某租户有没有 Key。因此这里报的是
// **保底路径**——即「一个没配 Key 的租户此刻能用的引擎」。
// 有 Key 的租户走能言，能言失败也会回落到这条路径，所以它的可达性
// 就是翻译能力的地板：地板塌了才是真事故。
func (t *TenantPrimary) TranslateEngine() string { return engineOf(t.Inner) }

// TranslateReachable 问次级引擎的可达性（理由见 TranslateEngine 的注释）。
func (t *TenantPrimary) TranslateReachable(ctx context.Context) bool {
	return reachableOf(ctx, t.Inner)
}

// Name 报告次级引擎名：本实现在无请求上下文时无法判断租户有没有 Key，
// 因此这里给保守答案；前端看到的真实引擎以 /api/health 的字段为准。
func (t *TenantPrimary) Name() string {
	if t.Inner == nil {
		return "lexicorn"
	}
	return t.Inner.Name()
}

// Translate 先按租户解析 Key 走能言，不成了再让次级引擎补洞。
func (t *TenantPrimary) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	if t.Inner == nil {
		return make([]string, len(texts)), nil
	}
	tenantID := TenantFromContext(ctx)
	if t.Vault == nil || tenantID == "" {
		return t.Inner.Translate(ctx, texts, from, to)
	}

	cred, err := t.Vault.Credential(ctx, tenantID)
	if err != nil || cred == nil || cred.Key == "" {
		// 无凭据 / 凭据读不出：直接走次级引擎（错误只记录，不阻断翻译）
		return t.Inner.Translate(ctx, texts, from, to)
	}
	if cred.DailyLimit > 0 && t.exceededDaily(tenantID, cred.DailyLimit) {
		// 当日到顶：不走能言，避免把商家的额度耗在无效调用上
		return t.Inner.Translate(ctx, texts, from, to)
	}
	t.bumpDaily(tenantID)

	eng := NewLexicornTranslator(t.Base, cred.Key, cred.Mode)
	out, err := eng.Translate(ctx, texts, from, to)
	out = normalizeLen(out, len(texts))
	if err == nil && !hasEmpty(out) {
		return out, nil
	}

	// 能言失败或有空洞：次级引擎补位（与 FallbackTranslator 同口径）
	fb, ferr := t.Inner.Translate(ctx, texts, from, to)
	fb = normalizeLen(fb, len(texts))
	if ferr != nil {
		return out, err
	}
	filled := false
	for i := range out {
		if out[i] == "" && fb[i] != "" {
			out[i] = fb[i]
			filled = true
		}
	}
	if filled {
		return out, nil
	}
	return out, err
}

// exceededDaily 判断该租户当日调用是否已达上限。
//
// ★ 口径说明：这是进程内计数，重启即清零，多实例部署下各算各的。
// 因此它是「尽力而为的护栏」而不是「计费口径」——真正的计费由能言平台按 Key 结算。
// 把它做进服务端只是为了在明显超额时及时刹车，避免商家无感地耗尽额度。
func (t *TenantPrimary) exceededDaily(tenantID string, limit int) bool {
	today := time.Now().UTC().Format("2006-01-02")
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.usage[tenantID]
	if !ok || u.day != today {
		t.usage[tenantID] = &dailyUsage{day: today}
		return false
	}
	return u.count >= limit
}

func (t *TenantPrimary) bumpDaily(tenantID string) {
	today := time.Now().UTC().Format("2006-01-02")
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.usage[tenantID]
	if !ok || u.day != today {
		u = &dailyUsage{day: today}
		t.usage[tenantID] = u
	}
	u.count++
}

// 编译期断言：*repo.Repo 满足凭据存储接口（repo 不依赖 service，这里是单向依赖）。
var _ TranslationKeyStore = (*repo.Repo)(nil)
