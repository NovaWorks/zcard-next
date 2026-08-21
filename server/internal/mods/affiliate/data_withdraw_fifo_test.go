package affiliate

// 佣金提现 FIFO 消耗测试：整行消耗 / 末行拆分 / 余额不足拒绝 / 冻结口径。

import (
	"context"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
)

// seedAvailable 造 available 佣金行（金额列表）。
func seedAvailable(t *testing.T, repo *CommissionRepo, userID uint64, amounts []int64) {
	t.Helper()
	ctx := context.Background()
	for i, amt := range amounts {
		if err := repo.Insert(ctx, CommissionRow{
			ReferrerID: userID, OrderID: uint64(100 + i), Tier: 1, Rate: 500,
			BaseAmount: amt * 10, Amount: amt,
			AvailableAt: time.Now().Add(-2 * time.Hour),
		}); err != nil && err != ErrDuplicate {
			t.Fatal(err)
		}
	}
	// 到期确认 → available（真实路径）
	due, _ := repo.ListDueConfirm(ctx, time.Now().Add(-time.Hour), 100)
	for _, c := range due {
		_ = repo.MarkAvailable(ctx, c.ID)
	}
}

// TestConsumeFIFOWholeRows 整行消耗：100+200 消耗 300 → 全 withdrawn。
func TestConsumeFIFOWholeRows(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := context.Background()
	seedAvailable(t, repo, 7, []int64{100, 200})

	if err := repo.ConsumeAvailableFIFO(ctx, 7, 300); err != nil {
		t.Fatalf("FIFO 消耗: %v", err)
	}
	rows, _ := d.Client.AffiliateCommission.Query().
		Where(affiliatecommission.ReferrerID(7)).All(ctx)
	for _, r := range rows {
		if r.Status != affiliatecommission.StatusWithdrawn {
			t.Fatalf("应全部 withdrawn: %+v", r.Status)
		}
	}
}

// TestConsumeFIFOSplit 末行拆分：100+200 消耗 250 → 第一行 withdrawn(100) +
// 第二行拆分 available(50) + 新行 withdrawn(150)。
func TestConsumeFIFOSplit(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := context.Background()
	seedAvailable(t, repo, 8, []int64{100, 200})

	if err := repo.ConsumeAvailableFIFO(ctx, 8, 250); err != nil {
		t.Fatalf("FIFO 拆分: %v", err)
	}
	rows, _ := d.Client.AffiliateCommission.Query().
		Where(affiliatecommission.ReferrerID(8)).All(ctx)
	var avail, withdrawn int64
	for _, r := range rows {
		switch r.Status {
		case affiliatecommission.StatusAvailable:
			avail += r.Amount
		case affiliatecommission.StatusWithdrawn:
			withdrawn += r.Amount
		}
	}
	if avail != 50 || withdrawn != 250 {
		t.Fatalf("拆分错误: avail=%d want 50, withdrawn=%d want 250", avail, withdrawn)
	}
	// 行数 = 原 2 行（第二行减额）+ 拆分新 1 行 = 3
	if n := len(rows); n != 3 {
		t.Fatalf("行数 = %d, want 3", n)
	}
}

// TestConsumeFIFOInsufficient 余额不足拒绝（整体不变）。
func TestConsumeFIFOInsufficient(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := context.Background()
	seedAvailable(t, repo, 9, []int64{100})

	if err := repo.ConsumeAvailableFIFO(ctx, 9, 200); err == nil {
		t.Fatal("不足应拒绝")
	}
	rows, _ := d.Client.AffiliateCommission.Query().
		Where(affiliatecommission.ReferrerID(9)).All(ctx)
	if len(rows) != 1 || rows[0].Status != affiliatecommission.StatusAvailable || rows[0].Amount != 100 {
		t.Fatalf("拒绝后状态不应变化: %+v", rows)
	}
}

// TestFrozenWithdrawAmount 冻结口径：pending/approved 计入，paid/rejected 不计。
func TestFrozenWithdrawAmount(t *testing.T) {
	repo, d := newAffiliateData(t)
	ctx := context.Background()
	for _, st := range []string{"pending", "approved", "paid", "rejected"} {
		if _, err := d.Client.Withdrawal.Create().
			SetUserID(11).SetAmount(100).SetFee(0).
			SetMethod(map[string]any{"type": "alipay"}).
			SetStatus(withdrawal.Status(st)).
			Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	frozen, err := repo.FrozenWithdrawAmount(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if frozen != 200 { // pending + approved
		t.Fatalf("冻结额 = %d, want 200", frozen)
	}
}
