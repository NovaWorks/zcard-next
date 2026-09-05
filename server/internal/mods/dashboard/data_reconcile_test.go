package dashboard

// 货源对账测试：四态构造（matched/mismatched/local_only/upstream_only）、
// 任务生命周期（pending → processing → done/failed）、幂等重跑、mismatch 告警一次、
// 上游不支持列表 → failed 可查。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/reconciliationitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/reconciliationjob"
	dashboardport "github.com/NovaWorks/zcard-next/server/internal/mods/dashboard/port"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// fakeSource 对账数据源假实现（内存订单清单 / 可切换不支持）。
type fakeSource struct {
	orders      []dashboardport.UpstreamOrder
	unsupported bool
}

func (f *fakeSource) ListOrders(ctx context.Context, connectionID uint64, start, end time.Time) ([]dashboardport.UpstreamOrder, error) {
	if f.unsupported {
		return nil, dashboardport.ErrUpstreamListUnsupported
	}
	return f.orders, nil
}

// fakeSender 告警收集。
type fakeSender struct{ sent []notifyport.Message }

func (f *fakeSender) Send(ctx context.Context, msg notifyport.Message) error {
	f.sent = append(f.sent, msg)
	return nil
}

// seedProcure 造链：order → order_item(amount) → procurement_order(upstreamID)。
func seedProcure(t *testing.T, d *data.Data, conn uint64, upstreamID string, itemAmount int64, at time.Time) {
	t.Helper()
	ctx := context.Background()
	o, err := d.Client.Order.Create().
		SetOrderNo(fmt.Sprintf("R-%d", at.UnixNano())).
		SetStatus("paid").
		SetTotalAmount(itemAmount).
		SetBaseCurrency("CNY").
		SetCreatedAt(at).
		SetVersion(0).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	it, err := d.Client.OrderItem.Create().
		SetOrderID(o.ID).
		SetProductID(1).
		SetUnitPrice(itemAmount).
		SetQuantity(1).
		SetAmount(itemAmount).
		SetFulfillmentType("upstream").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	create := d.Client.ProcurementOrder.Create().
		SetOrderItemID(it.ID).
		SetConnectionID(conn).
		SetStatus("submitted").
		SetDedupeKey(fmt.Sprintf("dk-%d", at.UnixNano())).
		SetCreatedAt(at)
	if upstreamID != "" {
		create.SetUpstreamOrderID(upstreamID)
	}
	if _, err := create.Save(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileFourStates 四态比对 + 汇总计数 + 告警一次 + 幂等重跑。
func TestReconcileFourStates(t *testing.T) {
	d := newDashboardData(t)
	ctx := context.Background()
	day := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)

	// 本地 3 单：U1 金额一致 / U2 金额不一致 / U3 上游缺失
	seedProcure(t, d, 7, "U1", 1000, day.Add(1*time.Hour))
	seedProcure(t, d, 7, "U2", 2000, day.Add(2*time.Hour))
	seedProcure(t, d, 7, "U3", 3000, day.Add(3*time.Hour))
	// 窗外单不参与
	seedProcure(t, d, 7, "U9", 900, day.Add(48*time.Hour))
	// 未提交上游（无 upstream_order_id）不参与
	seedProcure(t, d, 7, "", 500, day.Add(4*time.Hour))

	src := &fakeSource{orders: []dashboardport.UpstreamOrder{
		{UpstreamOrderID: "U1", Amount: 1000},
		{UpstreamOrderID: "U2", Amount: 2500},
		{UpstreamOrderID: "U4", Amount: 400},
	}}
	sender := &fakeSender{}
	rc := NewReconciler(d, src, sender)

	job, err := rc.CreateJob(ctx, 7, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != reconciliationjob.StatusPending {
		t.Fatalf("初始态错误: %s", job.Status)
	}
	if err := rc.RunJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	got, err := rc.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != reconciliationjob.StatusDone {
		t.Fatalf("终态错误: %s %+v", got.Status, got.ResultJSON)
	}
	if got.MatchedCount != 1 || got.MismatchedCount != 1 || got.TotalCount != 4 {
		t.Fatalf("汇总计数错误: matched=%d mismatched=%d total=%d", got.MatchedCount, got.MismatchedCount, got.TotalCount)
	}
	// 四态明细各 1 行
	for status, want := range map[reconciliationitem.Status]int{
		reconciliationitem.StatusMatched:      1,
		reconciliationitem.StatusMismatched:   1,
		reconciliationitem.StatusLocalOnly:    1,
		reconciliationitem.StatusUpstreamOnly: 1,
	} {
		rows, total, err := rc.ListItems(ctx, job.ID, string(status), 1, 50)
		if err != nil || len(rows) != want || int64(want) != total {
			t.Fatalf("明细 %s 错误: rows=%d total=%d err=%v", status, len(rows), total, err)
		}
	}
	// mismatch 金额差异进 diff_json
	misRows, _, _ := rc.ListItems(ctx, job.ID, string(reconciliationitem.StatusMismatched), 1, 50)
	if len(misRows) != 1 || misRows[0].UpstreamOrderNo != "U2" {
		t.Fatalf("mismatch 明细错误: %+v", misRows)
	}
	// JSON 读回数值为 float64（方言无关断言）
	if diff, ok := misRows[0].DiffJSON["amount"].(map[string]any); !ok || fmt.Sprintf("%.0f", diff["local"]) != "2000" || fmt.Sprintf("%.0f", diff["upstream"]) != "2500" {
		t.Fatalf("diff_json 金额差异缺失: %+v", misRows[0].DiffJSON)
	}

	// 幂等：重跑不重复落明细
	if err := rc.RunJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := d.Client.ReconciliationItem.Query().All(ctx)
	if len(all) != 4 {
		t.Fatalf("重跑产生重复明细: %d", len(all))
	}

	// 差异告警恰好一次
	if len(sender.sent) != 1 || sender.sent[0].EventType != "reconciliation.mismatch" {
		t.Fatalf("告警次数/类型错误: %+v", sender.sent)
	}
}

// TestReconcileUnsupported 上游不支持列表 → failed（原因可查，不静默）。
func TestReconcileUnsupported(t *testing.T) {
	d := newDashboardData(t)
	ctx := context.Background()
	day := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	seedProcure(t, d, 9, "U1", 1000, day.Add(1*time.Hour))

	rc := NewReconciler(d, &fakeSource{unsupported: true}, &fakeSender{})
	job, err := rc.CreateJob(ctx, 9, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.RunJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := rc.GetJob(ctx, job.ID)
	if got.Status != reconciliationjob.StatusFailed {
		t.Fatalf("不支持列表应 failed: %s", got.Status)
	}
	if res, ok := got.ResultJSON["error"].(string); !ok || res == "" {
		t.Fatalf("失败原因缺失: %+v", got.ResultJSON)
	}
	// failed 后不可重跑（幂等直接返回）
	if err := rc.RunJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileRangeInvalid 时间窗校验（end < start / 超 31 天）。
func TestReconcileRangeInvalid(t *testing.T) {
	d := newDashboardData(t)
	rc := NewReconciler(d, &fakeSource{}, &fakeSender{})
	day := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	if _, err := rc.CreateJob(context.Background(), 1, day.Add(24*time.Hour), day); err == nil {
		t.Fatal("end < start 应拒绝")
	}
	if _, err := rc.CreateJob(context.Background(), 1, day, day.Add(32*24*time.Hour)); err == nil {
		t.Fatal("超 31 天应拒绝")
	}
}
