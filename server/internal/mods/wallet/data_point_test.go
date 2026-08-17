package wallet

// 积分账本测试（P1-05 M1b）：入账幂等/非负/重算一致/充值赠送接线。

import (
	"context"
	"testing"
)

// TestPointCreditDebit 入账/扣减/幂等/非负。
func TestPointCreditDebit(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()

	// 入账 100（幂等：同 reference 重放只入一次）
	for i := 0; i < 3; i++ {
		if err := repo.PointCreditInTx(ctx, PointEntry{
			UserID: 1, Direction: "in", Type: "earn_recharge",
			Amount: 100, Reference: "points:recharge:1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	pts, err := repo.GetPoints(ctx, 1)
	if err != nil || pts != 100 {
		t.Fatalf("积分错误: %d %v", pts, err)
	}
	// 扣减 30
	if err := repo.PointDebitInTx(ctx, PointEntry{
		UserID: 1, Direction: "out", Type: "redeem",
		Amount: 30, Reference: "points:redeem:1",
	}); err != nil {
		t.Fatal(err)
	}
	// 余额不足拒绝（不产生流水）
	if err := repo.PointDebitInTx(ctx, PointEntry{
		UserID: 1, Direction: "out", Type: "redeem",
		Amount: 999, Reference: "points:redeem:2",
	}); err == nil {
		t.Fatal("积分不足应拒绝")
	}
	pts, _ = repo.GetPoints(ctx, 1)
	if pts != 70 {
		t.Fatalf("扣减后积分错误: %d", pts)
	}
	// 重算一致
	reb, err := repo.RebuildPoints(ctx, 1)
	if err != nil || reb != 70 {
		t.Fatalf("重算不一致: %d %v", reb, err)
	}
}
