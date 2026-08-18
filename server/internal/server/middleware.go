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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	identityport "github.com/NovaWorks/zcard-next/server/internal/mods/identity/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// DomainResolver 域名→租户解析端口（reseller 模块实现；server 不依赖业务实现）。
type DomainResolver interface {
	ResolveDomain(ctx context.Context, host string) (subsiteID uint64, siteName string, err error)
}

// domainCache 域名解析缓存（30s——reseller_sites 变更窗口）。
type domainCache struct {
	mu  sync.RWMutex
	m   map[string]domainCacheEntry
	ttl time.Duration
}
type domainCacheEntry struct {
	subsiteID uint64
	siteName  string
	at        time.Time
}

var dcache = &domainCache{m: map[string]domainCacheEntry{}, ttl: 30 * time.Second}

func (c *domainCache) get(host string) (uint64, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[host]
	if !ok || time.Since(e.at) > c.ttl {
		return 0, "", false
	}
	return e.subsiteID, e.siteName, true
}

func (c *domainCache) put(host string, subsiteID uint64, siteName string) {
	c.mu.Lock()
	c.m[host] = domainCacheEntry{subsiteID: subsiteID, siteName: siteName, at: time.Now()}
	c.mu.Unlock()
}

// tenantFilter 域名解析过滤器（§6.5）。
// http.Filter 层实现（Kratos 中间件的 Transporter 是内部 *khttp.Transport——
// 拿不到 Host 字段，恒主站；与 audit/supplier 同款 Filter 模式）。
// Host → verified 分站域名 → 分站上下文；未匹配/未验证 → 主站兜底（fail-open 绝不 5xx）。
// req.WithContext 后续中间件与业务 handler 自动继承租户上下文。
func tenantFilter(mainDomain string, resolver DomainResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tc := tenancy.Context{SubsiteID: tenancy.MainSubsiteID, IsMain: true, Host: r.Host}
			if resolver != nil && r.Host != "" && r.Host != mainDomain {
				host := strings.Split(r.Host, ":")[0]
				if id, _, cached := dcache.get(host); cached {
					tc.SubsiteID, tc.IsMain = id, id == tenancy.MainSubsiteID
				} else if id, _, err := resolver.ResolveDomain(r.Context(), host); err == nil && id > 0 {
					dcache.put(host, id, "")
					tc.SubsiteID, tc.IsMain = id, false
				} else {
					dcache.put(host, 0, "") // 负缓存（主站）30s
				}
			}
			next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), tc)))
		})
	}
}

// userAuthMiddleware storefront 用户 realm JWT（storefront 面需要登录态的端点：
// wallet/ticket/affiliate/user.me）。解析失败放行（claims 为 nil，业务端自行 401——
// 避免游客可访问的列表端点被一刀切）。
func userAuthMiddleware(signer *authn.Signer) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				token := bearerToken(tr.RequestHeader().Get("Authorization"))
				if token != "" {
					if claims, err := signer.Verify(authn.RealmUser, token); err == nil {
						ctx = identity.WithClaims(ctx, claims)
					}
				}
			}
			return handler(ctx, req)
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

// adminAuthMiddleware admin realm 鉴权：JWT 校验 → 账户实时状态（enabled + 当前
// RoleID，禁用/换角色即时生效）→ 权限点校验（RBAC）→ 注入 claims。
// 超管角色（*）通配放行（authz 模块）。
//
// 每请求按主键回查 admin_users 一次（管理面低频、PK get 代价可忽略）——JWT 内的
// RoleID 是签发时快照，换角色后旧 token 若沿用快照将保留旧权限直至过期，此处
// 以库中当前值为准（fail-closed：账户不存在/查询失败一律 401）。
func adminAuthMiddleware(signer *authn.Signer, az port.Authorizer, dir *authz.Directory, admins identityport.AdminReader) middleware.Middleware {
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
			acc, err := admins.Admin(ctx, claims.Subject)
			if err != nil || acc == nil {
				return nil, errors.Unauthorized("identity.UNAUTHORIZED", "账户不存在")
			}
			if !acc.Enabled {
				return nil, errors.Unauthorized("identity.ADMIN_DISABLED", "账号已禁用")
			}
			claims.RoleID = acc.RoleID
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
