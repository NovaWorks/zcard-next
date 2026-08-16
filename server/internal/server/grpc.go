package server

// gRPC Server（规划 D2：HTTP/gRPC 双协议免费获得；M4 聚合平台阶段某些模块
// 直接以 gRPC 互联，无需重写接口）。当前注册与 HTTP 相同的服务面。

import (
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"

	"github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/middleware/logging"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

// NewGRPCServer 构造 gRPC server（wire provider）。
func NewGRPCServer(
	c *conf.Server,
	authSvc *identity.AdminAuthService,
	settingsSvc *settings.AdminSettingsService,
	catalogSvc *catalog.StoreCatalogService,
	supplySvc *supply.SupplyService,
	roleSvc *authz.RoleService,
	adminSvc *authz.AdminUserService,
	confSvc *settings.StorefrontConfigService,
	currencySvc *settings.AdminCurrencyService,
	catalogAdminSvc *catalog.AdminCatalogService,
	invSvc *inventory.AdminInventoryService,
	orderAdminSvc *order.AdminOrderService,
	orderStoreSvc *order.StoreOrderService,
	payAdminSvc *payment.AdminPaymentService,
	payStoreSvc *payment.StorePaymentService,
	walletStoreSvc *wallet.StoreWalletService,
	walletAdminSvc *wallet.AdminWalletService,
	fulfillStoreSvc *fulfillment.StoreDeliveryService,
	fulfillAdminSvc *fulfillment.AdminFulfillmentService,
) *kgrpc.Server {
	var opts = []kgrpc.ServerOption{
		kgrpc.Middleware(
			recovery.Recovery(),
			logging.Server(log.Default()),
			validate.Validator(func(req any) error {
				if msg, ok := req.(proto.Message); ok {
					return fieldbehavior.ValidateRequiredFields(msg)
				}
				return nil
			}),
			// gRPC 侧 admin 鉴权 M1 接 metadata（当前管理面以 HTTP 为主）
		),
	}
	if c != nil && c.Grpc != nil {
		if c.Grpc.Network != "" {
			opts = append(opts, kgrpc.Network(c.Grpc.Network))
		}
		if c.Grpc.Addr != "" {
			opts = append(opts, kgrpc.Address(c.Grpc.Addr))
		}
		if c.Grpc.Timeout != nil {
			opts = append(opts, kgrpc.Timeout(c.Grpc.Timeout.AsDuration()))
		}
	}
	srv := kgrpc.NewServer(opts...)

	adminv1.RegisterAdminAuthServiceServer(srv, authSvc)
	adminv1.RegisterAdminSettingsServiceServer(srv, settingsSvc)
	adminv1.RegisterRoleServiceServer(srv, roleSvc)
	adminv1.RegisterAdminUserServiceServer(srv, adminSvc)
	adminv1.RegisterAdminCurrencyServiceServer(srv, currencySvc)
	storefrontv1.RegisterStorefrontConfigServiceServer(srv, confSvc)
	adminv1.RegisterAdminCatalogServiceServer(srv, catalogAdminSvc)
	adminv1.RegisterAdminInventoryServiceServer(srv, invSvc)
	adminv1.RegisterAdminOrderServiceServer(srv, orderAdminSvc)
	adminv1.RegisterAdminPaymentServiceServer(srv, payAdminSvc)
	storefrontv1.RegisterStoreOrderServiceServer(srv, orderStoreSvc)
	storefrontv1.RegisterStorePaymentServiceServer(srv, payStoreSvc)
	storefrontv1.RegisterStoreWalletServiceServer(srv, walletStoreSvc)
	adminv1.RegisterAdminWalletServiceServer(srv, walletAdminSvc)
	adminv1.RegisterAdminFulfillmentServiceServer(srv, fulfillAdminSvc)
	storefrontv1.RegisterStoreDeliveryServiceServer(srv, fulfillStoreSvc)
	storefrontv1.RegisterStoreCatalogServiceServer(srv, catalogSvc)
	supplyv1.RegisterSupplyServiceServer(srv, supplySvc)
	// reflection 已由 kratos v3 grpc server 内置（手动注册会 duplicate panic）
	return srv
}
