package server

// HTTP Server：单进程承载 admin/storefront/supply 三面 API + 回调 + 健康检查。
// 中间件栈：recovery → logging → validate → tenant（全局）+ adminAuth（selector：
// 仅 /api/v1/admin/* 前缀；回调与供货路由绝不挂 JWT，架构测试规则 9）。

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/coupon"
	"github.com/NovaWorks/zcard-next/server/internal/mods/dashboard"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"

	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/selector"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

// Version 构建注入（-ldflags -X），health/ping 下发。
var Version = "dev"

// NewHTTPServer 构造 HTTP server（wire provider）。
func NewHTTPServer(
	c *conf.Server,
	d *data.Data,
	signer *authn.Signer,
	az port.Authorizer,
	authSvc *identity.AdminAuthService,
	settingsSvc *settings.AdminSettingsService,
	catalogSvc *catalog.StoreCatalogService,
	supplySvc *supply.SupplyService,
	supplyAdminSvc *supply.AdminSupplyService,
	roleSvc *authz.RoleService,
	adminSvc *authz.AdminUserService,
	confSvc *settings.StorefrontConfigService,
	currencySvc *settings.AdminCurrencyService,
	catalogAdminSvc *catalog.AdminCatalogService,
	memberLevelSvc *memberlevel.AdminMemberLevelService,
	couponSvc *coupon.AdminCouponService,
	dashboardSvc *dashboard.AdminDashboardService,
	invSvc *inventory.AdminInventoryService,
	orderAdminSvc *order.AdminOrderService,
	orderStoreSvc *order.StoreOrderService,
	payAdminSvc *payment.AdminPaymentService,
	payStoreSvc *payment.StorePaymentService,
	payRepo *payment.PaymentRepoImpl,
	walletStoreSvc *wallet.StoreWalletService,
	walletAdminSvc *wallet.AdminWalletService,
	fulfillStoreSvc *fulfillment.StoreDeliveryService,
	fulfillAdminSvc *fulfillment.AdminFulfillmentService,
	enq queue.Enqueuer,
	dir *authz.Directory,
) *khttp.Server {
	var opts = []khttp.ServerOption{
		khttp.Filter(corsFilter),
		khttp.Middleware(
			recovery.Recovery(),
			logging.Server(log.Default()),
			validate.Validator(func(req any) error {
				if msg, ok := req.(proto.Message); ok {
					return fieldbehavior.ValidateRequiredFields(msg)
				}
				return nil
			}),
			i18nMiddleware("zh_CN"),
			ensureInstalled(func() bool { return settings.Installed(context.Background(), d) }),
			tenantMiddleware(tenancyMainDomain(c)),
			// admin realm 鉴权仅挂管理面 operation；Public 声明（登录）经目录豁免；
			// storefront/supply/回调路由不挂 JWT（架构测试规则 9）。
			selector.Server(adminAuthMiddleware(signer, az, dir)).
				Match(func(_ context.Context, operation string) bool {
					return isAdminOperation(operation, dir)
				}).
				Build(),
		),
		khttp.Timeout(30 * time.Second),
	}
	if c != nil && c.Http != nil {
		if c.Http.Network != "" {
			opts = append(opts, khttp.Network(c.Http.Network))
		}
		if c.Http.Addr != "" {
			opts = append(opts, khttp.Address(c.Http.Addr))
		}
		if c.Http.Timeout != nil {
			opts = append(opts, khttp.Timeout(c.Http.Timeout.AsDuration()))
		}
	}
	srv := khttp.NewServer(opts...)

	// 业务路由（proto 注解生成；静态路由先于参数路由由注册顺序保证，铁律 4）
	adminv1.RegisterAdminAuthServiceHTTPServer(srv, authSvc)
	adminv1.RegisterAdminSettingsServiceHTTPServer(srv, settingsSvc)
	adminv1.RegisterRoleServiceHTTPServer(srv, roleSvc)
	adminv1.RegisterAdminUserServiceHTTPServer(srv, adminSvc)
	adminv1.RegisterAdminCurrencyServiceHTTPServer(srv, currencySvc)
	adminv1.RegisterAdminMemberLevelServiceHTTPServer(srv, memberLevelSvc)
	adminv1.RegisterAdminCouponServiceHTTPServer(srv, couponSvc)
	adminv1.RegisterAdminDashboardServiceHTTPServer(srv, dashboardSvc)
	storefrontv1.RegisterStorefrontConfigServiceHTTPServer(srv, confSvc)
	adminv1.RegisterAdminCatalogServiceHTTPServer(srv, catalogAdminSvc)
	adminv1.RegisterAdminInventoryServiceHTTPServer(srv, invSvc)
	adminv1.RegisterAdminOrderServiceHTTPServer(srv, orderAdminSvc)
	adminv1.RegisterAdminPaymentServiceHTTPServer(srv, payAdminSvc)
	storefrontv1.RegisterStoreOrderServiceHTTPServer(srv, orderStoreSvc)
	storefrontv1.RegisterStorePaymentServiceHTTPServer(srv, payStoreSvc)
	storefrontv1.RegisterStoreWalletServiceHTTPServer(srv, walletStoreSvc)
	adminv1.RegisterAdminWalletServiceHTTPServer(srv, walletAdminSvc)
	adminv1.RegisterAdminFulfillmentServiceHTTPServer(srv, fulfillAdminSvc)
	storefrontv1.RegisterStoreDeliveryServiceHTTPServer(srv, fulfillStoreSvc)
	storefrontv1.RegisterStoreCatalogServiceHTTPServer(srv, catalogSvc)
	supplyv1.RegisterSupplyServiceHTTPServer(srv, supplySvc)
	adminv1.RegisterAdminSupplyServiceHTTPServer(srv, supplyAdminSvc)

	// 保留路径（规划 §10.1：/api /uploads /health /payments /install 为保留前缀）
	registerHealth(srv, d, enq)

	// 启动对账（P0-03 fail-fast）：管理路由未声明权限点 → 拒绝启动
	if err := reconcileRoutes(srv, dir); err != nil {
		panic(err)
	}
	registerPaymentCallback(srv, payRepo, d)

	// TODO(M1b)：go:embed SPA（fullstack build tag；index.html 永不缓存，铁律 8）
	// TODO(M1)：/install 安装向导（EnsureInstalled 中间件先于业务路由）
	return srv
}

