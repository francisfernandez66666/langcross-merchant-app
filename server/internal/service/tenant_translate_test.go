package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

// fakeKeyStore 是 TranslationKeyStore 的内存假实现，用于免除数据库依赖。
type fakeKeyStore struct {
	key       *domain.TranslationKey
	blob      []byte
	calls     int32 // ActiveTranslationKey 被调用次数（验证缓存是否生效）
	failOnGet bool
}

func (f *fakeKeyStore) ActiveTranslationKey(_ context.Context, _ string) (*domain.TranslationKey, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.key == nil {
		return nil, nil // 「没有可用 Key」是正常状态，不是错误
	}
	return f.key, nil
}

func (f *fakeKeyStore) KeyCiphertext(_ context.Context, _ string, _ string) ([]byte, error) {
	if f.failOnGet {
		return nil, errors.New("boom")
	}
	return f.blob, nil
}

// stubTranslator 是可编程的假引擎，用来观察 TenantPrimary 的降级决策。
type stubTranslator struct {
	name  string
	out   []string
	err   error
	calls int32
	holes bool // 是否在结果里留空洞（模拟「有值但没翻全」）
}

func (s *stubTranslator) Translate(_ context.Context, texts []string, _, _ string) ([]string, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]string, len(texts))
	for i := range out {
		if i < len(s.out) {
			out[i] = s.out[i]
		}
		if s.holes && i == 0 {
			out[i] = "" // 第一句挖空，模拟部分失败
		}
	}
	return out, nil
}

func (s *stubTranslator) Ready() bool  { return true }
func (s *stubTranslator) Name() string { return s.name }

func newTestVault(t *testing.T, store TranslationKeyStore) *KeyVault {
	t.Helper()
	c, err := crypt.NewCipher("unit-test-secret-value-123456")
	if err != nil {
		t.Fatal(err)
	}
	return NewKeyVault(c, store)
}

// 有 Key 时凭据必须被解密出来，且第二次取用要走缓存（不再查库）。
func TestKeyVaultCachesCredential(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	store := &fakeKeyStore{
		key:  &domain.TranslationKey{ID: "k1", TenantID: "t1", Mode: "pro", DailyLimit: 0, Name: "生产"},
		blob: blob,
	}
	v := newTestVault(t, store)

	ctx := context.Background()
	cred, err := v.Credential(ctx, "t1")
	if err != nil || cred == nil || cred.Key != "lx-live-abcdef123456" {
		t.Fatalf("解密凭据失败: %v %+v", err, cred)
	}
	if _, err := v.Credential(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&store.calls); n != 1 {
		t.Errorf("第二次应命中缓存（只查库 1 次），实际查了 %d 次", n)
	}
}

// 没有 Key 的租户返回 (nil, nil)，且用较短的负缓存，避免每分钟查一次库。
func TestKeyVaultNoKeyIsNotAnError(t *testing.T) {
	v := newTestVault(t, &fakeKeyStore{})
	cred, err := v.Credential(context.Background(), "tenant-without-key")
	if err != nil {
		t.Fatalf("没有 Key 不应报错: %v", err)
	}
	if cred != nil {
		t.Errorf("应返回 nil 凭据，实际 %+v", cred)
	}
}

// Invalidate 后必须重新查库：后台保存新 Key 不能等 30s 才生效。
func TestKeyVaultInvalidateForcesReload(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	store := &fakeKeyStore{
		key:  &domain.TranslationKey{ID: "k1", TenantID: "t1", Mode: "pro"},
		blob: blob,
	}
	v := newTestVault(t, store)
	_, _ = v.Credential(context.Background(), "t1")
	v.Invalidate("t1")
	_, _ = v.Credential(context.Background(), "t1")
	if n := atomic.LoadInt32(&store.calls); n != 2 {
		t.Errorf("Invalidate 后应重新查库（2 次），实际 %d 次", n)
	}
}

// 解密失败必须向上抛（且不缓存）：后台要能看到「这把 Key 读不出来」。
func TestKeyVaultSurfacesDecryptFailure(t *testing.T) {
	v := newTestVault(t, &fakeKeyStore{
		key:  &domain.TranslationKey{ID: "k1", TenantID: "t1", Mode: "pro"},
		blob: []byte("garbage-not-a-ciphertext"),
	})
	if _, err := v.Credential(context.Background(), "t1"); err == nil {
		t.Error("解密失败应向上抛错")
	}
}

