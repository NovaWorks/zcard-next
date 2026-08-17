package affiliate

// P3-03 必测项：幂等（同订单重复事件三级各一条）、不发佣矩阵（自购/无归因/停用）、
// 冻结确认（到期 wallet 入账恰一次）、退款逆向（pending 作废 / available 扣回 /
// 不足负债 → 后续佣金抵扣）。

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	_ "modernc.org/sqlite"
)

// fakeWallet 内存钱包（记录流水；余额可编程）。
type fakeWallet struct {
	balance map[uint64]int64
	credits []walletport.Entry
	debits  []walletport.Entry
}

func newFakeWallet() *fakeWallet { return &fakeWallet{balance: map[uint64]int64{}} }

func (w *fakeWallet) CreditInTx(_ context.Context, e walletport.Entry) error {
	for _, c := range w.credits {
		if c.Reference == e.Reference {
			return nil // 幂等
		}
	}
	w.credits = append(w.credits, e)
	w.balance[e.UserID] += int64(e.Amount)
	return nil
}

func (w *fakeWallet) DebitInTx(_ context.Context, e walletport.Entry) error {
	for _, d := range w.debits {
		if d.Reference == e.Reference {
			return nil
		}
	}
	if w.balance[e.UserID] < int64(e.Amount) {
		return errors.New("wallet.BALANCE_INSUFFICIENT")
	}
	w.debits = append(w.debits, e)
	w.balance[e.UserID] -= int64(e.Amount)
	return nil
}

func (w *fakeWallet) Lock(_ context.Context, _ uint64, _ money.Cents, _ int64) error { return nil }
func (w *fakeWallet) Unlock(_ context.Context, _ uint64, _ money.Cents) error        { return nil }

// fakeSettings affiliate 配置桩（扁平键按 key 返回）。
type fakeSettings struct{ kv map[string]string }

func (f fakeSettings) GetJSON(_ context.Context, _, key string) ([]byte, error) {
	if v, ok := f.kv[key]; ok {
		return []byte(v), nil
	}
	return nil, nil
}

func newAffiliateData(t *testing.T) (*CommissionRepo, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:aftest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewCommissionRepo(d), d
}

// newEngine cfg 形如 `enabled=false|self_buy=true|freeze_days=0`（k=v 竖线分隔）。
func newEngine(t *testing.T, wallet *fakeWallet, cfg string) (*AffiliateService, *CommissionRepo, *data.Data) {
	repo, d := newAffiliateData(t)
	kv := map[string]string{}
	for _, part := range splitPipe(cfg) {
		if k, v, ok := cutEq(part); ok {
			kv[k] = v
		}
	}
	svc := NewAffiliateService(repo, wallet, fakeSettings{kv: kv}, nil, nil)
	return svc, repo, d
}

