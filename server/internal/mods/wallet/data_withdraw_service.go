package wallet

// T5 提现服务面（P1-05 M3）：storefront 申请 + admin 审核/打款。
// 金额服务端裁决（铁律 16）：白名单/最低额/手续费全部取 settings.withdraw。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
	affiliateport "github.com/NovaWorks/zcard-next/server/internal/mods/affiliate/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/go-kratos/kratos/v3/errors"
)

// withdrawPolicy 提现配置（settings.withdraw 组；读取失败回退目录默认值）。
type withdrawPolicy struct {
	Enabled   bool
	MinAmount int64
	FeeType   string // fixed | percent
	FeeValue  int64  // fixed=分；percent=万分比
	Methods   []withdrawMethod
}

type withdrawMethod struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Remark string `json:"remark,omitempty"`
}

func (s *StoreWalletService) withdrawPolicy(ctx context.Context) withdrawPolicy {
	p := withdrawPolicy{Enabled: false, MinAmount: 1000, FeeType: "fixed"}
	if s.settings == nil {
		return p
	}
	get := func(key string, out any) bool {
		raw, err := s.settings.GetJSON(ctx, "withdraw", key)
		if err != nil || len(raw) == 0 {
			return false
		}
		return json.Unmarshal(raw, out) == nil
	}
	get("enabled", &p.Enabled)
	get("min_amount", &p.MinAmount)
	var feeType string
	if get("fee_type", &feeType) && (feeType == "fixed" || feeType == "percent") {
		p.FeeType = feeType
	}
	get("fee_value", &p.FeeValue)
	var rawMethods json.RawMessage
	if get("methods", &rawMethods) {
		_ = json.Unmarshal(rawMethods, &p.Methods)
	}
	return p
}

// feeOf 手续费（fixed=分；percent=万分比）。
func (p withdrawPolicy) feeOf(amount int64) int64 {
	if p.FeeType == "percent" {
		return amount * p.FeeValue / 10000
	}
	return p.FeeValue
}

// CreateWithdrawal 提现申请（佣金提现）：档位校验 → 白名单收款方式 →
// USDT 地址校验 → 佣金余额校验（available − 冻结 ≥ 金额）→ 落单（冻结口径=单本身）。
func (s *StoreWalletService) CreateWithdrawal(ctx context.Context, req *storefrontv1.CreateWithdrawalRequest) (*storefrontv1.CreateWithdrawalReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	policy := s.withdrawPolicy(ctx)
	if !policy.Enabled {
		return nil, errors.Forbidden("wallet.WITHDRAW_DISABLED", "提现功能未开放")
	}
	amount := req.GetAmountCents()
	if !money.ValidCents(amount) || amount < policy.MinAmount {
		return nil, errors.BadRequest("wallet.INVALID_AMOUNT", "提现金额低于最低限额或超出上限")
	}
	// 收款方式白名单（方法类型必须命中；账号服务端不设限但落快照）
	var matched *withdrawMethod
	for i := range policy.Methods {
		if policy.Methods[i].Type == req.GetMethodType() {
			matched = &policy.Methods[i]
			break
		}
	}
	if matched == nil || req.GetAccount() == "" {
		return nil, errors.BadRequest("wallet.METHOD_INVALID", "收款方式不在白名单或账号为空")
	}
	// USDT TRC20 地址格式校验
	if matched.Type == "usdt_trc20" && !usdtTrc20Re.MatchString(req.GetAccount()) {
		return nil, errors.BadRequest("wallet.USDT_ADDRESS_INVALID", "USDT TRC20 地址格式不正确（T 开头 34 位）")
	}
	// 佣金余额校验（可提 = available − 冻结中提现单）
	if s.commissions != nil {
		stats, err := s.commissions.StatsByUser(ctx, claims.Subject)
		if err != nil {
			return nil, errors.InternalServer("wallet.WITHDRAW_FAILED", "佣金查询失败")
		}
		frozen, _ := s.commissions.FrozenWithdrawAmount(ctx, claims.Subject)
		if stats.AvailableCents-frozen < amount {
			return nil, errors.BadRequest("wallet.INSUFFICIENT_COMMISSION", "可提佣金不足")
		}
	}
	method := map[string]any{
		"type": matched.Type, "name": matched.Name, "account": req.GetAccount(),
	}
	if qr := req.GetQrCodeUrl(); qr != "" {
		method["qr_code_url"] = qr // 收款码图片 URL（微信/支付宝；admin 审核展示）
	}
	fee := policy.feeOf(amount)
	w, err := s.repo.CreateWithdrawal(ctx, claims.Subject, amount, fee, method)
	if err != nil {
		return nil, mapWithdrawErr(err)
	}
	return &storefrontv1.CreateWithdrawalReply{
		WithdrawalId: w.ID, AmountCents: w.Amount, FeeCents: w.Fee,
		CreditedCents: w.Amount - w.Fee,
	}, nil
}

