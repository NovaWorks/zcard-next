package authz

// 权限点声明（P0-03 T1/T4）：每条 admin RPC 一条声明，Op 与 HTTP 注解同源。
// 新增管理路由必须在此（或对应模块）Declare——漏声明启动即失败（Reconcile fail-fast）。

func init() {
	Declare(
		// ── 认证（identity）──────────────────────────────
		Perm{Code: "auth:login", Desc: "管理员登录", Domain: "auth",
			Op: "zcard.api.admin.v1.AdminAuthService/Login", Method: "POST", Path: "/api/v1/admin/auth/login", Public: true},
		Perm{Code: "auth:profile", Desc: "查看当前身份", Domain: "auth",
			Op: "zcard.api.admin.v1.AdminAuthService/GetProfile", Method: "GET", Path: "/api/v1/admin/auth/profile"},
		Perm{Code: "auth:logout", Desc: "登出", Domain: "auth",
			Op: "zcard.api.admin.v1.AdminAuthService/Logout", Method: "POST", Path: "/api/v1/admin/auth/logout"},

		// ── 设置中心（settings）──────────────────────────
		Perm{Code: "settings:read", Desc: "查看设置", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminSettingsService/ListSettings", Method: "GET", Path: "/api/v1/admin/settings"},
		Perm{Code: "settings:read_detail", Desc: "查看设置项", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminSettingsService/GetSetting", Method: "GET", Path: "/api/v1/admin/settings/{group}/{key}"},
		Perm{Code: "settings:update", Desc: "修改设置", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminSettingsService/UpdateSetting", Method: "PUT", Path: "/api/v1/admin/settings/{group}/{key}"},

		// ── 权限管理（authz，本任务 T2 新增路由）──────────
		Perm{Code: "authz:role_read", Desc: "查看角色", Domain: "authz",
			Op: "zcard.api.admin.v1.RoleService/ListRoles", Method: "GET", Path: "/api/v1/admin/authz/roles"},
		Perm{Code: "authz:role_read_detail", Desc: "查看角色详情", Domain: "authz",
			Op: "zcard.api.admin.v1.RoleService/GetRole", Method: "GET", Path: "/api/v1/admin/authz/roles/{id}"},
		Perm{Code: "authz:role_write", Desc: "创建/修改角色（超管专属）", Domain: "authz", AdminOnly: true,
			Op: "zcard.api.admin.v1.RoleService/CreateRole", Method: "POST", Path: "/api/v1/admin/authz/roles"},
		Perm{Code: "authz:role_write", Desc: "创建/修改角色（超管专属）", Domain: "authz", AdminOnly: true,
			Op: "zcard.api.admin.v1.RoleService/UpdateRole", Method: "PUT", Path: "/api/v1/admin/authz/roles/{id}"},
		Perm{Code: "authz:role_delete", Desc: "删除角色（超管专属）", Domain: "authz", AdminOnly: true,
			Op: "zcard.api.admin.v1.RoleService/DeleteRole", Method: "DELETE", Path: "/api/v1/admin/authz/roles/{id}"},
		Perm{Code: "authz:role_grant", Desc: "角色权限勾选（超管专属）", Domain: "authz", AdminOnly: true,
			Op: "zcard.api.admin.v1.RoleService/UpdateRolePermissions", Method: "PUT", Path: "/api/v1/admin/authz/roles/{id}/permissions"},
		Perm{Code: "authz:tree", Desc: "查看权限目录", Domain: "authz",
			Op: "zcard.api.admin.v1.RoleService/GetPermissionTree", Method: "GET", Path: "/api/v1/admin/authz/permissions"},

		// ── 员工管理（identity 数据层 + authz API 面）─────
		Perm{Code: "identity:admin_read", Desc: "查看员工", Domain: "identity",
			Op: "zcard.api.admin.v1.AdminUserService/ListAdmins", Method: "GET", Path: "/api/v1/admin/admins"},
		Perm{Code: "identity:admin_write", Desc: "创建/修改员工（超管专属）", Domain: "identity", AdminOnly: true,
			Op: "zcard.api.admin.v1.AdminUserService/CreateAdmin", Method: "POST", Path: "/api/v1/admin/admins"},
		Perm{Code: "identity:admin_write", Desc: "创建/修改员工（超管专属）", Domain: "identity", AdminOnly: true,
			Op: "zcard.api.admin.v1.AdminUserService/UpdateAdmin", Method: "PUT", Path: "/api/v1/admin/admins/{id}"},
		Perm{Code: "identity:admin_toggle", Desc: "启停员工（超管专属）", Domain: "identity", AdminOnly: true,
			Op: "zcard.api.admin.v1.AdminUserService/ToggleAdmin", Method: "PUT", Path: "/api/v1/admin/admins/{id}/toggle"},

		// ── 货币管理（settings，P0-04 T3）──────────────
		Perm{Code: "settings:currency_read", Desc: "查看货币", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminCurrencyService/ListCurrencies", Method: "GET", Path: "/api/v1/admin/currencies"},
		Perm{Code: "settings:currency_write", Desc: "新增货币", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminCurrencyService/CreateCurrency", Method: "POST", Path: "/api/v1/admin/currencies"},
		Perm{Code: "settings:currency_write", Desc: "修改货币", Domain: "settings",
			Op: "zcard.api.admin.v1.AdminCurrencyService/UpdateCurrency", Method: "PUT", Path: "/api/v1/admin/currencies/{code}"},
		Perm{Code: "settings:currency_delete", Desc: "删除货币", Domain: "settings", AdminOnly: true,
			Op: "zcard.api.admin.v1.AdminCurrencyService/DeleteCurrency", Method: "DELETE", Path: "/api/v1/admin/currencies/{code}"},

		// ── 敏感权限点预登记（§5.20.4 防内部偷卡；路由 M1 落地）──
		Perm{Code: "card:view_content", Desc: "查看完整卡密（需二次确认+审计）", Domain: "inventory", AdminOnly: true},
		Perm{Code: "card:export", Desc: "导出卡密（审批+审计+限流）", Domain: "inventory", AdminOnly: true},
		Perm{Code: "order:view_delivery", Desc: "查看订单交付内容", Domain: "order", AdminOnly: true},
		Perm{Code: "order:refund", Desc: "订单退款（二次确认+审计）", Domain: "order", AdminOnly: true},
		Perm{Code: "system:update", Desc: "在线更新/密钥轮换", Domain: "system", AdminOnly: true},
	)
}
