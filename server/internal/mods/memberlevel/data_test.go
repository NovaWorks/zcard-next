package memberlevel

// 测试：积分产生（order.paid → points_rule 入账幂等）+ 等级进度
// （阈值全矩阵 + countAsRecharge 防刷口径）。

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	_ "modernc.org/sqlite"
)

// walletPorts 真实 wallet 仓储的四端口适配（测试内装配，与 providers 同构）。
type walletPorts struct{ repo *wallet.WalletRepoImpl }

func (w walletPorts) PointCreditInTx(ctx context.Context, e walletport.PointEntry) error {
	return w.repo.PointCreditInTx(ctx, wallet.PointEntry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type, Amount: e.Amount,
		Reference: e.Reference, OrderID: e.OrderID, Remark: e.Remark,
	})
}
func (w walletPorts) CumulativeRecharge(ctx context.Context, userID uint64) (int64, error) {
	return w.repo.CumulativeRecharge(ctx, userID)
}
func (w walletPorts) GetPoints(ctx context.Context, userID uint64) (int64, error) {
	return w.repo.GetPoints(ctx, userID)
}

func newMemberLevelEnv(t *testing.T) (*data.Data, *MemberLevelRepoImpl, *PointsService, *wallet.WalletRepoImpl) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:mltest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	wrepo := wallet.NewWalletRepoImpl(d)
	repo := NewMemberLevelRepoImpl(d, walletPorts{repo: wrepo})
	svc := NewPointsService(repo, walletPorts{repo: wrepo}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return d, repo, svc, wrepo
}

// TestPointsEarnOnOrderPaid 消费产生积分：规则命中入账 + 幂等重放 + 口径豁免。
func TestPointsEarnOnOrderPaid(t *testing.T) {
	d, repo, svc, wrepo := newMemberLevelEnv(t)
	ctx := context.Background()

	// VIP：累计消费 ≥1000；规则 消费 100 分（1 元）产 1 分
	if _, err := repo.CreateLevel(ctx, "VIP", "consume", 0, 1000, 500, 1, true,
		map[string]any{"spend_cents": int64(100), "points": int64(1)}); err != nil {
		t.Fatal(err)
	}
	// 用户已消费 2500 分（25 元）
	if _, err := d.Client.Order.Create().
		SetOrderNo("P-1").SetUserID(9).SetStatus(order.StatusPaid).
		SetTotalAmount(2500).SetBaseCurrency("CNY").SetVersion(0).Save(ctx); err != nil {
		t.Fatal(err)
	}

	env := events.Envelope{Payload: []byte(`{"order_id":501,"user_id":9,"total_cents":2500}`)}
	if err := svc.OnOrderPaid(ctx, env); err != nil {
		t.Fatal(err)
	}
	bal, _ := wrepo.GetPoints(ctx, 9)
	if bal != 25 {
		t.Fatalf("积分入账错误: %d (want 25)", bal)
	}
	// 幂等：重放同一事件不重复入账（reference=points:501）
	if err := svc.OnOrderPaid(ctx, env); err != nil {
		t.Fatal(err)
	}
	bal, _ = wrepo.GetPoints(ctx, 9)
	if bal != 25 {
		t.Fatalf("重放重复入账: %d", bal)
	}

	// 口径豁免：游客 / total=0（积分兑换单）不入账
	for _, raw := range []string{
		`{"order_id":502,"user_id":0,"total_cents":2500}`,
		`{"order_id":503,"user_id":9,"total_cents":0}`,
	} {
		if err := svc.OnOrderPaid(ctx, events.Envelope{Payload: []byte(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	bal, _ = wrepo.GetPoints(ctx, 9)
	if bal != 25 {
		t.Fatalf("豁免口径错误: %d", bal)
	}
}

// TestResolveProgress 等级进度：当前级/下一级/差额/百分比 + countAsRecharge 防刷。
func TestResolveProgress(t *testing.T) {
	d, repo, _, wrepo := newMemberLevelEnv(t)
	ctx := context.Background()

	// 阶梯：L1 消费 ≥1000；L2 双条件（充值 ≥5000 且消费 ≥5000）
	if _, err := repo.CreateLevel(ctx, "L1", "consume", 0, 1000, 0, 1, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateLevel(ctx, "L2", "both_and", 5000, 5000, 800, 2, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.User.Create().SetUsername("u1").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}

	// 真实充值 3000（计入）+ 调账 9000（不计入——防小号互转刷级）
	if err := wrepo.CreditInTx(ctx, wallet.Entry{
		UserID: 1, Direction: "in", Type: "recharge", Amount: 3000, Reference: "r1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := wrepo.CreditInTx(ctx, wallet.Entry{
		UserID: 1, Direction: "in", Type: "adjust", Amount: 9000, Reference: "a1",
	}); err != nil {
		t.Fatal(err)
	}
	// 消费 2000
	if _, err := d.Client.Order.Create().
		SetOrderNo("P-2").SetUserID(1).SetStatus(order.StatusPaid).
		SetTotalAmount(2000).SetBaseCurrency("CNY").SetVersion(0).Save(ctx); err != nil {
		t.Fatal(err)
	}

	p, err := repo.ResolveProgress(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// countAsRecharge：recharged=3000（9000 调账不计）
	if p.RechargedCents != 3000 {
		t.Fatalf("累计充值口径错误: %d (want 3000)", p.RechargedCents)
	}
	if p.Current == nil || p.Current.Name != "L1" {
		t.Fatalf("当前级错误: %+v", p.Current)
	}
	if p.Next == nil || p.Next.Name != "L2" {
		t.Fatalf("下一级错误: %+v", p.Next)
	}
	// 差额：充值差 2000、消费差 3000；百分比 = min(3000/5000, 2000/5000) = 40
	if p.RechargeGap != 2000 || p.ConsumeGap != 3000 || p.Percent != 40 {
		t.Fatalf("进度差额错误: rg=%d cg=%d pct=%d", p.RechargeGap, p.ConsumeGap, p.Percent)
	}

	// 折扣解析与当前级一致（管线步骤 2 同源）
	rate, levelID, err := repo.EffectiveRate(ctx, 1)
	if err != nil || rate != 0 || levelID != p.Current.ID {
		t.Fatalf("EffectiveRate 与进度不一致: rate=%d lvl=%d err=%v", rate, levelID, err)
	}
}

// TestProgressFreshUser 未消费用户：无当前级、下一级=首级。
func TestProgressFreshUser(t *testing.T) {
	_, repo, _, _ := newMemberLevelEnv(t)
	ctx := context.Background()
	if _, err := repo.CreateLevel(ctx, "L1", "consume", 0, 1000, 0, 1, true, nil); err != nil {
		t.Fatal(err)
	}
	p, err := repo.ResolveProgress(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if p.Current != nil || p.Next == nil || p.Next.Name != "L1" {
		t.Fatalf("未入门进度错误: current=%v next=%v", p.Current, p.Next)
	}
}