// ListMyWithdrawals 本人提现记录（状态/驳回原因/时间）。
func (s *StoreWalletService) ListMyWithdrawals(ctx context.Context, req *storefrontv1.ListMyWithdrawalsRequest) (*storefrontv1.ListMyWithdrawalsReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	page, size := withdrawPage(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListWithdrawalsByUser(ctx, claims.Subject, page, size)
	if err != nil {
		return nil, errors.InternalServer("wallet.LIST_FAILED", "读取提现记录失败")
	}
	reply := &storefrontv1.ListMyWithdrawalsReply{Total: total}
	for _, w := range rows {
		item := &storefrontv1.MyWithdrawalItem{
			WithdrawalId: w.ID, AmountCents: w.Amount, FeeCents: w.Fee,
			Status: string(w.Status), RejectReason: w.RejectReason,
		}
		var m struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Account string `json:"account"`
		}
		if raw, err := json.Marshal(w.Method); err == nil {
			_ = json.Unmarshal(raw, &m)
		}
		item.MethodType, item.MethodName, item.Account = m.Type, m.Name, m.Account
		if !w.ReviewedAt.IsZero() {
			item.ReviewedAt = w.ReviewedAt.Unix()
		}
		if !w.PaidAt.IsZero() {
			item.PaidAt = w.PaidAt.Unix()
		}
		item.Receipt = w.Receipt
		item.CreatedAt = w.CreatedAt.Unix()
		reply.Withdrawals = append(reply.Withdrawals, item)
	}
	return reply, nil
}

func mapWithdrawErr(err error) error {
	msg := err.Error()
	switch {
	case containsStr(msg, "INSUFFICIENT"):
		return errors.BadRequest("wallet.INSUFFICIENT_BALANCE", "可用余额不足")
	case containsStr(msg, "CONCURRENT"):
		return errors.InternalServer("wallet.CONCURRENT_UPDATE", "并发冲突，请重试")
	case containsStr(msg, "NOT_FOUND"):
		return errors.NotFound("wallet.WITHDRAWAL_NOT_FOUND", "提现单不存在")
	case containsStr(msg, "NOT_PENDING"), containsStr(msg, "NOT_APPROVED"):
		return errors.BadRequest("wallet.WITHDRAWAL_STATE", "提现单状态不允许该操作")
	default:
		return errors.InternalServer("wallet.WITHDRAW_FAILED", "提现操作失败")
	}
}

// ── Admin 提现管理 ──────────────────────────────────────────

