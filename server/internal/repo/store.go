// Package repo 的数据访问实现之一：门店（store）。
//
// 门店是租户边界：菜品、原文稿、评价、券、埋点都以 store_id 归属。客人端
// 扫门口码 / 桌码后按 slug 打开菜单页 /s/{slug}。slug 印在二维码与桌贴上，
// 一旦生成即不可变（改了旧码就扫不开），因此本包只提供创建，没有「改 slug」。
//
// 分类名多语化（category_names）与菜品多语名同构，见 UpdateStoreCategoryNames。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// storeCols 是 store 表的固定列清单，供各查询 SELECT 复用，避免列序与 Scan 不一致。
//
// 可空列统一用 COALESCE(...,”) 折叠成空串，好处是能直接 Scan 进 string，
// 调用方无需处理 sql.NullString。列序必须与 scanStore 的 Scan 参数严格对应。
const storeCols = `id, account_id, tenant_id, COALESCE(org_id,''), name, COALESCE(addr,''), geo_lat, geo_lng,
	COALESCE(wifi_ssid,''), COALESCE(wifi_pwd,''), COALESCE(business_hours,''),
	coupon_enabled, service_tier, order_mode, slug, category_names, created_at`

// CreateStore 新建门店。slug 冲突时自动重试。
//
// slug 由调用方生成（通常 idgen.Slug()）。ON CONFLICT (slug) DO NOTHING 配合
// RETURNING：冲突时没有行返回，scanStore 会得到 ErrNotFound，于是换一个新 slug
// 重试，最多 5 次；这样把「随机短码撞车」这种低概率事件在存储层就地化解，
// 调用方无需关心。重试耗尽仍失败才返回错误。
//
// tenantID 由创建者账号的租户决定（上层传入），门店一旦建立即固定归属，
// 这是数据隔离的根基：之后菜品 / 评价 / 券都随门店落在这个租户里。
//
// ★ org_id 用空串表示「未分组」而不是 NULL：迁移里该列是 NOT NULL DEFAULT ”，
// 与 org.parent_id 的空串约定一致（空串 = 挂父节点下）。写 NULLIF 会直接
// 违反 NOT NULL 约束——这类错只在运行时炸，且报错信息与根因离得很远。
func (r *Repo) CreateStore(ctx context.Context, accountID, tenantID, orgID, name, slug string) (*domain.Store, error) {
	for attempt := 0; attempt < 5; attempt++ {
		row := r.DB.QueryRowContext(ctx,
			`INSERT INTO store(id, account_id, tenant_id, org_id, name, slug)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (slug) DO NOTHING
			 RETURNING `+storeCols,
			idgen.New(), accountID, tenantID, orgID, name, slug)

		s, err := scanStore(row)
		if err == nil {
			return s, nil
		}
		if errors.Is(err, ErrNotFound) { // slug 撞了，换一个再来
			slug = idgen.Slug()
			continue
		}
		return nil, fmt.Errorf("create store: %w", err)
	}
	return nil, fmt.Errorf("create store: slug collision retries exhausted")
}

// GetStoreBySlug 按 slug 查门店（客人端入口）。
//
// 外国客人扫桌码 / 门口码后，后端据码里的 slug 定位门店并渲染菜单页；
// 这是公开接口，不要求登录。不存在返回 ErrNotFound。
func (r *Repo) GetStoreBySlug(ctx context.Context, slug string) (*domain.Store, error) {
	return scanStore(r.DB.QueryRowContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE slug = $1`, slug))
}

// GetStoreByID 按 ID 查门店。
// 商家端在鉴权中间件里由 account_id + store_id 定位当前门店，是最常用的读路径。
func (r *Repo) GetStoreByID(ctx context.Context, id string) (*domain.Store, error) {
	return scanStore(r.DB.QueryRowContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE id = $1`, id))
}

