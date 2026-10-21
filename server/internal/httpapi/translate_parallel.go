// 本文件：多语种翻译的并发编排。
//
// 修的是一个真实的卡死级性能缺陷（2026-10-21 由「引导流 P4 端到端渲染回归」暴露）：
// 翻译引擎是「每语种每词一次外部 HTTP 调用」。原先各处都是
//
//	for _, tg := range targets { tr.Translate(...) }   ← 串行
//
// 于是一次「11 道菜 + 5 个分类」× 3 语种 的预览 = 48 次串行往返。
// 冷缓存实测 20~30 秒，而在预览返回之前，前端 P4 的「下一步」一直是
// 禁用的——商家看到的就是「页面卡死」，引导流在价值最高的一步断掉。
//
// 这里同时解决了两件事，缺一不可：
//  1. 并发：语种之间互不影响，并发翻，冷缓存从 ~30s 压到 ~10s；
//  2. 整包截止时间：外部引擎再慢也必须在这个时间内返回（哪怕只翻出一半），
//     否则一个慢请求就能把这个 HTTP 连接无限挂住。
//     只有并发没有截止时间的话，「外部引擎整体变慢」时照样会卡死。
package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/service"
)

// translateOverallDeadline 是一次翻译请求的最长等待时间。
//
// ★ 为什么必须有：外部翻译引擎不受我们控制。没有整包截止时间时，
//
//	引擎变慢会直接把这个 HTTP 请求无限挂住，连接数与 goroutine 一起涨。
//
// ★ 为什么是 45s 而不是更短：11 道菜 + 5 分类 × 3 语种、冷缓存、并发 3 的
//
//	实测约 10~12 秒；45s 给到近 4 倍余量，既能覆盖更大的菜单，
//	又稳稳小于网关的 proxy_read_timeout（60s），不会先被网关切断。
//
// ★ 超时不是错误：翻译引擎本身逐条失败不中断（见 MemoryTranslator.Translate），
//
//	超时后已翻出的部分照常返回，没翻出的留空、由前端回落显示原文——不编造。
const translateOverallDeadline = 45 * time.Second

// translateTargetConcurrency 是同时翻译的语种数上限。
//
// ★ 刻意压到 3 而不是「有多少语种开多少 goroutine」：
//
//	公开翻译引擎按 IP 限流，并发拉满会拿到 429，反而一条都翻不出来。
//	这里要的是「快 2~3 倍」，不是「把连接打满」。
const translateTargetConcurrency = 3

// withTranslateDeadline 给整次翻译套上截止时间。
// 调用方必须 defer cancel()，否则父 Context 会一直持有这个定时器。
func withTranslateDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, translateOverallDeadline)
}

// forEachTargetParallel 并发处理每个目标语种，全部完成后才返回。
//
// ★ 为什么并发安全由调用方承诺、这里不加锁：
//
//	本函数无法知道 fn 会写什么。约定是「fn 只在自己的 goroutine 里收集结果，
//	Wait 返回后再由调用方合并」。理由见下面两个调用点——
//	Go 的 map 并发写是 **fatal error（必崩）**，不是「概率性竞态」，
//	所以绝不能心存侥幸地在 fn 里直接写共享 map。
//	并发写「切片的不同下标」是安全的（不同内存位置），
//	这也是各调用点一律用 perLang[li] 收集、事后合并的原因。
func forEachTargetParallel(targets []string, fn func(li int, tg string)) {
	if len(targets) == 0 {
		return
	}
	limit := translateTargetConcurrency
	if limit > len(targets) {
		limit = len(targets)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for li, tg := range targets {
		wg.Add(1)
		go func(li int, tg string) {
			defer wg.Done()
			sem <- struct{}{} // 信号量：并发上限，防止把外部引擎的限流打满
			defer func() { <-sem }()
			fn(li, tg)
		}(li, tg)
	}
	wg.Wait()
}

// translateAllTargets 把同一批文本并发翻成多个目标语种。
//
// 返回 [语种下标][文本下标]；某个语种整批失败时该层为 nil（不影响其它语种）。
// 它是 handlePreviewTranslate 与 translateCategories 共用的那一层：
// 两处的输入文本对各语种都相同，所以能直接按语种切分。
//
// 商家端正式翻译 handleTranslate 不复用它——那里每个语种要翻的菜不同
// （已有译文的跳过），文本集合随语种变化，只能用 forEachTargetParallel 自己编排。
func translateAllTargets(
	ctx context.Context,
	tr service.Translator,
	texts []string,
	from string,
	targets []string,
) [][]string {
	perLang := make([][]string, len(targets))
	if len(texts) == 0 {
		return perLang
	}
	forEachTargetParallel(targets, func(li int, tg string) {
		res, err := tr.Translate(ctx, texts, from, tg)
		// 该语种整批失败且无任何结果：留 nil，不影响其它语种
		if err != nil && len(res) == 0 {
			return
		}
		perLang[li] = res // 只写自己的下标，并发安全
	})
	return perLang
}
