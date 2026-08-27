package wallet

// 钱包 API（P1-05；storefront 余额/流水/充值 + admin 调账/流水）。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	settingsport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	paymentport "github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ── Storefront ──

// StoreWalletService 顾客钱包服务。
type StoreWalletService struct {
	storefrontv1.UnimplementedStoreWalletServiceServer
	repo *WalletRepoImpl
	data *data.Data
	// settings 充值档位读取（铁律 16：客户端金额只作意向，档位由服务端裁决）
	settings settingsport.SettingsReader
	// payer 充值支付单创建（payment 端口，通道 A；nil = 支付管线未装配）
	payer paymentport.RechargePayer
	// giftcards 礼品卡兑换（P1-05 T4；nil = 未装配）
	giftcards *GiftcardRepo
	// commissions 佣金读取（提现校验；通道 A；nil = 跳过校验）
	commissions CommissionSource
}

// NewStoreWalletService 构造。
func NewStoreWalletService(repo *WalletRepoImpl, d *data.Data, settings settingsport.SettingsReader, payer paymentport.RechargePayer, giftcards *GiftcardRepo, commissions CommissionSource) *StoreWalletService {
	return &StoreWalletService{repo: repo, data: d, settings: settings, payer: payer, giftcards: giftcards, commissions: commissions}
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
	points, _ := s.repo.GetPoints(ctx, claims.Subject)
	return &storefrontv1.BalanceReply{
		AvailableCents: avail, LockedCents: locked, TotalCents: avail + locked,
		Points: points,
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

// CreateRecharge 充值（金额服务端档位裁决；支付确认前绝不入账——铁律 16）。
//
// 安全口径：客户端提交的 amount_cents 只是「意向」，服务端按 settings.recharge
// 档位（enabled/min_amount/max_amount）校验，赠送（gift_tiers）由服务端计算，
// 落 recharge_orders(pending)。余额入账只发生在支付回调成功后
// （payment.succeeded → wallet 订阅，reference=recharge:<paymentID>——M1b 支付
// 管线接线后完成）。抓包改金额只能落在档位区间内且不会直接入账。
func (s *StoreWalletService) CreateRecharge(ctx context.Context, req *storefrontv1.CreateRechargeRequest) (*storefrontv1.CreateRechargeReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	amount := req.GetAmountCents()
	minAmount, maxAmount, enabled := s.rechargePolicy(ctx)
	if !enabled {
		return nil, errors.Forbidden("wallet.RECHARGE_DISABLED", "充值功能未开放")
	}
	if !money.ValidCents(amount) || amount < minAmount || amount > maxAmount {
		return nil, errors.BadRequest("wallet.INVALID_AMOUNT", "充值金额超出允许范围")
	}
	giftAmount, giftPoints := s.giftFor(ctx, amount)
	ro, err := s.repo.CreateRechargeOrder(ctx, claims.Subject, amount, giftAmount, giftPoints)
	if err != nil {
		return nil, errors.InternalServer("wallet.RECHARGE_FAILED", "创建充值单失败")
	}
	// 创建支付单并发起渠道（充值单保持 pending；余额入账只发生在回调成功后）
	if s.payer == nil {
		return nil, errors.InternalServer("wallet.PAYMENT_UNBOUND", "支付管线未装配")
	}
	info, err := s.payer.CreateRechargePayment(ctx, ro.ID, req.GetChannel(), money.Cents(amount))
	if err != nil {
		return nil, mapRechargeErr(err)
	}
	return &storefrontv1.CreateRechargeReply{
		RechargeId: ro.ID, PaymentId: info.PaymentID,
		Type: info.Type, Payload: info.Payload,
	}, nil
}

// mapRechargeErr 充值支付发起错误映射（渠道不存在/停用/配置无效等）。
func mapRechargeErr(err error) error {
	msg := err.Error()
	switch {
	case containsStr(msg, "CHANNEL_NOT_FOUND"):
		return errors.NotFound("wallet.CHANNEL_NOT_FOUND", "支付渠道不存在或未启用")
	case containsStr(msg, "CHANNEL_INVALID"):
		return errors.BadRequest("wallet.CHANNEL_INVALID", "充值不支持余额渠道")
	case containsStr(msg, "CHANNEL_UNSUPPORTED"):
		return errors.BadRequest("wallet.CHANNEL_UNSUPPORTED", "支付渠道驱动未实现")
	case containsStr(msg, "CHANNEL_CONFIG_INVALID"):
		return errors.InternalServer("wallet.CHANNEL_CONFIG_INVALID", "支付渠道配置无效")
	case containsStr(msg, "RECHARGE_NOT_FOUND"):
		return errors.NotFound("wallet.RECHARGE_NOT_FOUND", "充值单不存在")
	default:
		return errors.InternalServer("wallet.PAYMENT_CREATE_FAILED", "发起支付失败")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// rechargePolicy 充值档位（settings.recharge 组；读取失败回退目录默认值——
// min=1000 分 / max=500000 分 / enabled=true，与 settings 目录一致）。
func (s *StoreWalletService) rechargePolicy(ctx context.Context) (minAmount, maxAmount int64, enabled bool) {
	minAmount, maxAmount, enabled = 1000, 500000, true
	if s.settings == nil {
		return
	}
	get := func(key string, out any) bool {
		raw, err := s.settings.GetJSON(ctx, "recharge", key)
		if err != nil || len(raw) == 0 {
			return false
		}
		return json.Unmarshal(raw, out) == nil
	}
	get("enabled", &enabled)
	get("min_amount", &minAmount)
	get("max_amount", &maxAmount)
	return
}

// giftFor 赠送档位（gift_tiers 精确匹配充值金额；客户端无法指定赠送——
// 防止「改赠送字段刷余额」）。
func (s *StoreWalletService) giftFor(ctx context.Context, amount int64) (giftAmount int64, giftPoints int32) {
	if s.settings == nil {
		return 0, 0
	}
	raw, err := s.settings.GetJSON(ctx, "recharge", "gift_tiers")
	if err != nil || len(raw) == 0 {
		return 0, 0
	}
	var tiers []struct {
		Amount      int64 `json:"amount"`
		GiftBalance int64 `json:"gift_balance"`
		GiftPoints  int32 `json:"gift_points"`
	}
	if json.Unmarshal(raw, &tiers) != nil {
		return 0, 0
	}
	for _, t := range tiers {
		if t.Amount == amount {
			return t.GiftBalance, t.GiftPoints
		}
	}
	return 0, 0
}

// ── Admin ──

// AdminWalletService 钱包管理服务。
type AdminWalletService struct {
	adminv1.UnimplementedAdminWalletServiceServer
	repo      *WalletRepoImpl
	data      *data.Data
	giftcards *GiftcardRepo // 礼品卡批次管理（P1-05 T4；nil = 未装配）
}

// NewAdminWalletService 构造。
func NewAdminWalletService(repo *WalletRepoImpl, d *data.Data, giftcards *GiftcardRepo) *AdminWalletService {
	return &AdminWalletService{repo: repo, data: d, giftcards: giftcards}
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

// AdjustPoints 积分调整（正=增加 负=扣减；走积分账本，幂等键带时间戳防同额去重误伤）。
func (s *AdminWalletService) AdjustPoints(ctx context.Context, req *adminv1.AdjustPointsRequest) (*adminv1.PointsBalance, error) {
	if req.GetReason() == "" {
		return nil, errors.BadRequest("wallet.REASON_REQUIRED", "调账原因必填")
	}
	if req.GetPoints() == 0 {
		return nil, errors.BadRequest("wallet.INVALID_POINTS", "积分调整数量必须非零")
	}
	pts := req.GetPoints()
	direction := "in"
	if pts < 0 {
		direction = "out"
		pts = -pts
	}
	ref := fmt.Sprintf("adjust-points:%d:%d:%d", req.GetUserId(), req.GetPoints(), time.Now().UnixNano())
	entry := PointEntry{
		UserID: req.GetUserId(), Direction: direction, Type: "adjust",
		Amount: pts, Reference: ref, Remark: req.GetReason(),
	}
	var err error
	if direction == "in" {
		err = s.repo.PointCreditInTx(ctx, entry)
	} else {
		err = s.repo.PointDebitInTx(ctx, entry)
	}
	if err != nil {
		return nil, errors.InternalServer("wallet.POINTS_ADJUST_FAILED", "积分调整失败: "+err.Error())
	}
	balance, err := s.repo.GetPoints(ctx, req.GetUserId())
	if err != nil {
		balance = 0
	}
	return &adminv1.PointsBalance{UserId: req.GetUserId(), Points: balance}, nil
}

// Adjust 手动调账（管理面唯一合法「客户端提交金额」路径——RBAC 权限 + 服务端
// 边界校验 + 审计覆盖，铁律 16）。
func (s *AdminWalletService) Adjust(ctx context.Context, req *adminv1.AdjustRequest) (*adminv1.Balance, error) {
	if req.GetReason() == "" {
		return nil, errors.BadRequest("wallet.REASON_REQUIRED", "调账原因必填")
	}
	if req.GetAmountCents() == 0 || !money.ValidSignedCents(req.GetAmountCents()) {
		return nil, errors.BadRequest("wallet.INVALID_AMOUNT", "调账金额非法（非零且不超上限）")
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
	// 全站流水（user_id=0）：批量回填归属用户名
	names := map[uint64]string{}
	if req.GetUserId() == 0 && len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		seen := map[uint64]bool{}
		for _, r := range rows {
			if !seen[r.UserID] {
				seen[r.UserID] = true
				ids = append(ids, r.UserID)
			}
		}
		if names, err = s.repo.UsernamesByIDs(ctx, ids); err != nil {
			names = map[uint64]string{} // 用户名回填失败不阻断流水列表
		}
	}
	reply := &adminv1.ListWalletTxReply{Total: total}
	for _, r := range rows {
		reply.Transactions = append(reply.Transactions, &adminv1.WalletTx{
			Id: r.ID, Direction: r.Direction, Type: r.Type,
			AmountCents: r.Amount, BalanceBeforeCents: r.BalanceBefore,
			BalanceAfterCents: r.BalanceAfter, Reference: r.Reference, Remark: r.Remark,
			UserId: r.UserID, Username: names[r.UserID],
		})
	}
	return reply, nil
}

var _ = order.StatusPaid // 保持引用