// registerHealth 健康检查（DB 连通 + 队列模式 + 版本；/metrics Prometheus M1 接入内网口）。
func registerHealth(srv *khttp.Server, d *data.Data, enq queue.Enqueuer) {
	srv.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := contextWithTimeout(r)
		defer cancel()
		dbOK := d.Ping(ctx) == nil
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  map[string]bool{"server": true, "database": dbOK},
			"queue":   map[string]any{"enabled": enq.Enabled()},
			"version": Version,
			"dialect": string(d.Dialect),
		})
	})
}

// registerPaymentCallback 支付回调（§5.5.3 统一入口——四重校验+幂等+markPaid）。
// 不挂 JWT（架构测试规则 9）；验签由渠道适配器完成（M1b 接真实渠道）。
func registerPaymentCallback(srv *khttp.Server, payRepo *payment.PaymentRepoImpl, d *data.Data) {
	payment.RegisterPaymentCallback(srv, payRepo, d)
}

func tenancyMainDomain(c *conf.Server) string { return "" } // 由 bootstrap 注入 Tenancy 配置后补齐

func contextWithTimeout(r *http.Request) (ctx context.Context, cancel context.CancelFunc) {
	return context.WithTimeout(r.Context(), 3*time.Second)
}

// corsFilter CORS 过滤器（开发阶段前后端联调；生产同源部署无此问题——M1b 按环境开关化）。
func corsFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,PATCH,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