// ListWithdrawals 提现单列表。
func (s *AdminWalletService) ListWithdrawals(ctx context.Context, req *adminv1.ListWithdrawalsRequest) (*adminv1.ListWithdrawalsReply, error) {
	page, size := withdrawPage(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListWithdrawals(ctx, req.GetStatus(), page, size)
	if err != nil {
		return nil, errors.InternalServer("wallet.LIST_FAILED", "读取提现单失败")
	}
	reply := &adminv1.ListWithdrawalsReply{Total: total}
	for _, w := range rows {
		reply.Withdrawals = append(reply.Withdrawals, toWithdrawalPB(w))
	}
	s.enrichUsernames(ctx, reply.Withdrawals)
	return reply, nil
}

// ReviewWithdrawal 审核（通过/驳回；驳回解锁回余额）。
func (s *AdminWalletService) ReviewWithdrawal(ctx context.Context, req *adminv1.ReviewWithdrawalRequest) (*adminv1.WithdrawalItem, error) {
	if !req.GetApprove() && req.GetReason() == "" {
		return nil, errors.BadRequest("wallet.REASON_REQUIRED", "驳回原因必填")
	}
	w, err := s.repo.ReviewWithdrawal(ctx, req.GetId(), req.GetApprove(), req.GetReason(), adminWalletUID(ctx))
	if err != nil {
		return nil, mapWithdrawErr(err)
	}
	return toWithdrawalPB(w), nil
}

// PayWithdrawal 打款（人工打款模式：approved→paid + locked 扣减 + 流水）。
func (s *AdminWalletService) PayWithdrawal(ctx context.Context, req *adminv1.PayWithdrawalRequest) (*adminv1.WithdrawalItem, error) {
	w, err := s.repo.PayWithdrawal(ctx, req.GetId(), req.GetReceipt())
	if err != nil {
		return nil, mapWithdrawErr(err)
	}
	return toWithdrawalPB(w), nil
}

func adminWalletUID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

func withdrawPage(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}

// toWithdrawalPB 转 admin 协议（method 快照拆出 type/name/account/qr）。
func toWithdrawalPB(w *ent.Withdrawal) *adminv1.WithdrawalItem {
	out := &adminv1.WithdrawalItem{
		Id: w.ID, UserId: w.UserID, AmountCents: w.Amount, FeeCents: w.Fee,
		Status: string(w.Status), RejectReason: w.RejectReason,
	}
	var m struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Account   string `json:"account"`
		QrCodeURL string `json:"qr_code_url"`
	}
	if raw, err := json.Marshal(w.Method); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	out.MethodType, out.MethodName, out.Account, out.QrCodeUrl = m.Type, m.Name, m.Account, m.QrCodeURL
	if !w.ReviewedAt.IsZero() {
		out.ReviewedAt = w.ReviewedAt.Unix()
	}
	if !w.PaidAt.IsZero() {
		out.PaidAt = w.PaidAt.Unix()
	}
	out.Receipt = w.Receipt
	out.CreatedAt = w.CreatedAt.Unix()
	return out
}

// enrichUsernames 批量富化用户名（列表一次查询防 N+1）。
func (s *AdminWalletService) enrichUsernames(ctx context.Context, items []*adminv1.WithdrawalItem) {
	if len(items) == 0 {
		return
	}
	ids := make([]uint64, 0, len(items))
	for _, it := range items {
		if it.UserId > 0 {
			ids = append(ids, it.UserId)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := data.Client(ctx, s.data).User.Query().Where(user.IDIn(ids...)).All(ctx)
	if err != nil {
		return
	}
	nameOf := map[uint64]string{}
	for _, u := range rows {
		nameOf[u.ID] = u.Username
	}
	for _, it := range items {
		it.Username = nameOf[it.UserId]
	}
}

var _ = withdrawal.StatusPending
var _ = fmt.Sprintf

// CommissionSource 佣金读取端口（提现校验；affiliate.CommissionRepo 实现，通道 A）。
type CommissionSource interface {
	StatsByUser(ctx context.Context, userID uint64) (*affiliateport.CommissionStats, error)
	FrozenWithdrawAmount(ctx context.Context, userID uint64) (int64, error)
}

// usdtTrc20Re TRC20 地址（T 开头 34 位 base58，剔除 0OIl）。
var usdtTrc20Re = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
