package domain

// 本文件：租户隔离与角色权限体系的领域模型。
//
// 设计来源：/Users/zhangzifei/Desktop/翻译助手 的 tenants + 四级角色模型，
// 按商家店端裁剪为「租户 → 账号 → 门店」三级（去掉组织树 / 部门 / 部门预算）。
// 对应迁移：migrations/0005_tenant_rbac.sql。

// DefaultTenantID 是平台默认租户 ID。
//
// ★ 与 migrations/0005_tenant_rbac.sql 里的 INSERT 字面量一一对应，
// 两处改动必须同步（本仓库用 TestDefaultTenantIDMatchesMigration 钉死）。
// 选 32 位 0 开头十六进制是为了与 idgen.New() 生成的主键同长度、一眼可辨为哨兵值。
const DefaultTenantID = "00000000000000000000000000000001"

// PlatformTenantID 是平台级（超管）账号的 tenant_id。
// 空串即「不属于任何租户」——超管管的是租户本身，不该被某个租户的边界框住。
const PlatformTenantID = ""

// 五级角色（等级 5 > 4 > 3 > 2 > 1，见 RoleLevel）。
//
// 与翻译助手（能言 SaaS）的四级角色一一对应，只是把「部门管理员」按餐饮业态
// 改名为「品牌 / 区域管理员」；等级数量从 4 增到 5，为门店管理员与店员分层：
//
//	翻译助手               本应用                等级   能干什么
//	────────────────       ────────────────      ────   ──────────────────────
//	super_admin            平台超管                5     租户 / 组织 / 角色 / 翻译 Key / AI 凭据
//	tenant_admin           集团管理员              4     本租户成员、本集团全部门店
//	dept_admin             品牌区域管理员          3     本组织（含子组织）门店
//	user                   门店管理员              2     自己名下的店（建店 / 菜单 / 券）
//	—                      店员                    1     核销券 / 看菜单 / 看评价
const (
	// RoleSuperAdmin 平台超管：租户 CRUD、跨租户只读、调整任意账号角色。
	// 数量必须极少，且每一次写操作都落 admin_audit。
	// ★ 必须同时满足 TenantID == PlatformTenantID（见 IsPlatformSuperAdmin）。
	RoleSuperAdmin = "super_admin"
	// RoleTenantAdmin 集团管理员：本租户账号管理、看本租户全部门店。
	RoleTenantAdmin = "tenant_admin"
	// RoleOrgAdmin 品牌 / 区域管理员：本组织（含子组织）门店。
	// 对应翻译助手的 dept_admin：管的是「一个品牌或一个区域」，不是整个集团。
	RoleOrgAdmin = "org_admin"
	// RoleManager 门店管理员：建店、菜单、翻译、券；只管自己名下的店。
	// 自助注册的商家默认就是这个角色——立刻可用，又不涉及集团级特权。
	RoleManager = "manager"
	// RoleStaff 店员：看菜单、核销券、看评价。不能建店、不能碰任何配置。
	RoleStaff = "staff"
)

// RoleLevel 把角色换算成可比较的等级；未知角色按 0 处理（拒绝优于放过）。
func RoleLevel(role string) int {
	switch role {
	case RoleSuperAdmin:
		return 5
	case RoleTenantAdmin:
		return 4
	case RoleOrgAdmin:
		return 3
	case RoleManager:
		return 2
	case RoleStaff:
		return 1
	default:
		return 0
	}
}

// RoleNameCN 角色中文名（后台展示与错误提示用）。
func RoleNameCN(role string) string {
	switch role {
	case RoleSuperAdmin:
		return "平台超管"
	case RoleTenantAdmin:
		return "集团管理员"
	case RoleOrgAdmin:
		return "品牌区域管理员"
	case RoleManager:
		return "门店管理员"
	case RoleStaff:
		return "店员"
	default:
		return "未知角色"
	}
}

// RoleNameByLevel 等级 → 角色中文名（requireRole 的报错文案用）。
// 传入不存在的等级时返回「更高权限」，避免把内部等级数字暴露给用户。
func RoleNameByLevel(level int) string {
	switch level {
	case 5:
		return "平台超管"
	case 4:
		return "集团管理员"
	case 3:
		return "品牌区域管理员"
	case 2:
		return "门店管理员"
	case 1:
		return "店员"
	default:
		return "更高权限"
	}
}

// IsPlatformSuperAdmin 判断是否为「平台级超管」。
//
// ★ 这条规则直接抄自翻译助手 2026-09-16 的安全整改（iam/auth.go 的 IsSuperAdmin）：
// 仅看角色等级不够——历史数据 / 旁路写入可能产生「role=super_admin 但 tenant_id 非空」
// 的行，那种账号会自动获得跨租户权能（退款、跨租户拉数据）。因此**双条件**：
// 角色等级达到最高，且账号不属于任何租户。
//
// 我们的落地方式：requireRole 在 required >= 最高等级时校验 TenantID，
// 让「租户里的超管」这条路径直接 403，从机制上杜绝旁路账号拿到平台权能。
func IsPlatformSuperAdmin(a *Account) bool {
	return a != nil && a.Role == RoleSuperAdmin && a.TenantID == PlatformTenantID
}

