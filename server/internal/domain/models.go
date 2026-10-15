// Package domain 定义核心领域模型（与数据库表一一对应，字段名对前端 JSON 友好）。
//
// 这里是「契约层」：结构体字段既映射 PostgreSQL 列，又通过 json tag 直接序列化给
// 前端，因此字段命名与增删都牵动两端。repo 层负责在表与这些结构体之间搬运，
// 上层（httpapi / service）只认这些类型。可选字段用 omitempty 或指针表达「未设置」。
package domain

import "time"

// Account 商家主账号。注册只收手机号。
type Account struct {
	ID        string    `json:"id"`         // 账号 ID（idgen 生成的随机十六进制串）
	Phone     string    `json:"phone"`      // 手机号，全局唯一，是注册与登录的凭据
	CreatedAt time.Time `json:"created_at"` // 注册时间
}

// Store 门店。slug 定死为 /s/{slug}，物料印刷后不可变。
//
// 门店是租户边界：菜品、原文稿、评价、券、埋点均以 ID 归属。slug 是印在门口码 /
// 桌贴二维码里的短链标识，一旦生成不可修改（改了旧码就扫不开）。
type Store struct {
	ID            string   `json:"id"`                       // 门店 ID
	AccountID     string   `json:"account_id"`               // 所属账号，一个账号可开多店
	Name          string   `json:"name"`                     // 店名
	Addr          string   `json:"addr,omitempty"`           // 地址
	GeoLat        *float64 `json:"geo_lat,omitempty"`        // 纬度（可选）
	GeoLng        *float64 `json:"geo_lng,omitempty"`        // 经度（可选）
	WifiSSID      string   `json:"wifi_ssid,omitempty"`      // WiFi 名（注册后补填，可选）
	WifiPwd       string   `json:"wifi_pwd,omitempty"`       // WiFi 密码
	BusinessHours string   `json:"business_hours,omitempty"` // 营业时间
	CouponEnabled bool     `json:"coupon_enabled"`           // 券开关，同时是店型标签
	ServiceTier   string   `json:"service_tier"`             // pure 纯翻译档 / service 外客服务档
	OrderMode     string   `json:"order_mode"`               // assist 辅助型 / replace 替代型
	Slug          string   `json:"slug"`                     // 客人端短链 /s/{slug}，物料定死不可变
	// CategoryNames 是分类名的多语种表：{lang: {原文分类: 译文}}。
	// 与 Dish.Names 同构，保证客人端「分类标题 + 菜名」整体切换到目标语言。
	CategoryNames map[string]map[string]string `json:"category_names,omitempty"`
	CreatedAt     time.Time                    `json:"created_at"` // 建店时间
}

// Dish 菜品（多语化后的成品）。
//
// name_zh 永远是原文真值，其它语种统一放在 Names（names_json）里；翻译是整体
// 替换菜单，唯一可单独变更的动态字段是 SoldOut 与 Remaining。
type Dish struct {
	ID          string            `json:"id"`                    // 菜品 ID
	StoreID     string            `json:"store_id"`              // 所属门店
	NameZh      string            `json:"name_zh"`               // 原文名（真值，永不改写）
	NameEn      string            `json:"name_en,omitempty"`     // 英文名（历史字段，逐步并入 Names）
	Names       map[string]string `json:"names,omitempty"`       // 多语种菜名，如 {"en":"...","ja":"..."}
	Description string            `json:"description,omitempty"` // 描述
	Price       *float64          `json:"price,omitempty"`       // 价格（nil 表示未定价）
	Category    string            `json:"category,omitempty"`    // 分类名（原文），多语在 Store.CategoryNames
	Attrs       map[string]any    `json:"attrs,omitempty"`       // 过敏原 / 辣度 / 宗教
	SortOrder   int               `json:"sort_order"`            // 展示顺序（等于导入时的切片下标）
	SoldOut     bool              `json:"sold_out"`              // 是否售罄
	Remaining   *int              `json:"remaining,omitempty"`   // 每日剩余（唯一动态字段）
}

// DraftItem 原文确认稿里的一道菜（原始语言）。
type DraftItem struct {
	Name     string   `json:"name"`               // 菜品原名（原文语言）
	Price    *float64 `json:"price,omitempty"`    // 价格（OCR 可能识别不到，故可选）
	Category string   `json:"category,omitempty"` // 分类（原文）
	Selected bool     `json:"selected"`           // P3 勾选「要翻译的菜」
}

// SourceDraft 原文确认稿（唯一真值；未确认不允许翻译）。
//
// ConfirmedAt 为 nil 表示尚未确认，上层必须据此拦住翻译入口（硬规则）。
type SourceDraft struct {
	ID          string      `json:"id"`                     // 草稿 ID
	StoreID     string      `json:"store_id"`               // 所属门店
	Lang        string      `json:"lang"`                   // 原文语种，如 zh
	Items       []DraftItem `json:"items"`                  // 菜品清单（原始语言）
	ConfirmedAt *time.Time  `json:"confirmed_at,omitempty"` // 确认时间；nil 表示未确认
	CreatedAt   time.Time   `json:"created_at"`             // 创建时间（取最新草稿的依据）
}

// Rating 评价（三维度：菜品 / 服务 / 会再来吗）。
type Rating struct {
	ID            string    `json:"id"`                       // 评价 ID
	StoreID       string    `json:"store_id"`                 // 所属门店
	Ref           string    `json:"ref,omitempty"`            // 入口来源：t07 / door / coupon
	ScoreDish     int       `json:"score_dish"`               // 菜品评分（1-5）
	ScoreService  int       `json:"score_service"`            // 服务评分（1-5）
	ScoreReturn   int       `json:"score_return"`             // 会再来吗（1-5，复购意愿）
	CommentNative string    `json:"comment_native,omitempty"` // 客人原话（其母语）
	CommentZh     string    `json:"comment_zh,omitempty"`     // 原话的中文译文
	CreatedAt     time.Time `json:"created_at"`               // 提交时间
}

// CouponTemplate 券模板（注册后自动配好；发券免费）。
type CouponTemplate struct {
	ID        string         `json:"id"`         // 模板 ID
	StoreID   string         `json:"store_id"`   // 所属门店
	Content   map[string]any `json:"content"`    // 券内容（多语，如 zh/en 文案与 type）
	ValidDays int            `json:"valid_days"` // 有效期天数（默认 14）
	Enabled   bool           `json:"enabled"`    // 是否启用
	CreatedAt time.Time      `json:"created_at"` // 创建时间（取最新模板的依据）
}

// Coupon 券实例（截图保存，到店核销）。
type Coupon struct {
	ID         string     `json:"id"`                    // 券 ID
	TemplateID string     `json:"template_id"`           // 来源模板
	StoreID    string     `json:"store_id"`              // 归属门店（核销前必须比对，防跨店核销）
	Code       string     `json:"code"`                  // 券码（8 位数字，便于人工念读）
	Lang       string     `json:"lang,omitempty"`        // 客人语言，用于展示对应译文
	IssuedAt   time.Time  `json:"issued_at"`             // 发放时间
	ExpireAt   time.Time  `json:"expire_at"`             // 到期时间
	Status     string     `json:"status"`                // issued / redeemed / expired
	RedeemedAt *time.Time `json:"redeemed_at,omitempty"` // 核销时间；nil 表示未核销
}
