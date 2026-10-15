// Package domain 定义核心领域模型（与数据库表一一对应，字段名对前端 JSON 友好）。
package domain

import "time"

// Account 商家主账号。注册只收手机号。
type Account struct {
	ID        string    `json:"id"`
	Phone     string    `json:"phone"`
	CreatedAt time.Time `json:"created_at"`
}

// Store 门店。slug 定死为 /s/{slug}，物料印刷后不可变。
type Store struct {
	ID            string   `json:"id"`
	AccountID     string   `json:"account_id"`
	Name          string   `json:"name"`
	Addr          string   `json:"addr,omitempty"`
	GeoLat        *float64 `json:"geo_lat,omitempty"`
	GeoLng        *float64 `json:"geo_lng,omitempty"`
	WifiSSID      string   `json:"wifi_ssid,omitempty"`
	WifiPwd       string   `json:"wifi_pwd,omitempty"`
	BusinessHours string   `json:"business_hours,omitempty"`
	CouponEnabled bool     `json:"coupon_enabled"`
	ServiceTier   string   `json:"service_tier"` // pure 纯翻译档 / service 外客服务档
	OrderMode     string   `json:"order_mode"`   // assist 辅助型 / replace 替代型
	Slug          string   `json:"slug"`
	// CategoryNames 是分类名的多语种表：{lang: {原文分类: 译文}}。
	// 与 Dish.Names 同构，保证客人端「分类标题 + 菜名」整体切换到目标语言。
	CategoryNames map[string]map[string]string `json:"category_names,omitempty"`
	CreatedAt     time.Time                    `json:"created_at"`
}

// Dish 菜品（多语化后的成品）。
type Dish struct {
	ID          string            `json:"id"`
	StoreID     string            `json:"store_id"`
	NameZh      string            `json:"name_zh"`
	NameEn      string            `json:"name_en,omitempty"`
	Names       map[string]string `json:"names,omitempty"` // 多语种菜名，如 {"en":"...","ja":"..."}
	Description string            `json:"description,omitempty"`
	Price       *float64          `json:"price,omitempty"`
	Category    string            `json:"category,omitempty"`
	Attrs       map[string]any    `json:"attrs,omitempty"` // 过敏原 / 辣度 / 宗教
	SortOrder   int               `json:"sort_order"`
	SoldOut     bool              `json:"sold_out"`
	Remaining   *int              `json:"remaining,omitempty"` // 每日剩余（唯一动态字段）
}

// DraftItem 原文确认稿里的一道菜（原始语言）。
type DraftItem struct {
	Name     string   `json:"name"`
	Price    *float64 `json:"price,omitempty"`
	Category string   `json:"category,omitempty"`
	Selected bool     `json:"selected"` // P3 勾选「要翻译的菜」
}

// SourceDraft 原文确认稿（唯一真值；未确认不允许翻译）。
type SourceDraft struct {
	ID          string      `json:"id"`
	StoreID     string      `json:"store_id"`
	Lang        string      `json:"lang"`
	Items       []DraftItem `json:"items"`
	ConfirmedAt *time.Time  `json:"confirmed_at,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
}

// Rating 评价（三维度：菜品 / 服务 / 会再来吗）。
type Rating struct {
	ID            string    `json:"id"`
	StoreID       string    `json:"store_id"`
	Ref           string    `json:"ref,omitempty"`
	ScoreDish     int       `json:"score_dish"`
	ScoreService  int       `json:"score_service"`
	ScoreReturn   int       `json:"score_return"`
	CommentNative string    `json:"comment_native,omitempty"`
	CommentZh     string    `json:"comment_zh,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// CouponTemplate 券模板（注册后自动配好；发券免费）。
type CouponTemplate struct {
	ID        string         `json:"id"`
	StoreID   string         `json:"store_id"`
	Content   map[string]any `json:"content"`
	ValidDays int            `json:"valid_days"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
}

// Coupon 券实例（截图保存，到店核销）。
type Coupon struct {
	ID         string     `json:"id"`
	TemplateID string     `json:"template_id"`
	StoreID    string     `json:"store_id"`
	Code       string     `json:"code"`
	Lang       string     `json:"lang,omitempty"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpireAt   time.Time  `json:"expire_at"`
	Status     string     `json:"status"` // issued / redeemed / expired
	RedeemedAt *time.Time `json:"redeemed_at,omitempty"`
}
