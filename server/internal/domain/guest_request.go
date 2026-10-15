package domain

// 本文件：客人服务请求（服务铃 / 加水 / 加菜 / 买单）。
//
// 定位：扫桌码的外国客人与门店服务员之间的唯一通道，
// 也是 AI 点菜助手所有动作的落点——助手只提议按钮，客人点一下才生成请求。
//
// 对应迁移：migrations/0006_org_rbac.sql 的 guest_request 表。
//
// 为什么不用 event 埋点表承载：埋点是「发生过什么」的流水，只增不改；
// 服务请求是「待处理的任务」，有状态、要被商家轮询、要能按桌号聚合、
// 处理完要能标记完成。两者生命周期完全不同，混用一张表会让两边都难做。

// 请求类型。
//
// ★ AI 点菜助手只允许提议这几种，且每种都对应页面上一个真实存在的按钮。
// 新增类型时必须同步前端按钮，否则助手会提议一个点不动的动作。
const (
	// ReqBell 服务铃：叫服务员（最通用，任何答不上的场景都该给这个）
	ReqBell = "bell"
	// ReqWater 加水
	ReqWater = "water"
	// ReqAddDish 加菜：payload 里带 dish_id 与 qty
	ReqAddDish = "add_dish"
	// ReqBill 买单 / 结账
	ReqBill = "bill"
	// ReqOther 其它：note 里带客人原话
	ReqOther = "other"
)

// ValidRequestKind 判断请求类型是否登记在册。
// 不校验会让前端 / 助手写入任意字符串，商家端就得分不清该干什么。
func ValidRequestKind(k string) bool {
	switch k {
	case ReqBell, ReqWater, ReqAddDish, ReqBill, ReqOther:
		return true
	}
	return false
}

// 请求状态。
const (
	// ReqOpen 待处理（商家端待办列表的默认过滤条件）
	ReqOpen = "open"
	// ReqDone 已完成
	ReqDone = "done"
	// ReqCancelled 已取消（客人误点 / 商家拒绝）
	ReqCancelled = "cancelled"
)

// ValidRequestStatus 判断状态取值是否登记在册。
func ValidRequestStatus(s string) bool {
	return s == ReqOpen || s == ReqDone || s == ReqCancelled
}

// GuestRequest 一条客人服务请求。
type GuestRequest struct {
	ID        string         `json:"id"`
	StoreID   string         `json:"store_id"`
	TableRef  string         `json:"table_ref"`         // 桌码 ref（如 t01）；空 = 未关联桌
	Kind      string         `json:"kind"`              // bell / water / add_dish / bill / other
	Payload   map[string]any `json:"payload,omitempty"` // add_dish 时 {"dish_id":"...","qty":1}
	Lang      string         `json:"lang,omitempty"`    // 客人界面语言（商家端据此展示对应提示）
	Note      string         `json:"note,omitempty"`    // 客人原话（语音转写后的文本，便于服务员理解上下文）
	Status    string         `json:"status"`            // open / done / cancelled
	CreatedAt int64          `json:"created_at"`        // Unix 秒
	// DishName 是附带信息，不落库：加菜请求时把菜名一并带给商家端，
	// 省得服务员再回菜单里查一遍这个 ID 是什么菜。
	DishName string `json:"dish_name,omitempty"`
}

// KindLabelCN 请求类型的中文名（商家端待办列表展示）。
func KindLabelCN(kind string) string {
	switch kind {
	case ReqBell:
		return "服务铃"
	case ReqWater:
		return "加水"
	case ReqAddDish:
		return "加菜"
	case ReqBill:
		return "买单"
	case ReqOther:
		return "其它"
	}
	return "未知请求"
}