func splitPipe(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func cutEq(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func paidEnv(orderID, buyer, l1, l2, l3 uint64, total int64) events.Envelope {
	payload := fmt.Sprintf(`{"order_id":%d,"user_id":%d,"invite_l1":%d,"invite_l2":%d,"invite_l3":%d,"total_cents":%d,"profit_cents":%d}`,
		orderID, buyer, l1, l2, l3, total, total)
	return events.Envelope{EventID: 1, Type: "order.paid", Payload: []byte(payload)}
}

func refundedEnv(orderID uint64, ratio int64) events.Envelope {
	payload := fmt.Sprintf(`{"order_id":%d,"refund_ratio":%d}`, orderID, ratio)
	return events.Envelope{EventID: 2, Type: "order.refunded", Payload: []byte(payload)}
}

// TestCommissionIdempotent 同订单重复事件三级各恰好一条。
func TestCommissionIdempotent(t *testing.T) {
	w := newFakeWallet()
	svc, repo, _ := newEngine(t, w, "")
	ctx := context.Background()

	env := paidEnv(100, 10, 7, 8, 9, 10000)
	_ = svc.OnOrderPaid(ctx, env)
	_ = svc.OnOrderPaid(ctx, env) // 重复投递
	_ = svc.OnOrderPaid(ctx, env)

	rows, _ := repo.ListByOrder(ctx, 100)
	if len(rows) != 3 {
		t.Fatalf("应三级各一条: %d", len(rows))
	}
	tiers := map[int8]bool{}
	for _, r := range rows {
		tiers[r.Tier] = true
	}
	if !tiers[1] || !tiers[2] || !tiers[3] {
		t.Fatal("层级缺失")
	}
	// 金额：默认 5%/2%/1% × 10000 = 500/200/100
	amounts := map[int8]int64{}
	for _, r := range rows {
		amounts[r.Tier] = r.Amount
	}
	if amounts[1] != 500 || amounts[2] != 200 || amounts[3] != 100 {
		t.Fatalf("三级金额错误: %v", amounts)
	}
}

// TestNoCommissionMatrix 不发佣：自购 / 无归因 / 停用。
func TestNoCommissionMatrix(t *testing.T) {
	t.Run("自购不发", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, "")
		_ = svc.OnOrderPaid(context.Background(), paidEnv(1, 7, 7, 8, 9, 10000)) // buyer=7=l1
		rows, _ := repo.ListByOrder(context.Background(), 1)
		if len(rows) != 0 {
			t.Fatalf("自购不应发佣: %d", len(rows))
		}
	})
	t.Run("无归因不发", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, "")
		_ = svc.OnOrderPaid(context.Background(), paidEnv(2, 10, 0, 0, 0, 10000))
		rows, _ := repo.ListByOrder(context.Background(), 2)
		if len(rows) != 0 {
			t.Fatal("无归因不应发佣")
		}
	})
	t.Run("停用不发", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, `enabled=false`)
		_ = svc.OnOrderPaid(context.Background(), paidEnv(3, 10, 7, 0, 0, 10000))
		rows, _ := repo.ListByOrder(context.Background(), 3)
		if len(rows) != 0 {
			t.Fatal("停用不应发佣")
		}
	})
	t.Run("自购开关放开", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, `self_buy=true`)
		_ = svc.OnOrderPaid(context.Background(), paidEnv(4, 7, 7, 8, 0, 10000))
		rows, _ := repo.ListByOrder(context.Background(), 4)
		if len(rows) != 2 { // l1(自购人自己)+l2 发；l3 空
			t.Fatalf("自购开关放开应发: %d", len(rows))
		}
	})
}

// TestConfirmDue 冻结确认：到期 wallet 入账恰一次（幂等键）。
func TestConfirmDue(t *testing.T) {
	w := newFakeWallet()
	svc, repo, _ := newEngine(t, w, `confirm_days=0`) // 0 天=立即到期
	ctx := context.Background()
	_ = svc.OnOrderPaid(ctx, paidEnv(200, 10, 7, 0, 0, 10000))

	svc.ConfirmDue(ctx)
	svc.ConfirmDue(ctx) // 重复执行（cron 重入）

	if len(w.credits) != 1 {
		t.Fatalf("入账应恰一次: %d", len(w.credits))
	}
	if w.balance[7] != 500 {
		t.Fatalf("余额错误: %d", w.balance[7])
	}
	// 佣金状态 → available
	rows, _ := repo.ListByOrder(ctx, 200)
	if string(rows[0].Status) != "available" {
		t.Fatalf("状态错误: %s", rows[0].Status)
	}
}

