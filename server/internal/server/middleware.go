package server

// 中间件：租户解析（Host → tenancy.Context）与 admin realm JWT 鉴权（双 realm 防串用）。
//
// operation 形态（Kratos v3）：
//   - HTTP：路径模板（如 /api/v1/admin/settings/{group}/{key}）
//   - gRPC：proto 方法全名（如 zcard.api.admin.v1.AdminAuthService/Login）

import (
	"context"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// tenantMiddleware 域名解析中间件（§6.5）：Host 匹配主站域名 → 主站上下文；
// 分站域名走 reseller_sites 验证（M3 交付，当前 fail-open 到主站并留痕 Host）。
// Ent interceptor（读写自动注入 subsite_id）M1 交付后在此上下文之上生效。
func tenantMiddleware(mainDomain string) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tc := tenancy.Context{SubsiteID: tenancy.MainSubsiteID, IsMain: true}
			if tr, ok := transport.FromServerContext(ctx); ok {
				tc.Host = hostOf(tr)
				// M3：reseller_sites 已验证域名 → SubsiteID/IsMain=false（DNS/文件验证后生效）
			}
			_ = mainDomain
			return handler(tenancy.WithContext(ctx, tc), req)
		}
	}
}

func hostOf(tr transport.Transporter) string {
	if hc, ok := tr.(khttp.Context); ok {
		h := hc.Request().Host
		if h == "" {
			h = hc.Header().Get("X-Forwarded-Host")
		}
		return h
	}
	return ""
}

const (
	// 手工注册路由的路径前缀（如 M1 支付回调白名单管理）
	adminPathPrefix = "/api/v1/admin/"
	// 生成的 pb 路由 operation = proto 方法全名（带前导斜杠），HTTP 与 gRPC 同构
	adminOpPrefix = "/zcard.api.admin.v1."
	adminLoginOp  = "/zcard.api.admin.v1.AdminAuthService/Login"
)

// isAdminOperation 是否管理面操作（admin realm 鉴权目标；登录路由豁免）。
// storefront/supply/回调路由一律不挂 JWT（架构测试规则 9）。
func isAdminOperation(operation string) bool {
	if strings.HasPrefix(operation, adminOpPrefix) {
		return operation != adminLoginOp
	}
	return strings.HasPrefix(operation, adminPathPrefix) && operation != "/api/v1/admin/auth/login"
}

// 权限映射（M1 起由启动时从 Kratos 路由表自动生成权限目录，§5.14；
// 当前手工维护规则，rbac_coverage_test M1 门禁）。
var permissionRules = []struct {
	prefix string // operation 前缀（proto 服务全名）
	method string // 具体方法（空 = 该服务全部）
	perm   string // 所需权限点
}{
	{prefix: adminOpPrefix + "AdminAuthService/", method: "GetProfile", perm: "auth:profile"},
	{prefix: adminOpPrefix + "AdminSettingsService/", method: "GetSetting", perm: settings.PermRead},
	{prefix: adminOpPrefix + "AdminSettingsService/", method: "ListSettings", perm: settings.PermRead},
	{prefix: adminOpPrefix + "AdminSettingsService/", method: "UpdateSetting", perm: settings.PermUpdate},
}

// permissionFor 按 operation（proto 方法全名）推导权限点。
func permissionFor(operation string) string {
	svc, method, ok := strings.Cut(operation, "/")
	if !ok {
		return ""
	}
	for _, r := range permissionRules {
		if strings.HasPrefix(operation, r.prefix) && (r.method == "" || r.method == method) {
			return r.perm
		}
	}
	_ = svc
	return ""
}

// adminAuthMiddleware admin realm 鉴权：JWT 校验 → 权限点校验（RBAC）→ 注入 claims。
// 超管角色（*）通配放行（authz 模块）。
func adminAuthMiddleware(signer *authn.Signer, az port.Authorizer) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return nil, errors.Unauthorized("identity.UNAUTHORIZED", "缺少 transport 上下文")
			}
			token := bearerToken(tr.RequestHeader().Get("Authorization"))
			if token == "" {
				return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
			}
			claims, err := signer.Verify(authn.RealmAdmin, token)
			if err != nil {
				return nil, errors.Unauthorized("identity.UNAUTHORIZED", "令牌无效或已过期")
			}
			if perm := permissionFor(tr.Operation()); perm != "" && !az.Allowed(ctx, claims.RoleID, perm) {
				return nil, errors.Forbidden("authz.PERMISSION_DENIED", "权限不足")
			}
			return handler(identity.WithClaims(ctx, claims), req)
		}
	}
}

func bearerToken(header string) string {
	if v, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
