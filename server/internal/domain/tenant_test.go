package domain

import (
	"os"
	"strings"
	"testing"
)

// 四级角色的等级必须严格有序：这是 requireRole 门禁的唯一依据，
// 等级一旦相等或倒置，「租户管理员不能提权」这类防提权规则就会失效。
func TestRoleLevelOrdering(t *testing.T) {
	if RoleLevel(RoleSuperAdmin) <= RoleLevel(RoleTenantAdmin) ||
		RoleLevel(RoleTenantAdmin) <= RoleLevel(RoleManager) ||
		RoleLevel(RoleManager) <= RoleLevel(RoleStaff) {
		t.Fatalf("角色等级必须 4>3>2>1，实际 %d/%d/%d/%d",
			RoleLevel(RoleSuperAdmin), RoleLevel(RoleTenantAdmin),
			RoleLevel(RoleManager), RoleLevel(RoleStaff))
	}
	// 未知角色按最低级处理（拒绝优于放过）
	if RoleLevel("president") != 0 {
		t.Error("未登记角色应为 0 级")
	}
	if ValidRole("president") {
		t.Error("未登记角色不应通过校验")
	}
	for _, r := range []string{RoleSuperAdmin, RoleTenantAdmin, RoleManager, RoleStaff} {
		if !ValidRole(r) {
			t.Errorf("%s 应通过校验", r)
		}
	}
}

// 验证租户可用性判定只有 active 一种状态可用：disabled、expired、空串
// 都不可用，过期或被停用的租户不得继续被放行服务。
func TestTenantIsUsable(t *testing.T) {
	if !(&Tenant{Status: TenantActive}).IsUsable() {
		t.Error("active 应可用")
	}
	for _, s := range []string{TenantDisabled, TenantExpired, ""} {
		if (&Tenant{Status: s}).IsUsable() {
			t.Errorf("%q 不应可用", s)
		}
	}
}

// 验证翻译档位白名单只有 fast / pro：未登记的模式必须被拒，防止脏值
// 一路传到能言平台才失败。
func TestKeyModeValidation(t *testing.T) {
	if !ValidKeyMode(KeyModeFast) || !ValidKeyMode(KeyModePro) {
		t.Error("fast / pro 应合法")
	}
	if ValidKeyMode("ultra") {
		t.Error("未登记的模式应被拒")
	}
}

// ★ 与 migrations/0005_tenant_rbac.sql 的一致性。
// 迁移里用字面量 INSERT 默认租户，Go 侧用常量访问它；两处一旦漂移，
// 存量数据会被归到一个不存在的租户下，且不会报错——所以用测试钉死。
func TestDefaultTenantIDMatchesMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0005_tenant_rbac.sql")
	if err != nil {
		t.Fatalf("读迁移失败: %v", err)
	}
	needle := "VALUES ('" + DefaultTenantID + "', 'langcross'"
	if !strings.Contains(string(raw), needle) {
		t.Fatalf("迁移 0005 里找不到与 domain.DefaultTenantID 一致的默认租户\n期望片段: %s", needle)
	}
}
