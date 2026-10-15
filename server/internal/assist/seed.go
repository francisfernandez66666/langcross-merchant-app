package assist

// 内置知识库、话术与流程种子。
//
// 为什么内置而不是全部来自数据库：经营助手的「平台侧知识」
// （怎么翻菜单、核销券怎么用、翻译质量分几档）是产品自身的说明，
// 与任何一家门店的数据无关，随二进制发布最稳——换门店、换租户都不变，
// 也不会因为运营后台一次误删就让助手变哑巴。
//
// 平台侧答案一律走 Script=true（话术直配，免 LLM）：这些答案必须字字确定，
// 不能今天一个说法明天一个说法。涉及门店数据的部分由第 4 道 LLM + 事实注入完成。

// seedKnowledge 平台侧知识 / 话术。
func seedKnowledge() []KnowledgeEntry {
	return []KnowledgeEntry{
		{
			Key: "how-translate", Title: "怎么把菜单翻成外语",
			Content:  "三步：①在「菜单」页上传或录入菜品（可先用 OCR 识别）→ ②确认原文稿（这是翻译的唯一真值，必须人工核对一遍）→ ③点「多语化」勾选目标语种。未确认的原文稿不允许翻译，这是为了防止把错的菜名翻给外国客人看。",
			Keywords: []string{"翻译", "多语化", "外语", "菜单怎么", "翻一下", "怎么翻"},
			Weight:   90, Script: true,
		},
		{
			Key: "lang-quality", Title: "支持哪些语言、质量如何",
			Content:  "目前支持 10 个语种，分三档质量：承诺档 中文/英语；较好档 日语/韩语/泰语；尽力而为档 法语/西班牙语/德语/葡萄牙语/越南语。菜名是固定短语，翻出来通常可用；若发现某道菜的译文不对，可以在菜单里直接改这一个词。",
			Keywords: []string{"哪些语言", "支持什么语言", "语种", "质量", "准不准", "日语", "韩语", "泰语", "法语", "德语"},
			Weight:   80, Script: true,
		},
		{
			Key: "untranslated-mark", Title: "客人端显示未翻译怎么办",
			Content:  "出现「未翻译」说明这道菜在那个语种下没有译文。两种处理：①回到「多语化」页补翻这个语种；②如果这道菜不该给外国客人看，可以在菜单里把它下架。我们不会拿中文或瞎猜的内容冒充译文——未翻就是未翻，这是对客人负责。",
			Keywords: []string{"未翻译", "没翻译", "未翻訳", "显示原文", "看到原文", "空白", "缺失"},
			Weight:   85, Script: true,
		},
		{
			Key: "coupon-redeem", Title: "怎么核销优惠券",
			Content:  "在「核销台」输入客人出示的券码即可。注意两点：券必须是在本店领取的（跨店券核销会被拒绝）；每张券只能核销一次，重复核销会提示冲突。核销记录会进入经营洞察。",
			Keywords: []string{"核销", "券码", "优惠券", "验证", "re deem"},
			Weight:   75, Script: true,
		},
		{
			Key: "qr-material", Title: "门口码和桌码怎么用",
			Content:  "建店后系统会自动生成门口码与桌码两种二维码，内容是你的门店页地址，印在物料上长期有效。门口码适合贴入口（统计「路过扫码」），桌码适合贴桌面（统计「入座点单」）。两张码带不同来源标记，经营洞察里能分开看。",
			Keywords: []string{"二维码", "门口码", "桌码", "物料", "打印", "贴"},
			Weight:   70, Script: true,
		},
		{
			Key: "rating-meaning", Title: "评价的三个维度是什么",
			Content:  "客人打三个分：菜品、服务、会不会再来。第三个分（复购意愿）是这套评价里最有价值的——它直接预测客人会不会二次到店。我们只统计扫了我们码、用了我们工具的外国客数据，不是全店经营数据，看趋势和占比可以，别当成营业额。",
			Keywords: []string{"评价", "评分", "打分", "星级", "复购"},
			Weight:   60, Script: true,
		},
		{
			Key: "data-scope", Title: "经营洞察的数据口径",
			Content:  "只覆盖「扫了我们的码、用了我们工具」的外国客人。因此它是「外语客服务效果」的镜子，不是全店营业额。看两个数就够：扫码数（有多少外国客进来）与券核销率（他们是否真的回来了）。",
			Keywords: []string{"洞察", "数据口径", "统计什么", "统计范围", "营业额"},
			Weight:   65, Script: true,
		},
		{
			Key: "dish-image", Title: "菜品图片怎么传",
			Content:  "在菜单里点菜品右侧的「图片」即可上传，支持 PNG / JPEG / WebP / GIF，单张不超过 5MB。图片按菜品名保存，重新翻译不会丢图。没有图的菜品在客人端显示占位块，不会破版。",
			Keywords: []string{"图片", "上传", "照片", "配图"},
			Weight:   55, Script: true,
		},
		{
			Key: "flow-translate-guide", Title: "手把手教我翻译菜单",
			Content: "好，我分步带你走一遍菜单多语化。",
			// ★ 不收「教我」：它在「评价维度教教我」这种句子里也会出现，
			// 会把无关问题拽进翻译引导。剩下的四个词都是明确的求助意图。
			Keywords: []string{"手把手", "一步步", "带我", "引导"},
			Weight:   88, Flow: "translate-guide",
		},
		{
			Key: "flow-newstore-guide", Title: "新手开店引导",
			Content:  "好，我从建店开始带你过一遍。",
			Keywords: []string{"新手", "刚注册", "开店流程", "从零开始", "怎么开始"},
			Weight:   82, Flow: "newstore-guide",
		},
	}
}

