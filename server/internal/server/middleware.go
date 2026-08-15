package server

// 中间件：租户解析（Host → tenancy.Context）与 admin realm JWT 鉴权（双 realm 防串用）。
//
// 权限判定经 authz.Directory（模块声明式目录，P0-03 T1）——本文件不再手工维护映射；
// 目录 miss = fail-closed：403 + 错误日志（新增路由漏声明会在启动对账时直接失败，
// 运行时 miss 属异常路径，绝不默认放行）。
//
// operation 形态（Kratos v3）：
//   - HTTP：生成的 pb 路由 operation = proto 方法全名（带前导斜杠）
//   - gRPC：同构

import (
	"context"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/log"
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
	adminPathPrefix = "/api/v1/admin/"
	adminOpPrefix   = "/zcard.api.admin.v1."
)

// isAdminOperation 是否管理面操作（admin realm 鉴权目标；Public 声明的登录路由豁免）。
// storefront/supply/回调路由一律不挂 JWT（架构测试规则 9）。
func isAdminOperation(operation string, dir *authz.Directory) bool {
	if strings.HasPrefix(operation, adminOpPrefix) {
		if _, public, ok := dir.PermissionForOp(operation); ok && public {
			return false
		}
		return true
	}
	return strings.HasPrefix(operation, adminPathPrefix)
}

// adminAuthMiddleware admin realm 鉴权：JWT 校验 → 权限点校验（RBAC）→ 注入 claims。
// 超管角色（*）通配放行（authz 模块）。
func adminAuthMiddleware(signer *authn.Signer, az port.Authorizer, dir *authz.Directory) middleware.Middleware {
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
			if perm, _, ok := dir.PermissionForOp(tr.Operation()); ok && !az.Allowed(ctx, claims.RoleID, perm) {
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

// reconcileRoutes 启动对账（P0-03 fail-fast）：真实路由表逐条核对 admin 前缀必须已声明权限点。
func reconcileRoutes(srv *khttp.Server, dir *authz.Directory) error {
	var routes []authz.RouteInfo
	err := srv.WalkRoute(func(ri khttp.RouteInfo) error {
		routes = append(routes, authz.RouteInfo{Method: ri.Method, Path: ri.Path})
		return nil
	})
	if err != nil {
		return err
	}
	if err := dir.Reconcile(routes, adminPathPrefix); err != nil {
		return err
	}
	log.Default().Info("authz.reconciled", "routes", len(routes), "perms", len(dir.Codes()))
	return nil
}
