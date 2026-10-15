package service

// Lang 是一个受支持的语言。
type Lang struct {
	Code   string `json:"code"`    // BCP-47 主语言码，如 ja
	Native string `json:"native"`  // 母语写法，用于语言选择器
	NameZh string `json:"name_zh"` // 中文名，用于商家端
	Tier   int    `json:"tier"`    // 质量分层：1 承诺质量 / 2 较好 / 3 尽力而为
}

// SourceLang 是原文语种（本期只做中文菜单，架构上语种无关）。
const SourceLang = "zh"

// SupportedLangs 是产品支持的语言。
// 架构与语种无关：新增一门语言只需在此登记 + 引擎支持即可，业务代码零改动。
var SupportedLangs = []Lang{
	{Code: "zh", Native: "中文", NameZh: "中文", Tier: 1},
	{Code: "en", Native: "English", NameZh: "英语", Tier: 1},
	{Code: "ja", Native: "日本語", NameZh: "日语", Tier: 2},
	{Code: "ko", Native: "한국어", NameZh: "韩语", Tier: 2},
	{Code: "th", Native: "ไทย", NameZh: "泰语", Tier: 2},
	{Code: "fr", Native: "Français", NameZh: "法语", Tier: 3},
	{Code: "es", Native: "Español", NameZh: "西班牙语", Tier: 3},
	{Code: "de", Native: "Deutsch", NameZh: "德语", Tier: 3},
	{Code: "pt", Native: "Português", NameZh: "葡萄牙语", Tier: 3},
	{Code: "vi", Native: "Tiếng Việt", NameZh: "越南语", Tier: 3},
}

// IsSupportedLang 判断语种是否受支持。
func IsSupportedLang(code string) bool {
	for _, l := range SupportedLangs {
		if l.Code == code {
			return true
		}
	}
	return false
}
