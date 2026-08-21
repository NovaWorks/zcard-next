package wallet

// T5 提现执行测试：申请锁定/白名单/最低额/审核通过/驳回解锁/打款扣减+流水/状态机。

import (
	"context"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	affiliateport "github.com/NovaWorks/zcard-next/server/internal/mods/affiliate/port"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/go-kratos/kratos/v3/errors"
)

// seedWithdrawSettings 提现配置（enabled/min_amount/fee_type/fee_value/methods）。
func seedWithdrawSettings(t *testing.T, svc *StoreWalletService) {
	t.Helper()
	m := map[string][]byte{}
	write := func(k string, v any) {
		r, _ := json.Marshal(v)
		m["withdraw/"+k] = r
	}
	write("enabled", true)
	write("min_amount", 1000)
	write("fee_type", "percent")
	write("fee_value", 100) // 1%
	// methods 已为 JSON 字面量（避免二次编码成字符串）
	m["withdraw/methods"] = []byte(`[{"type":"alipay","name":"支付宝"},{"type":"wechat","name":"微信"}]`)
	svc.settings = &fakeSettings{m: m}
}

// fakeCommission 佣金源（提现校验注入）。
type fakeCommission struct {
	available int64
	frozen    int64
}

func (f *fakeCommission) StatsByUser(ctx context.Context, userID uint64) (*affiliateport.CommissionStats, error) {
	return &affiliateport.CommissionStats{AvailableCents: f.available}, nil
}
func (f *fakeCommission) FrozenWithdrawAmount(ctx context.Context, userID uint64) (int64, error) {
	return f.frozen, nil
}

func seedBalance(t *testing.T, repo *WalletRepoImpl, uid uint64, amount int64) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreditInTx(ctx, Entry{
		UserID: uid, Direction: "in", Type: "adjust", Amount: amount, Reference: "wd-seed",
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWithdrawApply 申请：锁定 + 手续费 + 白名单 + 最低额。
func TestWithdrawApply(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	commission := &fakeCommission{available: 5000}
	svc := NewStoreWalletService(repo, d, nil, nil, nil, commission)
	seedWithdrawSettings(t, svc)
	seedBalance(t, repo, 1, 5000)
	ctx := userCtx(1)

	reply, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 3000, MethodType: "alipay", Account: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.FeeCents != 30 || reply.CreditedCents != 2970 {
		t.Fatalf("手续费错误: %+v", reply)
	}
	// 佣金提现：钱包余额不动（5000/0）
	avail, locked, _ := repo.GetBalance(ctx, 1)
	if avail != 5000 || locked != 0 {
		t.Fatalf("钱包余额不应变化: avail=%d locked=%d", avail, locked)
	}
	w, _ := repo.GetWithdrawal(ctx, reply.WithdrawalId)
	if string(w.Status) != "pending" || w.Fee != 30 {
		t.Fatalf("提现单错误: %+v", w)
	}

	// 白名单外方法拒绝
	if _, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 1000, MethodType: "bank", Account: "x",
	}); !errors.IsBadRequest(err) {
		t.Fatalf("白名单外应拒绝: %v", err)
	}
	// 低于最低额拒绝
	if _, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 999, MethodType: "alipay", Account: "a",
	}); !errors.IsBadRequest(err) {
		t.Fatalf("低于最低额应拒绝: %v", err)
	}
	// 超可提佣金拒绝（第一笔已占 3000——冻结口径；余 2000 不足再提 3000）
	commission.frozen = 3000
	if _, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 3000, MethodType: "alipay", Account: "a",
	}); !errors.IsBadRequest(err) {
		t.Fatalf("超佣金应拒绝: %v", err)
	}
	// 停用拒绝
	raw, _ := json.Marshal(false)
	svc.settings.(*fakeSettings).m["withdraw/enabled"] = raw
	if _, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 1000, MethodType: "alipay", Account: "a",
	}); !errors.IsForbidden(err) {
		t.Fatalf("停用应拒绝: %v", err)
	}
}

// TestWithdrawReviewPay 审核通过→打款；驳回→解锁。
func TestWithdrawReviewPay(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	commission := &fakeCommission{available: 5000}
	svc := NewStoreWalletService(repo, d, nil, nil, nil, commission)
	admin := NewAdminWalletService(repo, d, nil)
	seedWithdrawSettings(t, svc)
	seedBalance(t, repo, 1, 5000)
	ctx := userCtx(1)

	reply, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 3000, MethodType: "alipay", Account: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 审核通过
	item, err := admin.ReviewWithdrawal(ctx, &adminv1.ReviewWithdrawalRequest{Id: reply.WithdrawalId, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "approved" {
		t.Fatalf("状态应 approved: %s", item.Status)
	}
	// 打款：locked 扣减 + 流水
	item, err = admin.PayWithdrawal(ctx, &adminv1.PayWithdrawalRequest{Id: reply.WithdrawalId})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "paid" {
		t.Fatalf("状态应 paid: %s", item.Status)
	}
	// 佣金提现：钱包余额不动（线下人工打款，无 wallet 流水）
	avail, locked, _ := repo.GetBalance(ctx, 1)
	if avail != 5000 || locked != 0 {
		t.Fatalf("打款后钱包余额错误: avail=%d locked=%d", avail, locked)
	}
	txs, _, _ := repo.ListTransactions(ctx, 1, 1, 20)
	for _, txn := range txs {
		if txn.Type == "withdraw" {
			t.Fatal("佣金提现不应产生 wallet 流水")
		}
	}
	// 重复打款拒绝（状态机）
	if _, err := admin.PayWithdrawal(ctx, &adminv1.PayWithdrawalRequest{Id: reply.WithdrawalId}); !errors.IsBadRequest(err) {
		t.Fatalf("重复打款应拒绝: %v", err)
	}
}

// TestWithdrawRejectUnlock 驳回：解锁回余额。
func TestWithdrawRejectUnlock(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	svc := NewStoreWalletService(repo, d, nil, nil, nil, nil)
	admin := NewAdminWalletService(repo, d, nil)
	seedWithdrawSettings(t, svc)
	seedBalance(t, repo, 1, 5000)
	ctx := userCtx(1)

	reply, err := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 3000, MethodType: "alipay", Account: "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := admin.ReviewWithdrawal(ctx, &adminv1.ReviewWithdrawalRequest{
		Id: reply.WithdrawalId, Approve: false, Reason: "信息不符",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "rejected" || item.RejectReason != "信息不符" {
		t.Fatalf("驳回状态错误: %+v", item)
	}
	avail, locked, _ := repo.GetBalance(ctx, 1)
	if avail != 5000 || locked != 0 {
		t.Fatalf("驳回应解锁: avail=%d locked=%d", avail, locked)
	}
	// 驳回原因必填
	reply2, _ := svc.CreateWithdrawal(ctx, &storefrontv1.CreateWithdrawalRequest{
		AmountCents: 3000, MethodType: "alipay", Account: "a",
	})
	if _, err := admin.ReviewWithdrawal(ctx, &adminv1.ReviewWithdrawalRequest{Id: reply2.WithdrawalId, Approve: false}); !errors.IsBadRequest(err) {
		t.Fatalf("驳回原因必填: %v", err)
	}
}

var _ = withdrawal.FieldStatus
var _ = money.Zero
