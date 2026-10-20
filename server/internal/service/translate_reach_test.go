package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 本文件覆盖 2026-10-19 字节级 UAT 的 F-5 修订：健康检查必须区分
// 「翻译链配了」与「翻译链连得上」。黑洞地址（端口没人听）以前会被
// 报成 live，运维看到的是「翻译很健康」，实际每次翻译都在降级到词典。

// deadURL 取一个确定没人听的地址：先占一个端口再立刻关掉。
// 比硬编码端口稳——不会撞上恰好有人在用的端口，也不会依赖防火墙行为。
func deadURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占端口失败: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

// chainStub 起一个假的翻译链：任何请求都应答，可指定状态码。
func chainStub(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 锁定探活语义：任何 HTTP 应答（含 405）都算「链活着」。
// 探活答的是 liveness 不是「支持 HEAD」——把 405 当不可达会误报。
func TestHTTPTranslator_Reachable_ServerAnswersCountsAsReachable(t *testing.T) {
	// 405（HEAD 不被该路由支持）也是「服务在应答」——探活答的是 liveness，
	// 不是「这个接口支持 HEAD」。真实链返回什么码都证明它活着。
	srv := chainStub(t, http.StatusMethodNotAllowed)

	tr := NewHTTPTranslator(srv.URL)
	if !tr.TranslateReachable(context.Background()) {
		t.Fatal("服务在应答就该判为可达")
	}
	if got := tr.TranslateEngine(); got != EngineChain {
		t.Errorf("引擎标识应为 chain，实际 %q", got)
	}
}

// ★ F-5 核心回归：黑洞地址必须判不可达。同例还钉住 Ready() 仍为 true——
// 证明「就绪」与「可达」是两个口径，旧代码正是把它们混为一谈才谎报 live。
func TestHTTPTranslator_Reachable_BlackHoleIsUnreachable(t *testing.T) {
	// ★ F-5 的核心回归：端口没人听，必须判不可达。
	tr := NewHTTPTranslator(deadURL(t))

	if !tr.Ready() {
		t.Fatal("Ready() 本例应为 true——它只反映「配了地址」，这正是病灶本身")
	}
	if tr.TranslateReachable(context.Background()) {
		t.Fatal("黑洞地址必须判为不可达，否则健康检查会谎报 live")
	}
}

// 锁定：没配链地址（空 URL）时不存在可达的链，不能报可达——
// 否则未配置翻译的服务也会在健康检查里显示 live。
func TestHTTPTranslator_Reachable_EmptyBaseURLIsUnreachable(t *testing.T) {
	tr := NewHTTPTranslator("")
	if tr.TranslateReachable(context.Background()) {
		t.Fatal("没配地址就没有可达的链，不该报可达")
	}
}

// 锁定 TTL 缓存语义：探测结果 30s 内复用（服务刚关不翻脸），
// 拨快 probe.at 强制过期后必须重新探测。防止每次健康检查都打网络。
func TestHTTPTranslator_Reachable_CachedWithinTTL(t *testing.T) {
	srv := chainStub(t, http.StatusOK)
	tr := NewHTTPTranslator(srv.URL)

	if !tr.TranslateReachable(context.Background()) {
		t.Fatal("首次探测应可达")
	}
	srv.Close() // 服务已关，但 TTL 内不该重新探测（懒探测的意义所在）

	if !tr.TranslateReachable(context.Background()) {
		t.Fatal("TTL 内应复用缓存结果，服务刚关不应立刻翻脸")
	}

	// 把缓存时间往前拨一格，强制过期后重新探测：此时服务真关了 → 不可达
	tr.probe.at = time.Now().Add(-time.Hour)
	if tr.TranslateReachable(context.Background()) {
		t.Fatal("TTL 过期后应重新探测，服务已关则应判不可达")
	}
}

// ★ F-5 病灶原位回归：兜底词典恒可达，若把它算进可达性，
// 健康检查会永远报 live。可达性只认主引擎；引擎标识同样只透传主引擎。
func TestFallbackTranslator_Reachable_DictFallbackMustNotMaskDeadPrimary(t *testing.T) {
	// ★ F-5 的老毛病就在这里：兜底词典恒可达，若把兜底算进 Ready，
	// 健康检查会永远报 live。可达性只认主引擎。
	f := &FallbackTranslator{primary: NewHTTPTranslator(deadURL(t)), fallback: DictTranslator{}}

	if !f.Ready() {
		t.Fatal("Ready() 恒真（含兜底），这正是旧口径的来源")
	}
	if f.TranslateReachable(context.Background()) {
		t.Fatal("主引擎不可达时，内置词典兜底不得把可达性抬成真")
	}
	if got := f.TranslateEngine(); got != EngineChain {
		t.Errorf("引擎标识应透传主引擎 chain，实际 %q", got)
	}
}

// 锁定：内置词典编译进二进制、不依赖网络，恒可达且引擎标识为 dict——
// 这是全链唯一「永远可达」的合法形态，与谎报 live 区分开。
func TestDictTranslator_Reachable_AlwaysTrue(t *testing.T) {
	// 词典内嵌在二进制里，不需要网络，也就不存在「探测」这回事。
	dict := DictTranslator{}
	if got := dict.TranslateEngine(); got != EngineDict {
		t.Errorf("引擎标识应为 dict，实际 %q", got)
	}
	if !dict.TranslateReachable(context.Background()) {
		t.Fatal("本地词典恒可达")
	}
}

// 锁定装配级口径：配了链地址（黑洞）时装配结果必须实现 TranslateReporter
// 且如实报 chain + 不可达——对应离线实例 :8099 的真实形态。
func TestNewTranslatorWithKeys_BlackHoleChainReportsUnreachable(t *testing.T) {
	// 对应离线实例 :8099 的形态：LC_TRANSLATE_URL 指向没人听的地址。
	tr := NewTranslatorWithKeys(deadURL(t), "", nil, nil)

	rep, ok := tr.(TranslateReporter)
	if !ok {
		t.Fatal("装配结果必须实现 TranslateReporter，否则健康检查拿不到诚实口径")
	}
	if got := rep.TranslateEngine(); got != EngineChain {
		t.Errorf("配了链地址就该报 chain，实际 %q", got)
	}
	if rep.TranslateReachable(context.Background()) {
		t.Fatal("黑洞链必须报不可达：健康检查要能一眼看出「翻译实际在走词典」")
	}
}

// 锁定装配级反向边界：活链装配后报 chain + 可达，
// 与上一条合起来夹住「诚实化」的两个方向。
func TestNewTranslatorWithKeys_LiveChainReportsReachable(t *testing.T) {
	srv := chainStub(t, http.StatusOK)
	tr := NewTranslatorWithKeys(srv.URL, "", nil, nil)

	rep := tr.(TranslateReporter)
	if got := rep.TranslateEngine(); got != EngineChain {
		t.Errorf("引擎标识应为 chain，实际 %q", got)
	}
	if !rep.TranslateReachable(context.Background()) {
		t.Fatal("链在应答就该报可达")
	}
}

// 锁定：未配链地址时引擎标识为 mymemory。刻意不触发探测——
// MyMemory 可达性依赖外网，单测联网会让结果时好时坏。
func TestNewTranslatorWithKeys_NoChainURLUsesMyMemoryEngine(t *testing.T) {
	// 只断言引擎标识，不触发探测——MyMemory 可达性依赖外网，单测不该联网。
	tr := NewTranslatorWithKeys("", "", nil, nil)
	rep := tr.(TranslateReporter)

	if got := rep.TranslateEngine(); got != EngineMyMemory {
		t.Errorf("未配链地址应走 MyMemory，实际 %q", got)
	}
}

// 锁定：TM 缓存层不得「缓存出」可达性——引擎标识与可达性都透传内层，
// 缓存命中只说明「以前翻过」，不说明引擎此刻活着。
func TestCachedTranslator_Reachable_DelegatesToInner(t *testing.T) {
	// 缓存层不改变引擎形态，也不该「缓存出」可达性。
	inner := &FallbackTranslator{primary: NewHTTPTranslator(deadURL(t)), fallback: DictTranslator{}}
	c := NewCachedTranslator(inner, newFakeCache())

	if got := c.TranslateEngine(); got != EngineChain {
		t.Errorf("缓存层应透传 chain，实际 %q", got)
	}
	if c.TranslateReachable(context.Background()) {
		t.Fatal("缓存命中不代表引擎活着，应透传内层的不可达")
	}
}

// 锁定：TenantPrimary 无租户上下文时无法判断有无能言 Key，
// 报保底路径（次级引擎）的形态与可达性，不猜、不装。
func TestTenantPrimary_Reachable_DelegatesToInner(t *testing.T) {
	// TenantPrimary 没有租户上下文时无法判断有没有能言 Key，
	// 因此报的是「保底路径」——次级引擎的形态与可达性。
	tp := NewTenantPrimary(nil, NewHTTPTranslator(deadURL(t)))
	if got := tp.TranslateEngine(); got != EngineChain {
		t.Errorf("应透传次级引擎 chain，实际 %q", got)
	}
	if tp.TranslateReachable(context.Background()) {
		t.Fatal("次级引擎不可达就该如实报不可达")
	}
}

// 锁定：没有 Key 时直接判不可达（探到平台活着也没意义，调不了），
// 有 Key 时引擎标识为 lexicorn。
func TestLexicornTranslator_Reachable_NeedsKey(t *testing.T) {
	// 没有 Key 就没法调用：探到平台活着也没有意义，直接判不可达。
	if NewLexicornTranslator("", "", "").TranslateReachable(context.Background()) {
		t.Fatal("无 Key 应判不可达")
	}
	if got := NewLexicornTranslator("", "k", "").TranslateEngine(); got != EngineLexicorn {
		t.Errorf("引擎标识应为 lexicorn，实际 %q", got)
	}
}

// 锁定回落口径：未实现自陈接口的实现（测试假件 / 自定义注入）按 dict 回落，
// nil 内层同样回落；且取引擎标识不得触发网络探测——
// engineOf 是纯配置读取，这是它与 reachableOf 的分界线。
func TestEngineOf_FallsBackToDictForPlainImplementations(t *testing.T) {
	// 没实现自陈接口的实现（测试假件 / 自定义注入）按 dict 回落，
	// 且取引擎标识不得触发网络探测（engineOf 是纯配置读取）。
	if got := engineOf(&fakeTranslator{name: "fake"}); got != EngineDict {
		t.Errorf("未实现自陈接口应回落 dict，实际 %q", got)
	}
	if !reachableOf(context.Background(), &fakeTranslator{}) {
		t.Fatal("未实现自陈接口时按「本地可达」回落")
	}
	if got := engineOf(nil); got != EngineDict {
		t.Errorf("nil 内层应回落 dict，实际 %q", got)
	}
}