// ListStoresByAccount 列出某账号名下全部门店。一个账号可开多店，故返回切片。
// 按 created_at 升序，保证首页门店列表顺序稳定（先建的在前）。
func (r *Repo) ListStoresByAccount(ctx context.Context, accountID string) ([]domain.Store, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE account_id = $1 ORDER BY created_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ListStoresForViewer 列出「这个账号可见的门店」。
//
// 可见性规则（与 httpapi.requireStore 一一对应，两处必须同步改）：
//   - super_admin：看默认租户（平台侧自己运营）的门店；跨租户查看走 ListStoresByTenant；
//   - tenant_admin：看本租户全部门店（tenant_id 命中，不管挂谁名下）；
//   - org_admin：看本组织及其全部子组织下的门店（品牌 / 区域作用域）；
//   - manager / staff：只看自己名下的店（account_id 命中）。
func (r *Repo) ListStoresForViewer(ctx context.Context, acc *domain.Account) ([]domain.Store, error) {
	switch acc.Role {
	case domain.RoleSuperAdmin:
		return r.listStoresWhere(ctx, `tenant_id = $1 ORDER BY created_at`, domain.DefaultTenantID)
	case domain.RoleTenantAdmin:
		return r.listStoresWhere(ctx, `tenant_id = $1 ORDER BY created_at`, acc.TenantID)
	case domain.RoleOrgAdmin:
		ids, err := r.OrgDescendantIDs(ctx, acc.TenantID, acc.OrgID)
		if err != nil {
			return nil, err
		}
		// 没分配到组织（或组织已被删）：作用域退化为「自己的店」，绝不放大到全集团
		if len(ids) == 0 {
			return r.ListStoresByAccount(ctx, acc.ID)
		}
		return r.listStoresInOrgs(ctx, ids)
	default: // manager / staff：只看自己的店
		return r.ListStoresByAccount(ctx, acc.ID)
	}
}

// CanAccessStore 判断某账号是否有权访问这家门店。
//
// ★ 这是商家端越权防线的唯一判据（httpapi.requireStore 调用它），
// 与 ListStoresForViewer 的规则严格一致：列表能看到什么，点进去就该能看什么。
// 两处若漂移，会出现「列表里没有、但接口能打开」的怪象——那既是 bug 也是信息泄露。
func (r *Repo) CanAccessStore(ctx context.Context, acc *domain.Account, st *domain.Store) bool {
	if st.AccountID == acc.ID {
		return true // owner 永远能碰自己的店
	}
	switch acc.Role {
	case domain.RoleSuperAdmin:
		return st.TenantID == domain.DefaultTenantID
	case domain.RoleTenantAdmin:
		return st.TenantID == acc.TenantID
	case domain.RoleOrgAdmin:
		if st.TenantID != acc.TenantID || acc.OrgID == "" {
			return false
		}
		ids, err := r.OrgDescendantIDs(ctx, acc.TenantID, acc.OrgID)
		if err != nil {
			return false // 查不出子树时按无权处理（拒绝优于放过）
		}
		for _, id := range ids {
			if id == st.OrgID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// listStoresWhere 按条件列门店（供 ListStoresForViewer 的三个分支复用）。
func (r *Repo) listStoresWhere(ctx context.Context, where string, arg any) ([]domain.Store, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// listStoresInOrgs 列出落在给定组织集合里的门店（品牌区域管理员的作用域）。
func (r *Repo) listStoresInOrgs(ctx context.Context, orgIDs []string) ([]domain.Store, error) {
	// 动态 IN 列表：组织数是「一品牌几家门店铺」，量级很小，
	// 用字符串拼占位符比引 ORM 更直白；参数全部走查询参数，无注入面。
	ph := make([]string, len(orgIDs))
	args := make([]any, len(orgIDs))
	for i, id := range orgIDs {
		ph[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE org_id IN (`+strings.Join(ph, ",")+`) ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// ListStoresByTenant 列出指定租户的全部门店（超管后台用；倒序取最近的）。
func (r *Repo) ListStoresByTenant(ctx context.Context, tenantID string, limit int) ([]domain.Store, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+storeCols+` FROM store WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`,
		tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.Store{}
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// CountStores 统计门店总数 / 指定租户门店数（tenantID 为空串时统计全平台）。
func (r *Repo) CountStores(ctx context.Context, tenantID string) (int, error) {
	var n int
	if tenantID == "" {
		err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM store`).Scan(&n)
		return n, err
	}
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM store WHERE tenant_id = $1`, tenantID).Scan(&n)
	return n, err
}

// StorePatch 是门店可更新字段（nil 表示不改）。
//
// 用指针而非值，是为了区分「字段缺省」与「显式置空」：nil 走 COALESCE 保持原值。
// 注意 slug 不在此列——它是物料短链，创建后不可变。
type StorePatch struct {
	Name          *string // 店名
	Addr          *string // 地址
	WifiSSID      *string // WiFi 名（注册后补填，可选）
	WifiPwd       *string // WiFi 密码
	BusinessHours *string // 营业时间
	CouponEnabled *bool   // 券开关，同时也是店型标签（是否发放复购券）
	ServiceTier   *string // 服务档位：pure 纯翻译档 / service 外客服务档
	OrderMode     *string // 点单模式：assist 辅助型 / replace 替代型
}

// UpdateStore 局部更新门店。
//
// 每条列都用 COALESCE($n, col)：传 nil 即保持原值，只有非 nil 字段被写入，
// 因此一次请求只改部分字段也不会把其它列清空。更新后回读整行返回，保证返回的
// 对象与库内真值一致（含数据库侧可能触发的默认值）。
func (r *Repo) UpdateStore(ctx context.Context, id string, p StorePatch) (*domain.Store, error) {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE store SET
		   name           = COALESCE($2, name),
		   addr           = COALESCE($3, addr),
		   wifi_ssid      = COALESCE($4, wifi_ssid),
		   wifi_pwd       = COALESCE($5, wifi_pwd),
		   business_hours = COALESCE($6, business_hours),
		   coupon_enabled = COALESCE($7, coupon_enabled),
		   service_tier   = COALESCE($8, service_tier),
		   order_mode     = COALESCE($9, order_mode)
		 WHERE id = $1`,
		id, p.Name, p.Addr, p.WifiSSID, p.WifiPwd, p.BusinessHours,
		p.CouponEnabled, p.ServiceTier, p.OrderMode)
	if err != nil {
		return nil, fmt.Errorf("update store: %w", err)
	}
	return r.GetStoreByID(ctx, id)
}

// UpdateStoreCategoryNames 整体写入门店的分类名多语种表（{lang: {原文: 译文}}）。
//
// 与菜品名同构：客人端切换语言时，分类标题也要一起翻，否则会出现「外语菜名 +
// 中文标题」的割裂页。分类是每店少量且稳定的词，故整体覆盖式写入（非增量）。
// 空 map 直接返回、不落库，避免把已翻译好的分类误清空。
func (r *Repo) UpdateStoreCategoryNames(ctx context.Context, id string, names map[string]map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("marshal category names: %w", err)
	}
	if _, err := r.DB.ExecContext(ctx,
		`UPDATE store SET category_names = $2 WHERE id = $1`, id, raw); err != nil {
		return fmt.Errorf("update category names: %w", err)
	}
	return nil
}

// scanStore 把单行结果映射成 domain.Store，并把 sql.ErrNoRows 归一为 ErrNotFound。
//
// 同时接收 *sql.Row（QueryRow）与 *sql.Rows（Query），故形参是 rowScanner 接口。
// category_names 是 JSONB，先读成字节再反序列化；解析失败按「留空」处理不报错，
// 因为分类名缺失只影响展示、不应阻塞门店读取。
func scanStore(sc rowScanner) (*domain.Store, error) {
	var (
		s       domain.Store
		catJSON []byte
	)
	if err := sc.Scan(&s.ID, &s.AccountID, &s.TenantID, &s.OrgID, &s.Name, &s.Addr, &s.GeoLat, &s.GeoLng,
		&s.WifiSSID, &s.WifiPwd, &s.BusinessHours,
		&s.CouponEnabled, &s.ServiceTier, &s.OrderMode, &s.Slug, &catJSON, &s.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(catJSON) > 0 {
		_ = json.Unmarshal(catJSON, &s.CategoryNames)
	}
	return &s, nil
}
