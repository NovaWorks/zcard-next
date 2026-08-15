// Package port 为 authz 模块对外契约（零依赖包，规划 §4.4）。
//
// 权限管理三要素（功能模块设计 §1.3）：功能权限（本包接口级 RBAC）+
// 数据权限（tenancy subsite_id 行级隔离，M1 interceptor）+ 前端权限（动态路由，
// 权限目录由启动时从 Kratos 路由表自动生成）。
package port

import "context"

// 内置角色编码（种子数据，后台不可删除）。
const (
	RoleSuperAdmin = "super_admin" // 超管：权限通配符 *
	RoleOperator   = "operator"    // 运营
	RoleSupport    = "support"     // 客服
)

// PermissionAll 超管通配符。
const PermissionAll = "*"

// Authorizer 鉴权窄接口（server 中间件与 identity 消费，通道 A）。
type Authorizer interface {
	// Allowed 判定角色是否拥有权限点；super_admin 的 * 通配全部放行。
	Allowed(ctx context.Context, roleID uint64, permission string) bool
	// PermissionsOf 角色的权限点清单（登录后下发给前端动态路由）。
	PermissionsOf(ctx context.Context, roleID uint64) ([]string, error)
	// RoleName 角色名（profile 展示）。
	RoleName(ctx context.Context, roleID uint64) string
}
