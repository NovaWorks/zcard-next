package wallet

// T5 提现服务面（P1-05 M3）：storefront 申请 + admin 审核/打款。
// 金额服务端裁决（铁律 16）：白名单/最低额/手续费全部取 settings.withdraw。

import (
	"context"
	"encoding/json"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
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

// CreateWithdrawal 提现申请：档位校验 → 白名单收款方式 → 锁定+落单。
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
	method := map[string]any{
		"type": matched.Type, "name": matched.Name, "account": req.GetAccount(),
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
	w, err := s.repo.PayWithdrawal(ctx, req.GetId())
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

// toWithdrawalPB 转 admin 协议（method 快照拆出 type/account）。
func toWithdrawalPB(w *ent.Withdrawal) *adminv1.WithdrawalItem {
	out := &adminv1.WithdrawalItem{
		Id: w.ID, UserId: w.UserID, AmountCents: w.Amount, FeeCents: w.Fee,
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
	out.MethodType, out.Account = m.Type, m.Account
	if !w.ReviewedAt.IsZero() {
		out.ReviewedAt = w.ReviewedAt.Unix()
	}
	if !w.PaidAt.IsZero() {
		out.PaidAt = w.PaidAt.Unix()
	}
	out.CreatedAt = w.CreatedAt.Unix()
	return out
}

var _ = withdrawal.StatusPending
var _ = fmt.Sprintf
