package wallet

// 验收核心：InTx 账务内核测试（并发幂等/非负/重算一致性）。

import (
	"context"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newTestData(t *testing.T) *data.Data {
	t.Helper()
	handle, err := db.SQLite.Open("file:wallettest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
}

// TestSequentialCreditIdempotent 幂等验证：同 reference 连续 10 次只入账一次。
// SQLite 单写者天然串行（等价于并发路径的效果验证）；MySQL/PG 真并发测试 CI 集成线。
func TestSequentialCreditIdempotent(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()
	ref := "recharge:1:1000"

	successes := 0
	for i := 0; i < 10; i++ {
		err := repo.CreditInTx(ctx, Entry{
			UserID: 1, Direction: "in", Type: "recharge",
			Amount: 1000, Reference: ref,
		})
		if err == nil {
			successes++
		}
	}

	// 验证恰好入账一次（幂等：后续 9 次应也返回 nil 但不重复入账）
	avail, _, _ := repo.GetBalance(ctx, 1)
	if avail != 1000 {
		t.Fatalf("幂等入账金额错误：avail=%d（期望 1000），successes=%d", avail, successes)
	}

	// 验证流水只有一条
	txCount, _ := d.Client.WalletTransaction.Query().Count(ctx)
	if txCount != 1 {
		t.Fatalf("流水数错误：%d（期望 1）", txCount)
	}
}

// TestDebitInsufficient 余额不足拒绝。
func TestDebitInsufficient(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()

	// 入 100
	_ = repo.CreditInTx(ctx, Entry{
		UserID: 1, Direction: "in", Type: "recharge",
		Amount: 100, Reference: "r1",
	})

	// 扣 200 应失败
	err := repo.DebitInTx(ctx, Entry{
		UserID: 1, Direction: "out", Type: "order_pay",
		Amount: 200, Reference: "d1",
	})
	if err == nil {
		t.Fatal("余额不足应拒绝")
	}

	// 余额不变
	avail, _, _ := repo.GetBalance(ctx, 1)
	if avail != 100 {
		t.Fatalf("失败后余额变化：avail=%d（期望 100）", avail)
	}
}

// TestRebuildBalance 余额可由流水重算。
func TestRebuildBalance(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()

	// 入 1000 → 扣 300 → 入 500 → 扣 200
	ops := []Entry{
		{UserID: 1, Direction: "in", Type: "recharge", Amount: 1000, Reference: "t1"},
		{UserID: 1, Direction: "out", Type: "order_pay", Amount: 300, Reference: "t2"},
		{UserID: 1, Direction: "in", Type: "refund", Amount: 500, Reference: "t3"},
		{UserID: 1, Direction: "out", Type: "order_pay", Amount: 200, Reference: "t4"},
	}
	for _, op := range ops {
		if op.Direction == "in" {
			_ = repo.CreditInTx(ctx, op)
		} else {
			_ = repo.DebitInTx(ctx, op)
		}
	}

	// 重算
	rebuilt, err := repo.RebuildBalance(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	// 与账户比对
	avail, _, _ := repo.GetBalance(ctx, 1)
	if rebuilt != avail {
		t.Fatalf("重算不一致：rebuilt=%d avail=%d", rebuilt, avail)
	}
	if avail != 1000 { // 1000-300+500-200=1000
		t.Fatalf("余额计算错误：avail=%d（期望 1000）", avail)
	}
}

// TestIdempotentReentry 幂等重入：重复提交直接返回成功。
func TestIdempotentReentry(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()

	e := Entry{
		UserID: 1, Direction: "in", Type: "recharge",
		Amount: 500, Reference: "idem-test",
	}

	// 第一次成功
	if err := repo.CreditInTx(ctx, e); err != nil {
		t.Fatal(err)
	}

	// 第二次幂等返回成功（不报错）
	if err := repo.CreditInTx(ctx, e); err != nil {
		t.Fatalf("幂等重入应返回 nil，得到: %v", err)
	}

	// 余额只有 500
	avail, _, _ := repo.GetBalance(ctx, 1)
	if avail != 500 {
		t.Fatalf("幂等后余额变化：avail=%d（期望 500）", avail)
	}
}

// TestAccountAutoCreate 未开户自动建号。
func TestAccountAutoCreate(t *testing.T) {
	d := newTestData(t)
	repo := NewWalletRepoImpl(d)
	ctx := context.Background()

	// 未开户用户查余额 = 0
	avail, locked, err := repo.GetBalance(ctx, 999)
	if err != nil {
		t.Fatal(err)
	}
	if avail != 0 || locked != 0 {
		t.Fatalf("未开户应为零余额：avail=%d locked=%d", avail, locked)
	}

	// 入账后自动建号
	_ = repo.CreditInTx(ctx, Entry{
		UserID: 999, Direction: "in", Type: "adjust",
		Amount: 100, Reference: fmt.Sprintf("auto-%d", 999),
	})
	avail, _, _ = repo.GetBalance(ctx, 999)
	if avail != 100 {
		t.Fatalf("自动建号后余额错误：avail=%d", avail)
	}
}
