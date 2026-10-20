package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/crypt"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

/* ── AICredVault 的假存储 ─────────────────────────────────── */

// fakeAICredStore 是可编程的 AICredStore：按租户返回指定凭据，
// 并记录 AICredCiphertext 收到的租户参数（断言空串放行条款的传参）。
type fakeAICredStore struct {
	byTenant map[string]*domain.AICredential // ActiveAICredentialForTenant 的返回值
	blob     []byte

	cipherCalls int32  // AICredCiphertext 被调用次数
	lastTenant  string // 最近一次调用传来的租户参数
}

// ActiveAICredentialForTenant 实现 AICredStore：按测试预设的租户映射返回凭据，
// 查无即 nil（「无可用凭据」是正常状态）。
func (f *fakeAICredStore) ActiveAICredentialForTenant(_ context.Context, tenantID string) (*domain.AICredential, error) {
	return f.byTenant[tenantID], nil
}

// AICredCiphertext 实现 AICredStore：返回预设密文，并记下调用方传来的
// 租户参数——空串放行条款的传参正确性靠这个数字与字符串断言。
func (f *fakeAICredStore) AICredCiphertext(_ context.Context, _, tenantID string) ([]byte, error) {
	atomic.AddInt32(&f.cipherCalls, 1)
	f.lastTenant = tenantID
	return f.blob, nil
}

func newTestAICredVault(t *testing.T, store AICredStore) *AICredVault {
	t.Helper()
	c, err := crypt.NewCipher("unit-test-secret-value-123456")
	if err != nil {
		t.Fatal(err)
	}
	return NewAICredVault(c, store)
}

/* ── 解析规则 ─────────────────────────────────────────────── */

// TestAICredVault_TenantReadsPlatformCredential 锁定 FIX-8 的业务动机：
// 租户没配自己的凭据时，助手要用平台级默认凭据——vault 必须把「请求方租户」
// 传给 AICredCiphertext，repo 的 `tenant_id = ”` 放行条款据此让租户读到
// 平台凭据的密文。若这里改传空串或改传凭据归属租户，语义都会漂。
func TestAICredVault_TenantReadsPlatformCredential(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	store := &fakeAICredStore{
		byTenant: map[string]*domain.AICredential{
			// 租户 t1 解析到的是平台级凭据（tenant_id 为空串）
			"t1": {ID: "cred-plat", TenantID: "", BaseURL: "https://api.example.com/v1", Model: "m"},
		},
		blob: blob,
	}
	v := newTestAICredVault(t, store)

	cred, err := v.Credential(context.Background(), "t1")
	if err != nil || cred == nil || cred.APIKey != "lx-live-abcdef123456" {
		t.Fatalf("租户应能解析到平台默认凭据: %v %+v", err, cred)
	}
	if got := store.lastTenant; got != "t1" {
		t.Errorf("AICredCiphertext 应收到请求方租户 t1，实际 %q", got)
	}
}

// TestAICredVault_NoCredentialIsNotAnError 锁定「无凭据」是正常状态：
// 返回 (nil, nil) 且不进错误分支——助手据此降级到确定性兜底，
// 翻译、菜单、券等其它功能不受影响。
func TestAICredVault_NoCredentialIsNotAnError(t *testing.T) {
	v := newTestAICredVault(t, &fakeAICredStore{byTenant: map[string]*domain.AICredential{}})
	cred, err := v.Credential(context.Background(), "tenant-without-cred")
	if err != nil {
		t.Fatalf("没有凭据不应报错: %v", err)
	}
	if cred != nil {
		t.Errorf("应返回 nil 凭据，实际 %+v", cred)
	}
}

// TestAICredVault_CachesWithinTTL 锁定缓存语义：TTL 内二次解析不再查库
// （LLM 凭据解析在每次商家问答的路径上，不打缓存等于每条消息两次查询）。
func TestAICredVault_CachesWithinTTL(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	store := &fakeAICredStore{
		byTenant: map[string]*domain.AICredential{
			"t1": {ID: "cred-1", TenantID: "t1", BaseURL: "https://x/v1", Model: "m"},
		},
		blob: blob,
	}
	v := newTestAICredVault(t, store)

	ctx := context.Background()
	if _, err := v.Credential(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Credential(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&store.cipherCalls); n != 1 {
		t.Errorf("TTL 内应命中缓存（只解密 1 次），实际 %d 次", n)
	}
}

// TestAICredVault_InvalidateForcesReload 锁定 Invalidate：后台轮换 Key 后
// 必须立刻重新解析，不能等 30s TTL——否则商家看到「换了 Key 还用旧的」。
func TestAICredVault_InvalidateForcesReload(t *testing.T) {
	c, _ := crypt.NewCipher("unit-test-secret-value-123456")
	blob, _ := c.Encrypt("lx-live-abcdef123456")
	store := &fakeAICredStore{
		byTenant: map[string]*domain.AICredential{
			"t1": {ID: "cred-1", TenantID: "t1", BaseURL: "https://x/v1", Model: "m"},
		},
		blob: blob,
	}
	v := newTestAICredVault(t, store)

	ctx := context.Background()
	_, _ = v.Credential(ctx, "t1")
	v.Invalidate("t1")
	_, _ = v.Credential(ctx, "t1")
	if n := atomic.LoadInt32(&store.cipherCalls); n != 2 {
		t.Errorf("Invalidate 后应重新解密（2 次），实际 %d 次", n)
	}
}

// TestAICredVault_DecryptFailureSurfaces 锁定解密失败必须向上抛：
// 换了 LC_SECRET 或密文被改时，错误要抛出去让后台看见，
// 而不是静默按「无凭据」处理——那会把故障伪装成「没配 Key」。
func TestAICredVault_DecryptFailureSurfaces(t *testing.T) {
	store := &fakeAICredStore{
		byTenant: map[string]*domain.AICredential{
			"t1": {ID: "cred-1", TenantID: "t1", BaseURL: "https://x/v1", Model: "m"},
		},
		blob: []byte("garbage-not-a-ciphertext"),
	}
	v := newTestAICredVault(t, store)
	if _, err := v.Credential(context.Background(), "t1"); err == nil {
		t.Error("解密失败应向上抛错")
	}
}

// TestAICredVault_NoCipherMeansNoCredential 锁定 cipher 为 nil 的降级：
// 没配 LC_SECRET 时 Credential 恒返回无凭据（助手自动走规则模式），
// 绝不因为缺 key 而崩。
func TestAICredVault_NoCipherMeansNoCredential(t *testing.T) {
	v := NewAICredVault(nil, &fakeAICredStore{byTenant: map[string]*domain.AICredential{}})
	cred, err := v.Credential(context.Background(), "t1")
	if err != nil || cred != nil {
		t.Errorf("无 cipher 应返回 (nil, nil)，实际 %+v %v", cred, err)
	}
}

// 编译期断言：假存储满足 AICredStore（签名随 repo 一起收紧，此处防漂移）。
var _ AICredStore = (*fakeAICredStore)(nil)