// seedFlows 多轮流程定义。
// 每一步都是「说一句 + 给一个动作」：商家照做即可，不需要理解系统结构。
func seedFlows() map[string][]FlowStep {
	return map[string][]FlowStep{
		"translate-guide": {
			{
				Ask: "第一步：把菜单录进来。可以打字，也可以上传菜单照片让 OCR 识别（识别后一定要人工校对一遍）。",
				Actions: []Action{
					{Key: "menu", Name: "去录入菜单", URL: "/admin/menu"},
				},
			},
			{
				Ask: "第二步：确认原文稿。确认之后原文就被冻结为真值，所有译文都以它为准——所以这一步请务必逐道菜核对。",
				Actions: []Action{
					{Key: "confirm", Name: "去确认原文稿", URL: "/admin/menu"},
				},
			},
			{
				Ask: "第三步：点多语化，勾选目标语种。建议先翻英语和日语（来华客人最多的两个语种），其余语种随时补翻，不影响已有译文。",
				Actions: []Action{
					{Key: "translate", Name: "去多语化", URL: "/admin/translate"},
				},
			},
			{
				Ask: "完成。回到菜品列表能看到每道菜的各语种译文；客人端切到对应语言就会看到。有错的词可以直接在菜单里改。",
			},
		},
		"newstore-guide": {
			{
				Ask: "第一步：建店。只需要一个店名，系统会自动生成门店页地址和二维码。",
				Actions: []Action{
					{Key: "newstore", Name: "去建店", URL: "/admin/stores/new"},
				},
			},
			{
				Ask: "第二步：打印二维码。门口码贴入口、桌码贴桌面，两张码的来源标记不同，洞察里能分开统计。",
				Actions: []Action{
					{Key: "qr", Name: "去下载二维码", URL: "/admin/materials"},
				},
			},
			{
				Ask: "第三步：录菜单并多语化（可以说「带我翻译菜单」，我会分步带你来）。完成这三步，外国客人扫码就能看到自己语言的菜单了。",
			},
		},
	}
}

// seedSynonyms 同义词归一表（用户说法 → 标准词）。
// 只收「高频且语义确实相同」的说法，避免过度归一把不同问题混成一类。
func seedSynonyms() map[string]string {
	return map[string]string{
		"菜谱":  "菜单",
		"菜单页": "菜单",
		"译一下": "翻译",
		"翻一下": "翻译",
		"多语言": "多语化",
		"优惠券": "券",
		"折扣券": "券",
		"评分":  "评价",
		"打分":  "评价",
	}
}
