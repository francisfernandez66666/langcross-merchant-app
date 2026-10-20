package httpapi

import (
	"reflect"
	"testing"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
)

/* ── normalizeTargets：目标语种清洗 ─────────────────────────
   这条链路决定了「客人端会出现哪些语言」，任何放宽都可能让页面
   显示未登记的语种（例如质量很差的意大利语）。故用测试钉死行为。 */

// 验证目标语种清洗的兜底：langs 与 to 都为空时必须落到 [en]，并兼容
// 旧的单语种 to 参数——空列表会让前端无从渲染。
func TestNormalizeTargets_DefaultsToEnglish(t *testing.T) {
	// 两者都空：必须兜到 en，而不是返回空列表（否则前端无从渲染）
	if got := normalizeTargets(nil, ""); !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("空入参应兜底为 [en]，实际 %v", got)
	}
	// 兼容旧的单语种参数 to
	if got := normalizeTargets(nil, "ja"); !reflect.DeepEqual(got, []string{"ja"}) {
		t.Errorf("应回落到 to 参数，实际 %v", got)
	}
}

// 验证原文语种（zh）与未登记语种（it/ru）必须被剔除：客人端只能出现
// 注册表内的目标语，否则页面会显示质量不达标的语言。
func TestNormalizeTargets_RemovesSourceAndUnsupported(t *testing.T) {
	got := normalizeTargets([]string{"en", "zh", "it", "ja", "ru"}, "")
	want := []string{"en", "ja"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("应剔除原文语种 zh 与未登记语种 it/ru：want=%v got=%v", want, got)
	}
}

// 验证去空、去重且保序：带空白的重复入参应收敛成 [en, ja]，顺序抖动
// 会让同一店铺每次生成的语种列表都不一样。
func TestNormalizeTargets_DedupesAndTrims(t *testing.T) {
	got := normalizeTargets([]string{" en ", "en", "ja", "", "  ", "ja"}, "")
	want := []string{"en", "ja"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("应去空、去重、保序：want=%v got=%v", want, got)
	}
}

// 验证全部入参非法时兜底为 [en] 而不是返回空列表：调用方永远拿得到
// 非空的目标语种集合。
func TestNormalizeTargets_AllInvalidFallsBackToEnglish(t *testing.T) {
	// 全是不支持项：不能让调用方拿到空列表
	got := normalizeTargets([]string{"zh", "it"}, "")
	if !reflect.DeepEqual(got, []string{"en"}) {
		t.Errorf("全部非法时应兜底为 [en]，实际 %v", got)
	}
}

// 验证 9 个受支持语种应原样保留：防止清洗逻辑过度收紧，把正常需求削掉。
func TestNormalizeTargets_KeepsAllSupported(t *testing.T) {
	in := []string{"en", "ja", "ko", "th", "fr", "es", "de", "pt", "vi"}
	got := normalizeTargets(in, "")
	if !reflect.DeepEqual(got, in) {
		t.Errorf("9 个受支持语种应原样保留：%v", got)
	}
}

/* ── distinctPreviewCategories：分类名去重 ────────────────── */

// 验证预览分类名去重、保序，且跳过空与纯空白分类：空分类名渲染成 Tab
// 就是废按钮。
func TestDistinctPreviewCategories_DedupesAndKeepsOrder(t *testing.T) {
	items := []PreviewItem{
		{Name: "宫保鸡丁", Category: "热菜"},
		{Name: "麻婆豆腐", Category: "热菜"},
		{Name: "米饭", Category: "主食"},
		{Name: "可乐", Category: ""},   // 空分类应跳过
		{Name: "啤酒", Category: "  "}, // 纯空白也应跳过
		{Name: "酸辣汤", Category: "汤"},
	}
	got := distinctPreviewCategories(items)
	want := []string{"热菜", "主食", "汤"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want=%v got=%v", want, got)
	}
}

// 验证空输入返回空列表，不给调用方塞 nil 或占位值。
func TestDistinctPreviewCategories_EmptyInput(t *testing.T) {
	if got := distinctPreviewCategories(nil); len(got) != 0 {
		t.Errorf("空输入应返回空列表，实际 %v", got)
	}
}

/* ── distinctCategories：菜品分类名去重 ───────────────────── */

// 验证菜品分类名去重并跳过无分类菜品：菜单筛选器里只应出现实际存在的
// 分类。
func TestDistinctCategories_OnDishes(t *testing.T) {
	dishes := []domain.Dish{
		{NameZh: "宫保鸡丁", Category: "热菜"},
		{NameZh: "麻婆豆腐", Category: "热菜"},
		{NameZh: "拍黄瓜", Category: "凉菜"},
		{NameZh: "米饭"}, // 无分类
	}
	got := distinctCategories(dishes)
	want := []string{"热菜", "凉菜"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want=%v got=%v", want, got)
	}
}

/* ── mergeCategoryNames：分类译文的合并 ───────────────────── */

// 验证分类译文按语种合并：同语种新分类追加、旧分类保留、新语种整表
// 新增——增量翻译不得冲掉已有译文。
func TestMergeCategoryNames_MergesByLanguage(t *testing.T) {
	existing := map[string]map[string]string{
		"en": {"热菜": "Hot Dishes"},
		"ja": {"热菜": "ホットディッシュ"},
	}
	add := map[string]map[string]string{
		"en": {"汤": "Soups"},
		"ko": {"热菜": "핫디시"},
	}
	got := mergeCategoryNames(existing, add)

	if got["en"]["热菜"] != "Hot Dishes" || got["en"]["汤"] != "Soups" {
		t.Errorf("en 应合并两个分类：%v", got["en"])
	}
	if got["ja"]["热菜"] != "ホットディッシュ" {
		t.Errorf("ja 应保留：%v", got["ja"])
	}
	if got["ko"]["热菜"] != "핫디시" {
		t.Errorf("ko 应新增：%v", got["ko"])
	}
}

// 验证同语种同分类后写覆盖：人工校正过的译文必须能通过再次合并生效。
func TestMergeCategoryNames_NewValuesWin(t *testing.T) {
	// 同语种同分类后写覆盖：便于人工校正后的结果生效
	existing := map[string]map[string]string{"en": {"热菜": "Hot dish"}}
	add := map[string]map[string]string{"en": {"热菜": "Hot Dishes"}}
	if got := mergeCategoryNames(existing, add); got["en"]["热菜"] != "Hot Dishes" {
		t.Errorf("后写应覆盖，实际 %v", got["en"]["热菜"])
	}
}

// 验证空增量不破坏既有译文表：没有新译文时不得把旧表清空。
func TestMergeCategoryNames_EmptyAddKeepsExisting(t *testing.T) {
	existing := map[string]map[string]string{"en": {"热菜": "Hot Dishes"}}
	if got := mergeCategoryNames(existing, nil); got["en"]["热菜"] != "Hot Dishes" {
		t.Errorf("空增量不应破坏既有表：%v", got)
	}
}

/* ── clampScore：评价分数收敛到 1–5 ───────────────────────── */

// 验证评价分数收敛到 1–5：0 与 -3 抬到 1，9 与 100 压到 5，越界值
// 不得原样落库。
func TestClampScore(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 1}, {-3, 1}, {1, 1}, {3, 3}, {5, 5}, {9, 5}, {100, 5},
	}
	for _, c := range cases {
		if got := clampScore(c.in); got != c.want {
			t.Errorf("clampScore(%d)=%d，期望 %d", c.in, got, c.want)
		}
	}
}
