// 本文件：多语种翻译并发编排的测试。
//
// ★ 这里守的不是「能不能翻出来」，而是三件一旦退化就没人会发现的事：
//  1. 各语种是不是真的并发了——退化成串行，预览会从 ~10s 变回 ~30s，
//     单看返回值完全一样，只有「并发计量」能发现；
//  2. 并发是不是有上限——放开并发会把公开翻译引擎的限流打满，
//     线上表现为「一条都翻不出来」；
//  3. 「先收集、后合并」有没有串味——并发写共享 map 在 Go 里是 fatal error，
//     而这个 bug 只在**并发真的发生时**才炸，串行跑测试永远测不出来。
//     所以第 3 条是这次重构最该守的：各语种的译文必须落在各自的语种上。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

/* ── 可控的假翻译引擎 ───────────────────────────────────────── */

// concurrencyTranslator 是一个能「卡住 + 计量并发」的假翻译引擎。
//
// 译文固定为 "<语种>:<原文>"，这样测试能直接看出有没有串味——
// 若 en 的位置出现了 ja 的译文，一眼就能看见。
type concurrencyTranslator struct {
	// gateN > 0 时：每个语种进来先等，直到「同时在翻的语种数」达到 gateN 才放行。
	// ★ 这是检测「是不是真并发」的关键：串行时永远凑不齐 gateN，会一直卡住。
	gateN int
	gate  chan struct{}
	once  sync.Once

	// hold 是每次 Translate 人为停留的时长。
	// ★ 为什么必须能停留：fn 本身太轻（只是拼几个字符串），
	//   goroutine 大概率「起一个、跑完、再起下一个」，并发计量恒为 1——
	//   那样这条测试就变成一条会飘的测试：加 -race 变绿、不加变红。
	//   停留 30ms 让重叠成为必然，计量才是在测并发，而不是在测调度运气。
	hold time.Duration

	mu  sync.Mutex
	cur int // 当前在翻的语种数
	max int // 观测到的最大并发数

	// 收到的 ctx 是否带截止时间（用于断言截止时间真的传到了引擎这一层）
	muDeadline  sync.Mutex
	sawDeadline bool
}

func newConcurrencyTranslator(gateN int) *concurrencyTranslator {
	return &concurrencyTranslator{gateN: gateN, gate: make(chan struct{})}
}