// TestRefundReversal 退款逆向三态。
func TestRefundReversal(t *testing.T) {
	t.Run("pending 作废", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, ``) // 长冻结：保持 pending
		ctx := context.Background()
		_ = svc.OnOrderPaid(ctx, paidEnv(300, 10, 7, 0, 0, 10000))
		_ = svc.OnOrderRefunded(ctx, refundedEnv(300, 10000))
		rows, _ := repo.ListByOrder(ctx, 300)
		if string(rows[0].Status) != "reversed" {
			t.Fatalf("pending 应作废: %s", rows[0].Status)
		}
		if len(w.debits) != 0 {
			t.Fatal("未入账不应扣款")
		}
	})
	t.Run("available 扣回", func(t *testing.T) {
		w := newFakeWallet()
		svc, repo, _ := newEngine(t, w, `confirm_days=0`)
		ctx := context.Background()
		_ = svc.OnOrderPaid(ctx, paidEnv(301, 10, 7, 0, 0, 10000))
		svc.ConfirmDue(ctx) // 入账 500
		_ = svc.OnOrderRefunded(ctx, refundedEnv(301, 10000))
		if w.balance[7] != 0 {
			t.Fatalf("应全额扣回: %d", w.balance[7])
		}
		rows, _ := repo.ListByOrder(ctx, 301)
		if string(rows[0].Status) != "reversed" {
			t.Fatal("状态应 reversed")
		}
	})
	t.Run("部分退款按比例", func(t *testing.T) {
		w := newFakeWallet()
		svc, _, _ := newEngine(t, w, `confirm_days=0`)
		ctx := context.Background()
		_ = svc.OnOrderPaid(ctx, paidEnv(302, 10, 7, 0, 0, 10000))
		svc.ConfirmDue(ctx)                                  // 500
		_ = svc.OnOrderRefunded(ctx, refundedEnv(302, 5000)) // 50%
		if w.balance[7] != 250 {
			t.Fatalf("应按比例扣回: %d", w.balance[7])
		}
	})
	t.Run("不足转负债后续抵扣", func(t *testing.T) {
		w := newFakeWallet()
		w.balance[7] = 100 // 已提现只剩 100
		svc, repo, _ := newEngine(t, w, `confirm_days=0`)
		ctx := context.Background()
		_ = svc.OnOrderPaid(ctx, paidEnv(303, 10, 7, 0, 0, 10000))
		svc.ConfirmDue(ctx) // +500 → 600
		// 用户先提走大部分：模拟提现
		w.balance[7] = 50
		_ = svc.OnOrderRefunded(ctx, refundedEnv(303, 10000)) // 需扣 500 > 50 → 负债
		rows, _ := repo.ListByOrder(ctx, 303)
		hasDebt := false
		for _, r := range rows {
			if r.Amount < 0 {
				hasDebt = true
			}
		}
		if !hasDebt {
			t.Fatal("不足应产生负债行")
		}
		// 后续佣金入账（新订单同 referrer）：本轮入账 500 → 余额 550；
		// 负债行余额不足未扣——下一轮 cron（每小时）重试 550 ≥ 500 → 抵扣成功
		_ = svc.OnOrderPaid(ctx, paidEnv(304, 11, 7, 0, 0, 10000))
		svc.ConfirmDue(ctx) // 第 1 轮：佣金入账（负债因余额不足跳过）
		svc.ConfirmDue(ctx) // 第 2 轮：负债重试 → 抵扣
		stats, _ := repo.StatsByUser(ctx, 7)
		if stats.DebtCents != 0 {
			t.Fatalf("负债应已清: %d", stats.DebtCents)
		}
		// 550 - 500 = 50
		if w.balance[7] != 50 {
			t.Fatalf("抵扣后余额错误: %d", w.balance[7])
		}
	})
}

// TestStatsAndTeam 统计与团队。
func TestStatsAndTeam(t *testing.T) {
	w := newFakeWallet()
	svc, repo, d := newEngine(t, w, `confirm_days=0`)
	ctx := context.Background()
	// 用户链：7 ← 10（l1）← 11（l1=10, l2=7）
	if _, err := d.Client.User.Create().SetUsername("buyer").SetStatus(user.StatusActive).
		SetInviteL1(10).SetInviteL2(7).Save(ctx); err != nil {
		t.Fatal(err)
	}
	_ = svc.OnOrderPaid(ctx, paidEnv(400, 10, 7, 0, 0, 10000)) // buyer=10：l1=7 l2=0 l3=0
	// buyer 10 的链是 (10 无链)？——事件手工构造链 (l1=7)。10 的下级统计：
	l1, l2, l3, err := repo.TeamCounts(ctx, 10)
	if err != nil || l1 != 1 {
		t.Fatalf("10 的直推应 1: %d %v", l1, err)
	}
	_ = l2
	_ = l3
	stats, _ := repo.StatsByUser(ctx, 7)
	if stats.PendingCents != 500 {
		t.Fatalf("7 冻结中应 500: %+v", stats)
	}
}
