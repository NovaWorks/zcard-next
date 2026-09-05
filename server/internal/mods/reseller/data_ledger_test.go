package reseller

// 账本四件套：幂等 / 拆行 / 负债 / 重算一致（ 补全验证）。

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// TestLedgerWithdrawSplit 部分提现拆行：FIFO 消费 + 行额度不足拆行 + 打款/驳回。
func TestLedgerWithdrawSplit(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)

	// 三笔利润：100 / 200 / 300（到期确认转 available）
	for i, amt := range []money.Cents{100, 200, 300} {
		if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: uint64(100 + i), Amount: amt}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10); err != nil || n != 3 {
		t.Fatalf("确认失败: %d %v", n, err)
	}

	// 提现 250：FIFO 消费 100 + 200（拆行：200 拆为 150 available + 50 locked）
	if err := r.WithdrawLock(ctx, subsite, 250, "W001"); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := r.Ledger(ctx, subsite, "locked", 1, 10)
	if len(rows) != 2 { // 100 整行 + 200 拆出的 50
		t.Fatalf("锁定行数错误: %d", len(rows))
	}
	var lockedTotal int64
	for _, row := range rows {
		lockedTotal += row.Amount
	}
	if lockedTotal != 250 {
		t.Fatalf("锁定总额错误: %d", lockedTotal)
	}
	// 剩余 available = 600 - 250 = 350
	acc, _ := r.GetBalance(ctx, subsite)
	if acc.Available != 350 {
		t.Fatalf("可用余额错误: %d", acc.Available)
	}
	// 幂等重放：同 ref 返回已锁定（不重复扣）
	if err := r.WithdrawLock(ctx, subsite, 250, "W001"); err != ErrWithdrawLocked {
		t.Fatalf("重放应返回已锁定: %v", err)
	}

	// 打款：locked 清零
	if err := r.WithdrawPaid(ctx, subsite, "W001"); err != nil {
		t.Fatal(err)
	}
	acc, _ = r.GetBalance(ctx, subsite)
	if acc.Locked != 0 {
		t.Fatalf("打款后 locked 应清零: %d", acc.Locked)
	}
	// 重算一致
	ra, rl, _, _ := r.RecomputeBalance(ctx, subsite)
	if ra != 350 || rl != 0 {
		t.Fatalf("重算不一致: %d/%d", ra, rl)
	}
}

// TestLedgerWithdrawReject 驳回解锁回可用。
func TestLedgerWithdrawReject(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)
	if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: 1, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10); err != nil {
		t.Fatal(err)
	}
	if err := r.WithdrawLock(ctx, subsite, 100, "W001"); err != nil {
		t.Fatal(err)
	}
	if err := r.WithdrawReject(ctx, subsite, "W001"); err != nil {
		t.Fatal(err)
	}
	acc, _ := r.GetBalance(ctx, subsite)
	if acc.Available != 100 || acc.Locked != 0 {
		t.Fatalf("驳回应解锁: %d/%d", acc.Available, acc.Locked)
	}
}

// TestLedgerRefundDebt 退款逆向：不足扣 → 负债态 → 后续利润优先抵扣。
func TestLedgerRefundDebt(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)

	// 利润 100 入账并确认 → 提现打款花掉（可用归零）
	if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: 1, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10); err != nil {
		t.Fatal(err)
	}
	if err := r.WithdrawLock(ctx, subsite, 100, "W001"); err != nil {
		t.Fatal(err)
	}
	if err := r.WithdrawPaid(ctx, subsite, "W001"); err != nil {
		t.Fatal(err)
	}
	// 退款 100（全额，但可用已归零）→ 负债 100
	if err := r.RefundDeduct(ctx, subsite, 1, 100, 10000); err != nil {
		t.Fatal(err)
	}
	ra, _, rn, _ := r.RecomputeBalance(ctx, subsite)
	if ra != 0 || rn != 100 {
		t.Fatalf("负债态错误: avail=%d negative=%d", ra, rn)
	}
	acc, _ := r.GetBalance(ctx, subsite)
	if acc.Negative != 100 {
		t.Fatalf("负债缓存应与重算一致: %d", acc.Negative)
	}
	// 幂等：同单退款重放不重复扣
	if err := r.RefundDeduct(ctx, subsite, 1, 100, 10000); err != nil {
		t.Fatal(err)
	}
	// 新利润 150：优先抵债 100 → 净入账 50（pending）
	if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: 2, Amount: 150}); err != nil {
		t.Fatal(err)
	}
	_, _, rn2, _ := r.RecomputeBalance(ctx, subsite)
	if rn2 != 0 {
		t.Fatalf("利润应先抵债: negative=%d", rn2)
	}
	// 净入账 50 待确认；负债缓存已清（recompute 口径 pending 正数计入可用）
	acc, err := r.GetBalance(ctx, subsite)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Negative != 0 || acc.Available != 50 {
		t.Fatalf("缓存错误: avail=%d negative=%d", acc.Available, acc.Negative)
	}
	// 到期确认后可用 50；重算一致
	if _, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10); err != nil {
		t.Fatal(err)
	}
	ra2, _, rn3, err := r.RecomputeBalance(ctx, subsite)
	if err != nil {
		t.Fatal(err)
	}
	if rn3 != 0 || ra2 != 50 {
		t.Fatalf("抵债后净入账错误: avail=%d negative=%d", ra2, rn3)
	}
	_ = d
}

// TestLedgerRebalanceConsistent 重算一致（四件套收尾：任何操作序列后可重算）。
func TestLedgerRebalanceConsistent(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)

	for i := 1; i <= 3; i++ {
		if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: uint64(i), Amount: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10); err != nil {
		t.Fatal(err)
	}
	if err := r.WithdrawLock(ctx, subsite, 150, "W001"); err != nil {
		t.Fatal(err)
	}
	if err := r.RefundDeduct(ctx, subsite, 1, 100, 5000); err != nil { // 半退 50
		t.Fatal(err)
	}
	if err := r.WithdrawPaid(ctx, subsite, "W001"); err != nil {
		t.Fatal(err)
	}
	acc, _ := r.GetBalance(ctx, subsite)
	ra, rl, rn, _ := r.RecomputeBalance(ctx, subsite)
	// 缓存 available 与重算一致（locked 独立核算）
	if acc.Available != ra {
		t.Fatalf("缓存/重算不一致: cache=%d recompute=%d", acc.Available, ra)
	}
	if acc.Locked != rl || rl != 0 {
		t.Fatalf("locked 不一致: %d/%d", acc.Locked, rl)
	}
	_ = rn
	// 总额 = 300 入账 - 150 提现 - 50 退款 = 100
	if ra != 100 {
		t.Fatalf("最终可用错误: %d", ra)
	}
	_ = resellerledgerentry.FieldID
}
