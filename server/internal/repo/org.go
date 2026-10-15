// Package repo 的数据访问实现之一：组织（org）树。
//
// 对应翻译助手的 orgs 表，按餐饮业态裁剪为「集团根 → 品牌 → 区域」三层。
// 存在的唯一理由：品牌区域管理员（org_admin）的作用域是「我这个组织以及它下面
// 所有组织」，没有树就表达不了「下面所有」。
//
// 子树查询用递归 CTE 一次算完（OrgDescendantIDs / OrgAncestorIDs），
// 不在应用里循环查库——树的深度不可控，循环查会把 N+1 做成 N²。
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/domain"
	"github.com/francisfernandez66666/langcross-merchant-app/server/internal/idgen"
)

// orgCols 是 org 表的固定列清单（时间转 Unix 秒）。
const orgCols = `id, tenant_id, parent_id, name, type, extract(epoch FROM created_at)::bigint`

// EnsureRootOrg 确保某租户的根组织存在（幂等），返回根组织。
// 每个租户有且只有一个 root：账号没分配到具体品牌时，作用域就是「整个集团」。
func (r *Repo) EnsureRootOrg(ctx context.Context, tenantID, name string) (*domain.Org, error) {
	var o domain.Org
	err := r.DB.QueryRowContext(ctx,
		`SELECT `+orgCols+` FROM org WHERE tenant_id = $1 AND type = 'root' LIMIT 1`, tenantID).
		Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt)
	if err == nil {
		return &o, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	o = domain.Org{ID: idgen.New(), TenantID: tenantID, Name: name, Type: domain.OrgTypeRoot}
	err = r.DB.QueryRowContext(ctx,
		`INSERT INTO org(id, tenant_id, parent_id, name, type) VALUES ($1,$2,'',$3,$4)
		 RETURNING `+orgCols, o.ID, o.TenantID, o.Name, o.Type).
		Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create root org: %w", err)
	}
	return &o, nil
}

// CreateOrg 新建品牌 / 区域组织（parentID 为空时挂到本租户根组织下）。
func (r *Repo) CreateOrg(ctx context.Context, tenantID, parentID, name, orgType string) (*domain.Org, error) {
	if parentID == "" {
		root, err := r.EnsureRootOrg(ctx, tenantID, "集团根")
		if err != nil {
			return nil, err
		}
		parentID = root.ID
	}
	o := &domain.Org{ID: idgen.New(), TenantID: tenantID, ParentID: parentID,
		Name: name, Type: orgType}
	err := r.DB.QueryRowContext(ctx,
		`INSERT INTO org(id, tenant_id, parent_id, name, type) VALUES ($1,$2,$3,$4,$5)
		 RETURNING `+orgCols, o.ID, o.TenantID, o.ParentID, o.Name, o.Type).
		Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create org: %w", err)
	}
	return o, nil
}

// ListOrgs 列出某租户的全部组织（后台组织树；应用层自行组装父子关系）。
func (r *Repo) ListOrgs(ctx context.Context, tenantID string) ([]domain.Org, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+orgCols+` FROM org WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrgs(rows)
}

// GetOrg 按 ID 查组织；不存在返回 ErrNotFound。
func (r *Repo) GetOrg(ctx context.Context, id string) (*domain.Org, error) {
	var o domain.Org
	err := r.DB.QueryRowContext(ctx, `SELECT `+orgCols+` FROM org WHERE id = $1`, id).
		Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// RenameOrg 改组织名（根组织改名会同步租户展示名的场景由上层另做）。
func (r *Repo) RenameOrg(ctx context.Context, id, name string) error {
	res, err := r.DB.ExecContext(ctx, `UPDATE org SET name = $2 WHERE id = $1`, id, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MoveOrg 移动组织的父节点（换品牌 / 换区域）。
// parentID 为空表示移到本租户根组织下。
func (r *Repo) MoveOrg(ctx context.Context, tenantID, id, parentID string) error {
	if parentID == "" {
		root, err := r.EnsureRootOrg(ctx, tenantID, "集团根")
		if err != nil {
			return err
		}
		parentID = root.ID
	}
	res, err := r.DB.ExecContext(ctx,
		`UPDATE org SET parent_id = $3 WHERE id = $1 AND tenant_id = $2`, id, tenantID, parentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOrg 删除组织（仅允许没有子组织且没有门店的空组织）。
// 上层必须先确认空，否则这里返回 ErrConflict。
func (r *Repo) DeleteOrg(ctx context.Context, tenantID, id string) error {
	var children, stores int
	if err := r.DB.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM org WHERE parent_id = $1),
		        (SELECT count(*) FROM store WHERE org_id = $1)`, id).Scan(&children, &stores); err != nil {
		return err
	}
	if children > 0 || stores > 0 {
		return ErrConflict
	}
	res, err := r.DB.ExecContext(ctx,
		`DELETE FROM org WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OrgDescendantIDs 返回某组织的全部后代 ID（含自身）。
// 这是 org_admin 作用域的判据：他能碰的店 = org_id 落在这个集合里的店。
func (r *Repo) OrgDescendantIDs(ctx context.Context, tenantID, orgID string) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `
		WITH RECURSIVE sub AS (
			SELECT id FROM org WHERE id = $1 AND tenant_id = $2
			UNION ALL
			SELECT o.id FROM org o JOIN sub s ON o.parent_id = s.id WHERE o.tenant_id = $2
		)
		SELECT id FROM sub`, orgID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// OrgAncestorIDs 返回某组织的全部祖先 ID（含自身）。
// 用于反向判断「这个账号的组织是不是那个门店组织的上级」。
func (r *Repo) OrgAncestorIDs(ctx context.Context, tenantID, orgID string) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `
		WITH RECURSIVE anc AS (
			SELECT id, parent_id FROM org WHERE id = $1 AND tenant_id = $2
			UNION ALL
			SELECT o.id, o.parent_id FROM org o JOIN anc a ON o.id = a.parent_id
			  WHERE o.tenant_id = $2
		)
		SELECT id FROM anc`, orgID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetOrgInTenant 按 ID 查组织，且必须属于该租户（防跨租户挂载）。
// 不存在或不属于该租户都返回 ErrNotFound。
func (r *Repo) GetOrgInTenant(ctx context.Context, tenantID, id string) (*domain.Org, error) {
	var o domain.Org
	err := r.DB.QueryRowContext(ctx,
		`SELECT `+orgCols+` FROM org WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// CountStoresInOrg 统计某组织下的门店数（后台组织树显示规模）。
// 只数直属，不含子组织——子组织的规模由它自己的节点显示，避免重复计数。
func (r *Repo) CountStoresInOrg(ctx context.Context, orgID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM store WHERE org_id = $1`, orgID).Scan(&n)
	return n, err
}

// CountOrgs 统计某租户的组织数（后台总览）。
func (r *Repo) CountOrgs(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM org WHERE tenant_id = $1`, tenantID).Scan(&n)
	return n, err
}

// scanOrgs 把多行结果映射成 org 切片。
func scanOrgs(rows interface {
	Next() bool
	Scan(dest ...any) error
}) ([]domain.Org, error) {
	out := []domain.Org{}
	for rows.Next() {
		var o domain.Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}