// MaxRoleLevel 是角色等级上限（requireRole 用它触发平台级双条件校验）。
const MaxRoleLevel = 5

// ValidRole 判断是否为登记在册的角色。
// 后台改角色时必须校验，否则一个拼写错误就把人提成了「不存在的高权限角色」。
func ValidRole(role string) bool {
	return RoleLevel(role) > 0
}

// 租户状态。
const (
	TenantActive   = "active"   // 正常服务
	TenantDisabled = "disabled" // 停用：不可新建业务，存量数据保留
	TenantExpired  = "expired"  // 到期未续
)

// Tenant 租户（隔离与计费的基本单元）。
type Tenant struct {
	ID        string         `json:"id"`                   // 租户 ID
	Code      string         `json:"code"`                 // 租户编码（对外短标识）
	Name      string         `json:"name"`                 // 租户显示名
	Status    string         `json:"status"`               // active / disabled / expired
	ExpiresAt *int64         `json:"expires_at,omitempty"` // 到期时间（Unix 秒）；NULL = 永久
	Perms     map[string]any `json:"perms,omitempty"`      // 配额与开关（语种白名单 / 日字符上限等）
	CreatedAt int64          `json:"created_at"`           // 创建时间（Unix 秒）
	// Stores / Accounts 是后台列表页的附带上伴，不落库。
	Stores   int `json:"stores,omitempty"`   // 门店数
	Accounts int `json:"accounts,omitempty"` // 账号数
}

// IsUsable 判断租户当前是否可用于开展业务（停用与过期都不可用）。
func (t *Tenant) IsUsable() bool { return t.Status == TenantActive }

// Org 组织（品牌 / 区域），构成「集团根 → 品牌 → 区域」的树。
//
// 对应翻译助手的 orgs 表，但裁剪为餐饮形态：root 是集团根（每个租户一个），
// 其下挂品牌，品牌下可再挂区域（如「华东区」）。门店必经某个组织落地，
// 品牌区域管理员的作用域就是「自己这个组织 + 它的所有子组织」。
type Org struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	ParentID  string `json:"parent_id,omitempty"` // 空串 = 挂本租户 root 下
	Name      string `json:"name"`
	Type      string `json:"type"` // root / brand / region
	CreatedAt int64  `json:"created_at"`
	// Stores / Children 是后台组织树的可视化附伴，不落库。
	Stores   int   `json:"stores,omitempty"`
	Children []Org `json:"children,omitempty"`
}

// 组织类型常量。
const (
	OrgTypeRoot   = "root"   // 集团根（每个租户一个，租户创建时自动建）
	OrgTypeBrand  = "brand"  // 品牌
	OrgTypeRegion = "region" // 区域（品牌下的分区）
)

// ValidOrgType 判断组织类型是否登记在册。
func ValidOrgType(t string) bool {
	return t == OrgTypeRoot || t == OrgTypeBrand || t == OrgTypeRegion
}

// TranslationKey 租户级翻译 API Key（后台维护的对象）。
type TranslationKey struct {
	ID         string `json:"id"`                     // Key ID
	TenantID   string `json:"tenant_id"`              // 所属租户
	Name       string `json:"name"`                   // Key 别名（便于人识别）
	Provider   string `json:"provider"`               // 提供方：lexicorn
	KeyMasked  string `json:"key_masked"`             // 脱敏展示（如 lx-ab…wxyz）
	Mode       string `json:"mode"`                   // fast / pro
	Status     string `json:"status"`                 // active / disabled
	DailyLimit int    `json:"daily_limit"`            // 当日调用上限；0 = 不限
	LastUsedAt *int64 `json:"last_used_at,omitempty"` // 最近一次成功使用时间（Unix 秒）
	CreatedBy  string `json:"created_by,omitempty"`   // 创建者账号 ID
	CreatedAt  int64  `json:"created_at"`             // 创建时间（Unix 秒）
	UpdatedAt  int64  `json:"updated_at"`             // 更新时间（Unix 秒）
}

// 提供方与模式常量。
const (
	KeyProviderLexicorn = "lexicorn" // 能言开放平台
	KeyModeFast         = "fast"     // 快速档：只走 AI，消耗低
	KeyModePro          = "pro"      // 专业档：知识库匹配 + 审校流水线，质量高
)

// ValidKeyMode 判断模式取值是否合法（与能言文档一致，只有 fast / pro）。
func ValidKeyMode(mode string) bool { return mode == KeyModeFast || mode == KeyModePro }

// KeyCredential 是解密后的可用凭据，只在内存中流转，不落库、不出现在任何响应里。
type KeyCredential struct {
	Key        string // 明文 API Key
	Mode       string // fast / pro
	DailyLimit int    // 当日调用上限；0 = 不限
	Name       string // Key 别名（日志 / 错误提示用）
}

// TranslationKeyStatus 状态常量（与租户状态分开：Key 停用不影响租户存续）。
const (
	KeyActive   = "active"
	KeyDisabled = "disabled"
)