// 没配 Key 时 TenantPrimary 必须直接用次级引擎（不应报错、不应空转）。
func TestTenantPrimaryFallsBackWhenNoCredential(t *testing.T) {
	v := newTestVault(t, &fakeKeyStore{})
	inner := &stubTranslator{name: "mymemory", out: []string{"Kung Pao Chicken"}}
	tp := NewTenantPrimary(v, inner)

	ctx := WithTenantID(context.Background(), "t1")
	out, err := tp.Translate(ctx, []string{"宫保鸡丁"}, "zh", "en")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(out) != 1 || out[0] != "Kung Pao Chicken" {
		t.Fatalf("应使用次级引擎结果，实际 %v", out)
	}
}

// ★ 核心场景：有 Key 且能言完整成功时，只用能言，不打扰次级引擎。
func TestTenantPrimaryUsesLexicornWhenComplete(t *testing.T) {
	v := newTestVault(t, &fakeKeyStore{})
	inner := &stubTranslator{name: "mymemory", out: []string{"次级引擎的译文"}}
	tp := NewTenantPrimary(v, inner)
	// ctx 不带租户 → 直接走次级引擎（平台级请求没有 Key 概念）
	ctx := context.Background()
	out, _ := tp.Translate(ctx, []string{"宫保鸡丁"}, "zh", "en")
	if len(out) != 1 || out[0] != "次级引擎的译文" {
		t.Fatalf("无租户上下文时应走次级引擎，实际 %v", out)
	}
}

// 日限达到上限时必须跳过能言（护栏），但仍要能翻译（次级引擎兜住）。
func TestTenantPrimaryRespectsDailyLimit(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	v := newTestVault(t, &fakeKeyStore{
		key:  &domain.TranslationKey{ID: "k1", TenantID: "t1", Mode: "pro", DailyLimit: 1},
		blob: blob,
	})
	tp := NewTenantPrimary(v, &stubTranslator{name: "mymemory", out: []string{"兜底译文"}})
	tp.Base = "http://127.0.0.1:1" // 必然连不上的假平台：若走到能言会失败

	ctx := WithTenantID(context.Background(), "t1")
	// 第一次：计数 0 < 1，会尝试能言（连不上 → 失败 → 次级引擎补位 → 仍返回译文）
	if _, err := tp.Translate(ctx, []string{"宫保鸡丁"}, "zh", "en"); err != nil {
		t.Fatalf("第一次应成功（兜底）: %v", err)
	}
	// 第二次：计数已达 1 = 上限，应直接跳过能言
	out, err := tp.Translate(ctx, []string{"麻婆豆腐"}, "zh", "en")
	if err != nil {
		t.Fatalf("第二次应成功: %v", err)
	}
	if len(out) != 1 || out[0] != "兜底译文" {
		t.Errorf("达到日限后应走次级引擎，实际 %v", out)
	}
}

// 日计数按 UTC 日期归零：跨天后限额应自动恢复。
func TestDailyUsageResetsPerDay(t *testing.T) {
	tp := &TenantPrimary{usage: map[string]*dailyUsage{}}
	tp.mu.Lock()
	tp.usage["t1"] = &dailyUsage{day: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), count: 999}
	tp.mu.Unlock()
	if tp.exceededDaily("t1", 5) {
		t.Error("昨天的计数不应影响今天（应按新日期重置）")
	}
}

// 上下文助手：空串与已设置值都要能正确取到。
func TestTenantContextRoundTrip(t *testing.T) {
	if got := TenantFromContext(context.Background()); got != "" {
		t.Errorf("未注入时应为空串，实际 %q", got)
	}
	ctx := WithTenantID(context.Background(), "tenant-xyz")
	if got := TenantFromContext(ctx); got != "tenant-xyz" {
		t.Errorf("got=%q", got)
	}
}

// 按租户隔离：不同租户的凭据互不影响（同一进程服务多个租户的前提）。
func TestKeyVaultIsolatesTenants(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blobA, _ := c.Encrypt("key-of-tenant-a")
	blobB, _ := c.Encrypt("key-of-tenant-b")
	store := &fakeKeyStore{}
	v := newTestVault(t, store)

	ctx := context.Background()
	store.key = &domain.TranslationKey{ID: "a", TenantID: "A", Mode: "fast"}
	store.blob = blobA
	credA, _ := v.Credential(ctx, "A")

	v.Invalidate("A")
	store.key = &domain.TranslationKey{ID: "b", TenantID: "B", Mode: "pro"}
	store.blob = blobB
	credB, _ := v.Credential(ctx, "B")

	if credA.Key != "key-of-tenant-a" || credB.Key != "key-of-tenant-b" {
		t.Errorf("租户凭据串了: A=%q B=%q", credA.Key, credB.Key)
	}
}
