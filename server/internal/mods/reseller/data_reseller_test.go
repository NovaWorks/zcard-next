package reseller

// 必测项：定价 4 模式 × 三级优先级 + 上下限/下限保护、
// 分账幂等/冻结/重算一致、防自购三查。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	_ "modernc.org/sqlite"
)

func newResellerData(t *testing.T) (*ResellerRepo, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:rstest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewResellerRepo(d), d
}

// seedApproved 建已过审分站（user 1 = 站主），返回 profile id（= subsite_id）。
func seedApproved(t *testing.T, r *ResellerRepo, d *data.Data) uint64 {
	t.Helper()
	ctx := context.Background()
	if _, err := d.Client.User.Create().SetUsername("owner").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := r.Apply(ctx, ApplyInput{UserID: 1, Reason: "开店"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = r.Review(ctx, p.ID, true, "", 99, 10, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// TestPricingEngine 定价 4 模式 × 优先级。
func TestPricingEngine(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)
	base := money.Cents(1000)

	t.Run("默认加价率", func(t *testing.T) {
		got, err := r.ResolveUnitPrice(ctx, subsite, 1, 0, base)
		if err != nil || got != 1100 { // +10%
			t.Fatalf("默认加价错误: %d %v", got, err)
		}
	})
	t.Run("商品规则覆盖默认", func(t *testing.T) {
		if _, err := r.UpsertPricing(ctx, subsite, 1, 0, "fixed_price", 1500, 50); err != nil {
			t.Fatal(err)
		}
		got, _ := r.ResolveUnitPrice(ctx, subsite, 1, 0, base)
		if got != 1500 {
			t.Fatalf("商品规则应覆盖: %d", got)
		}
	})
	t.Run("SKU 规则最高优先", func(t *testing.T) {
		if _, err := r.UpsertPricing(ctx, subsite, 1, 9, "markup_percent", 2000, 50); err != nil {
			t.Fatal(err) // +20%
		}
		got, _ := r.ResolveUnitPrice(ctx, subsite, 1, 9, base)
		if got != 1200 {
			t.Fatalf("SKU 规则应最优: %d", got)
		}
	})
	t.Run("上限拒绝", func(t *testing.T) {
		if _, err := r.UpsertPricing(ctx, subsite, 2, 0, "markup_percent", 6000, 50); err != ErrMarkupExceed {
			t.Fatalf("超上限应拒绝: %v", err)
		}
	})
	t.Run("下限保护", func(t *testing.T) {
		// fixed_price 低于基础价 → 返回基础价
		if _, err := r.UpsertPricing(ctx, subsite, 3, 0, "fixed_price", 500, 50); err != nil {
			t.Fatal(err)
		}
		got, _ := r.ResolveUnitPrice(ctx, subsite, 3, 0, base)
		if got != base {
			t.Fatalf("下限保护失败: %d", got)
		}
	})
	t.Run("inherit 直通", func(t *testing.T) {
		if _, err := r.UpsertPricing(ctx, subsite, 4, 0, "inherit", 0, 50); err != nil {
			t.Fatal(err)
		}
		got, _ := r.ResolveUnitPrice(ctx, subsite, 4, 0, base)
		if got != base {
			t.Fatalf("inherit 应直通: %d", got)
		}
	})
}

// TestSettleLedger 分账：幂等 + 冻结 + 重算一致。
func TestSettleLedger(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d)

	// 幂等：同单两次入账一条
	if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: 100, Amount: 300}); err != nil {
		t.Fatal(err)
	}
	if err := r.SettleOrderProfit(ctx, SettleInput{SubsiteID: subsite, OrderID: 100, Amount: 300}); err != nil {
		t.Fatal(err)
	}
	rows, total, _ := r.Ledger(ctx, subsite, "", 1, 10)
	if total != 1 {
		t.Fatalf("幂等失败: %d", total)
	}
	if string(rows[0].Status) != "pending" {
		t.Fatal("初始应冻结")
	}
	// 到期确认
	n, err := r.ConfirmDue(ctx, time.Now().UTC().AddDate(0, 0, 8), 10)
	if err != nil || n != 1 {
		t.Fatalf("确认失败: %d %v", n, err)
	}
	// 缓存与重算一致
	acc, _ := r.GetBalance(ctx, subsite)
	ra, rl, rn, _ := r.RecomputeBalance(ctx, subsite)
	if acc.Available != ra || rl != 0 || rn != 0 {
		t.Fatalf("重算不一致: cache=%d recompute=%d/%d/%d", acc.Available, ra, rl, rn)
	}
	if ra != 300 {
		t.Fatalf("金额错误: %d", ra)
	}
	_ = d
}

// TestSelfPurchaseGuard 防自购三查。
func TestSelfPurchaseGuard(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	subsite := seedApproved(t, r, d) // 站主 user 1

	// 站主自购
	if r.ProfitEligible(ctx, subsite, 1) {
		t.Fatal("站主自购应 profit_eligible=false")
	}
	// 普通买家（user 2，无链）
	if _, err := d.Client.User.Create().SetUsername("buyer").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.ProfitEligible(ctx, subsite, 2) {
		t.Fatal("普通买家应可分账")
	}
	// 买家是站主下级（invite_l1=1）
	if _, err := d.Client.User.Create().SetUsername("sub").SetStatus(user.StatusActive).
		SetInviteL1(1).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if r.ProfitEligible(ctx, subsite, 3) {
		t.Fatal("站主下级购买应拒绝分账")
	}
	// 反向：买家是站主的上级（站主 invite_l1=买家）
	if _, err := d.Client.User.Create().SetUsername("boss").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.User.UpdateOneID(1).SetInviteL1(4).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if r.ProfitEligible(ctx, subsite, 4) {
		t.Fatal("站主上级购买应拒绝分账")
	}
}

// TestApplyDuplicate 重复申请拒绝。
func TestApplyDuplicate(t *testing.T) {
	r, d := newResellerData(t)
	ctx := context.Background()
	if _, err := d.Client.User.Create().SetUsername("u").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(ctx, ApplyInput{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(ctx, ApplyInput{UserID: 1}); err != ErrDuplicateApply {
		t.Fatalf("重复申请应拒绝: %v", err)
	}
}