// 实现编排器依赖的翻译接口：先记录 ctx 有没有带截止时间，再把「当前在翻的语种数」
// 加一并更新峰值，随后按 gateN 卡住直到凑够并发才放行。
// 边界：gateN=0 表示不设闸，进来即走；离开前必须把 cur 减回去（含走 ctx.Done() 的分支），
// 否则并发计数虚高，后面的「并发被限幅」断言会被自己污染。
func (c *concurrencyTranslator) Translate(ctx context.Context, texts []string, from, to string) ([]string, error) {
	if _, ok := ctx.Deadline(); ok {
		c.muDeadline.Lock()
		c.sawDeadline = true
		c.muDeadline.Unlock()
	}

	c.mu.Lock()
	c.cur++
	if c.cur > c.max {
		c.max = c.cur
	}
	reached := c.gateN > 0 && c.cur >= c.gateN
	c.mu.Unlock()

	if reached {
		c.once.Do(func() { close(c.gate) })
	}
	if c.gateN > 0 {
		select {
		case <-c.gate: // 凑够并发，放行
		case <-ctx.Done():
			c.mu.Lock()
			c.cur-- // 这条上下文里 cur-- 必须记账，否则并发计数会虚高
			c.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if c.hold > 0 {
		select {
		case <-time.After(c.hold):
		case <-ctx.Done():
		}
	}

	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = to + ":" + t
	}
	c.mu.Lock()
	c.cur--
	c.mu.Unlock()
	return out, nil
}

// 桩件恒定可用：让被测的编排逻辑只受并发与截止时间影响，不被引擎就绪态的分支带偏。
func (c *concurrencyTranslator) Ready() bool { return true }

// 引擎名只进日志与状态上报，桩件返回固定值，便于断言时能一眼分辨译文来自桩件还是真引擎。
func (c *concurrencyTranslator) Name() string {
	return "concurrency-stub"
}

// concurrencySnapshot 取并发观测值。
func (c *concurrencyTranslator) concurrencySnapshot() (cur, max int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur, c.max
}

func (c *concurrencyTranslator) sawDeadlineCtx() bool {
	c.muDeadline.Lock()
	defer c.muDeadline.Unlock()
	return c.sawDeadline
}

/* ── 1. 真的并发了吗 ────────────────────────────────────────── */

// TestTranslateAllTargets_RunsInParallel 锁死「各语种并发翻」。
//
// ★ 证伪方式：把 forEachTargetParallel 改回串行 for 循环，这个测试会
//
//	永远凑不齐 3 个并发而被 5 秒看门狗判死——它必须**卡住**，
//	而不是悄悄通过。这是它与普通「断言返回值」测试的根本区别。
func TestTranslateAllTargets_RunsInParallel(t *testing.T) {
	tr := newConcurrencyTranslator(3)
	targets := []string{"en", "ja", "ko"}

	done := make(chan struct{})
	go func() {
		_ = translateAllTargets(context.Background(), tr, []string{"宫保鸡丁"}, "zh", targets)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("并发编排卡死：三个语种没能同时进入翻译。" +
			"若已改回串行 for 循环，这里必然超时——说明并发没生效")
	}

	_, max := tr.concurrencySnapshot()
	if max < 3 {
		t.Fatalf("三个语种没有并发：观测到的最大并发 = %d，期望 >= 3", max)
	}
}

// TestTranslateAllTargets_ConcurrencyIsBounded 锁死「并发有上限」。
// 放开并发会把公开翻译引擎按 IP 的限流打满，线上表现为「一条都翻不出来」。
func TestTranslateAllTargets_ConcurrencyIsBounded(t *testing.T) {
	// 15 个语种：远超并发上限，逼出真实的最大并发
	var targets []string
	for i := 0; i < 15; i++ {
		targets = append(targets, fmt.Sprintf("l%02d", i))
	}
	tr := newConcurrencyTranslator(0)
	tr.hold = 30 * time.Millisecond // 让重叠成为必然，见 stub 里的说明

	_ = translateAllTargets(context.Background(), tr, []string{"宫保鸡丁"}, "zh", targets)

	_, max := tr.concurrencySnapshot()
	if max > translateTargetConcurrency {
		t.Fatalf("并发超出上限：最大并发 = %d，上限 = %d", max, translateTargetConcurrency)
	}
	if max < 2 {
		t.Fatalf("并发形同虚设：最大并发 = %d，说明又退回串行了", max)
	}
}

// TestTranslateAllTargets_ActuallyFasterThanSerial 用墙钟时间佐证并发真的生效。
//
// 只断言「最大并发 >= 2」仍可能被调度欺骗（goroutine 起得够快时计量会偶然撞上）；
// 墙钟时间是最朴素也最难造假的证据：15 个语种各停 30ms，
// 串行必然 >= 450ms，并发 3 只需 ~150ms。
// ★ 留足余量（阈值取 300ms）：CI 机器慢也不至于误判成串行。
func TestTranslateAllTargets_ActuallyFasterThanSerial(t *testing.T) {
	var targets []string
	for i := 0; i < 15; i++ {
		targets = append(targets, fmt.Sprintf("l%02d", i))
	}
	tr := newConcurrencyTranslator(0)
	tr.hold = 30 * time.Millisecond

	t0 := time.Now()
	_ = translateAllTargets(context.Background(), tr, []string{"宫保鸡丁"}, "zh", targets)
	elapsed := time.Since(t0)

	if elapsed > 300*time.Millisecond {
		t.Fatalf("15 个语种耗时 %v，超过并发应有的 ~150ms（串行应为 ~450ms）——并发没生效", elapsed)
	}
}

/* ── 2. 译文有没有串味 ──────────────────────────────────────── */

// TestTranslateAllTargets_NoCrossWiring 锁死「各语种译文落在各自语种上」。
//
// ★ 这是「先收集、后合并」重构最该守的一条：一旦有人图省事改成
//
//	在 goroutine 里直接写共享容器，轻则译文串味，重则 concurrent map writes
//	直接 fatal error 把进程打挂——而这两者都只在并发真的发生时才出现。
func TestTranslateAllTargets_NoCrossWiring(t *testing.T) {
	tr := newConcurrencyTranslator(0)
	texts := []string{"宫保鸡丁", "麻婆豆腐", "水煮鱼"}
	targets := []string{"en", "ja", "ko"}

	got := translateAllTargets(context.Background(), tr, texts, "zh", targets)

	if len(got) != len(targets) {
		t.Fatalf("结果层数 = %d，期望 %d", len(got), len(targets))
	}
	for li, tg := range targets {
		if len(got[li]) != len(texts) {
			t.Fatalf("语种 %s 的译文条数 = %d，期望 %d", tg, len(got[li]), len(texts))
		}
		for i, want := range texts {
			// 译文格式固定为 "<语种>:<原文>"，串味会立刻在这里暴露
			exp := tg + ":" + want
			if got[li][i] != exp {
				t.Fatalf("译文串味：第 %d 层（语种 %s）第 %d 条 = %q，期望 %q", li, tg, i, got[li][i], exp)
			}
		}
	}
}

// TestTranslateAllTargets_EmptyTexts 空文本直接返回，不空转也不 panic。
func TestTranslateAllTargets_EmptyTexts(t *testing.T) {
	tr := newConcurrencyTranslator(0)
	got := translateAllTargets(context.Background(), tr, nil, "zh", []string{"en", "ja"})
	if len(got) != 2 {
		t.Fatalf("结果层数 = %d，期望 2（空文本也要保持结构）", len(got))
	}
	for li := range got {
		if got[li] != nil {
			t.Fatalf("空文本时第 %d 层应为空，实际 = %v", li, got[li])
		}
	}
}

/* ── 3. 整包截止时间 ────────────────────────────────────────── */

// TestWithTranslateDeadline_ShortEnoughForGateway 锁死「截止时间小于网关超时」。
// 我们的截止时间必须**先于**网关触发：否则请求会被网关先切断，
// 前端拿到的是 504 而不是「翻出多少算多少」的部分结果。
func TestWithTranslateDeadline_ShortEnoughForGateway(t *testing.T) {
	const gatewayReadTimeout = 60 * time.Second
	if translateOverallDeadline >= gatewayReadTimeout {
		t.Fatalf("翻译截止时间 %v 不小于网关读超时 %v：请求会先被网关切断",
			translateOverallDeadline, gatewayReadTimeout)
	}
	if translateOverallDeadline < 10*time.Second {
		t.Fatalf("翻译截止时间 %v 过短：正常菜单都翻不完，等于人为制造失败", translateOverallDeadline)
	}
	ctx, cancel := withTranslateDeadline(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("withTranslateDeadline 没有给 Context 设截止时间")
	}
}

// TestPreviewTranslate_PassesDeadlineToEngine 锁死「截止时间真的传到了引擎」。
// 只在 handler 里 WithTimeout 但没把 ctx 传下去，等于没设——这条防的就是这个。
func TestPreviewTranslate_PassesDeadlineToEngine(t *testing.T) {
	tr := newConcurrencyTranslator(0)
	srv := &Server{tr: tr}

	body := `{"items":[{"name":"宫保鸡丁","selected":true}],"from":"zh","targets":["en","ja"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/public/translate-preview", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handlePreviewTranslate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", rr.Code, rr.Body.String())
	}
	if !tr.sawDeadlineCtx() {
		t.Fatal("引擎没有收到带截止时间的 Context：外部引擎慢下来时这个请求仍会被无限挂住")
	}
}

/* ── 4. 端到端：译文落到正确的菜上 ─────────────────────────── */

// TestPreviewTranslate_LandsOnRightItems 走完整 handler，断言译文与分类各就各位。
func TestPreviewTranslate_LandsOnRightItems(t *testing.T) {
	tr := newConcurrencyTranslator(0)
	srv := &Server{tr: tr}

	payload := map[string]any{
		"items": []map[string]any{
			{"name": "宫保鸡丁", "price": 38, "category": "热菜", "selected": true},
			{"name": "麻婆豆腐", "price": 32, "category": "热菜", "selected": true},
		},
		"from":    "zh",
		"targets": []string{"en", "ja", "ko"},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("构造请求体失败：%v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/public/translate-preview", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	srv.handlePreviewTranslate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", rr.Code, rr.Body.String())
	}

	var got struct {
		Items []struct {
			Name   string            `json:"name"`
			NameEn string            `json:"name_en"`
			Names  map[string]string `json:"names"`
		} `json:"items"`
		Categories map[string]map[string]string `json:"categories"`
		Targets    []string                     `json:"targets"`
		Engine     string                       `json:"engine"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析响应失败：%v（body=%s）", err, rr.Body.String())
	}
	if len(got.Items) != 2 {
		t.Fatalf("返回菜品数 = %d，期望 2", len(got.Items))
	}

	want := []string{"宫保鸡丁", "麻婆豆腐"}
	for i, it := range got.Items {
		if it.Name != want[i] {
			t.Fatalf("第 %d 道菜的原文 = %q，期望 %q（原文绝不能被译文覆盖）", i, it.Name, want[i])
		}
		for _, tg := range []string{"en", "ja", "ko"} {
			exp := tg + ":" + want[i]
			if it.Names[tg] != exp {
				t.Fatalf("第 %d 道菜的 %s 译文 = %q，期望 %q（译文串味或漏合并且）", i, tg, it.Names[tg], exp)
			}
		}
		// name_en 是兼容字段，必须与 names["en"] 一致
		if it.NameEn != it.Names["en"] {
			t.Fatalf("第 %d 道菜 name_en = %q，与 names.en = %q 不一致", i, it.NameEn, it.Names["en"])
		}
	}

	// 分类也要逐语种翻出来，否则客人端会出现「外语菜名 + 中文分类」的割裂页面
	for _, tg := range []string{"en", "ja", "ko"} {
		if got.Categories[tg]["热菜"] != tg+":热菜" {
			t.Fatalf("分类译文 %s.热菜 = %q，期望 %q", tg, got.Categories[tg]["热菜"], tg+":热菜")
		}
	}
	if got.Engine != "concurrency-stub" {
		t.Fatalf("engine = %q，期望 concurrency-stub", got.Engine)
	}
}

// TestPreviewTranslate_NoItemsIsBadRequest 空清单应被挡在翻译之前。
func TestPreviewTranslate_NoItemsIsBadRequest(t *testing.T) {
	srv := &Server{tr: newConcurrencyTranslator(0)}
	req := httptest.NewRequest(http.MethodPost, "/api/public/translate-preview",
		strings.NewReader(`{"items":[],"targets":["en"]}`))
	rr := httptest.NewRecorder()
	srv.handlePreviewTranslate(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rr.Code)
	}
}
