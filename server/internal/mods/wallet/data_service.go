package wallet

// 钱包 API（P1-05；storefront 余额/流水/充值 + admin 调账/流水）。

import (
	"context"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── Storefront ──

// StoreWalletService 顾客钱包服务。
type StoreWalletService struct {
	storefrontv1.UnimplementedStoreWalletServiceServer
	repo *WalletRepoImpl
	data *data.Data
}

// NewStoreWalletService 构造。
func NewStoreWalletService(repo *WalletRepoImpl, d *data.Data) *StoreWalletService {
	return &StoreWalletService{repo: repo, data: d}
}

// GetBalance 余额+积分。
func (s *StoreWalletService) GetBalance(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.BalanceReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	avail, locked, err := s.repo.GetBalance(ctx, claims.Subject)
	if err != nil {
		return nil, errors.InternalServer("wallet.BALANCE_FAILED", "查询余额失败")
	}
	return &storefrontv1.BalanceReply{
		AvailableCents: avail, LockedCents: locked, TotalCents: avail + locked,
	}, nil
}

// ListTransactions 流水。
func (s *StoreWalletService) ListTransactions(ctx context.Context, req *storefrontv1.ListTxRequest) (*storefrontv1.ListTxReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	page := int(req.GetPage())
	size := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	rows, _, err := s.repo.ListTransactions(ctx, claims.Subject, page, size)
	if err != nil {
		return nil, errors.InternalServer("wallet.TX_FAILED", "查询流水失败")
	}
	reply := &storefrontv1.ListTxReply{}
	for _, r := range rows {
		reply.Transactions = append(reply.Transactions, &storefrontv1.Tx{
			Id: r.ID, Direction: r.Direction, Type: r.Type,
			AmountCents: r.Amount, BalanceAfterCents: r.BalanceAfter,
			Reference: r.Reference, Remark: r.Remark,
		})
	}
	return reply, nil
}

// CreateRecharge 充值（M1a 框架——真实支付管线 M1b 接入）。
func (s *StoreWalletService) CreateRecharge(ctx context.Context, req *storefrontv1.CreateRechargeRequest) (*storefrontv1.CreateRechargeReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if req.GetAmountCents() <= 0 {
		return nil, errors.BadRequest("wallet.INVALID_AMOUNT", "充值金额必须大于 0")
	}

	// M1a：直接入账（M1b 接支付管线后走回调）
	ref := fmt.Sprintf("recharge:%d:%d", claims.Subject, req.GetAmountCents())
	if err := s.repo.CreditInTx(ctx, Entry{
		UserID: claims.Subject, Direction: "in", Type: "recharge",
		Amount: req.GetAmountCents(), Reference: ref,
	}); err != nil {
		return nil, errors.InternalServer("wallet.RECHARGE_FAILED", "充值失败")
	}
	return &storefrontv1.CreateRechargeReply{}, nil
}

// ── Admin ──

// AdminWalletService 钱包管理服务。
type AdminWalletService struct {
	adminv1.UnimplementedAdminWalletServiceServer
	repo *WalletRepoImpl
	data *data.Data
}

// NewAdminWalletService 构造。
func NewAdminWalletService(repo *WalletRepoImpl, d *data.Data) *AdminWalletService {
	return &AdminWalletService{repo: repo, data: d}
}

// GetBalance 指定用户余额。
func (s *AdminWalletService) GetBalance(ctx context.Context, req *adminv1.GetBalanceRequest) (*adminv1.Balance, error) {
	avail, locked, err := s.repo.GetBalance(ctx, req.GetUserId())
	if err != nil {
		return nil, errors.InternalServer("wallet.BALANCE_FAILED", "查询失败")
	}
	return &adminv1.Balance{
		UserId: req.GetUserId(), AvailableCents: avail, LockedCents: locked,
		TotalCents: avail + locked,
	}, nil
}

// Adjust 手动调账。
func (s *AdminWalletService) Adjust(ctx context.Context, req *adminv1.AdjustRequest) (*adminv1.Balance, error) {
	if req.GetReason() == "" {
		return nil, errors.BadRequest("wallet.REASON_REQUIRED", "调账原因必填")
	}
	claims := identity.ClaimsFromContext(ctx)
	var operatorID uint64
	if claims != nil {
		operatorID = claims.Subject
	}
	ref := fmt.Sprintf("adjust:%d:%d", req.GetUserId(), req.GetAmountCents())
	var err error
	if req.GetAmountCents() > 0 {
		err = s.repo.CreditInTx(ctx, Entry{
			UserID: req.GetUserId(), Direction: "in", Type: "adjust",
			Amount: req.GetAmountCents(), Reference: ref,
			OperatorID: operatorID, Remark: req.GetReason(),
		})
	} else if req.GetAmountCents() < 0 {
		err = s.repo.DebitInTx(ctx, Entry{
			UserID: req.GetUserId(), Direction: "out", Type: "adjust",
			Amount: -req.GetAmountCents(), Reference: ref,
			OperatorID: operatorID, Remark: req.GetReason(),
		})
	}
	if err != nil {
		return nil, errors.InternalServer("wallet.ADJUST_FAILED", "调账失败: "+err.Error())
	}
	// 返回新余额
	avail, locked, _ := s.repo.GetBalance(ctx, req.GetUserId())
	return &adminv1.Balance{
		UserId: req.GetUserId(), AvailableCents: avail, LockedCents: locked,
		TotalCents: avail + locked,
	}, nil
}

// ListTransactions 指定用户流水。
func (s *AdminWalletService) ListTransactions(ctx context.Context, req *adminv1.ListWalletTxRequest) (*adminv1.ListWalletTxReply, error) {
	page := int(req.GetPage())
	size := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	rows, total, err := s.repo.ListTransactions(ctx, req.GetUserId(), page, size)
	if err != nil {
		return nil, errors.InternalServer("wallet.TX_FAILED", "查询流水失败")
	}
	reply := &adminv1.ListWalletTxReply{Total: total}
	for _, r := range rows {
		reply.Transactions = append(reply.Transactions, &adminv1.WalletTx{
			Id: r.ID, Direction: r.Direction, Type: r.Type,
			AmountCents: r.Amount, BalanceBeforeCents: r.BalanceBefore,
			BalanceAfterCents: r.BalanceAfter, Reference: r.Reference, Remark: r.Remark,
		})
	}
	return reply, nil
}

var _ = order.StatusPaid // 保持引用
