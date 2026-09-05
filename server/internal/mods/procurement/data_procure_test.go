package procurement

// 必测项：状态机（非法迁移拒绝/并发汇聚幂等）、到手即加密（零明文断言）、
// 失败策略（auto_refund 退款单 / manual 终态）、轮询退避档位推进。

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderitem"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"
	fulfillmentport "github.com/NovaWorks/zcard-next/server/internal/mods/fulfillment/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	_ "modernc.org/sqlite"
)

func newProcureTestData(t *testing.T) (*ProcureRepo, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open("file:proctest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	return NewProcureRepo(d), d
}

func newTestCipher(t *testing.T) *inventory.CardCipher {
	t.Helper()
	c, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// seedOrderItem 建订单 + 订单项（返回 orderID/itemID）。
func seedOrderItem(t *testing.T, d *data.Data) (uint64, uint64) {
	t.Helper()
	ctx := context.Background()
	o, err := d.Client.Order.Create().
		SetOrderNo("T-ORDER-1").
		SetSubsiteID(0).
		SetStatus("paid").
		SetTotalAmount(1000).
		SetUserID(1).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	it, err := d.Client.OrderItem.Create().
		SetOrderID(o.ID).
		SetProductID(10).
		SetSkuID(0).
		SetQuantity(2).
		SetUnitPrice(1000).
		SetAmount(1000).
		SetFulfillmentType(orderitem.FulfillmentTypeUpstream).
		SetSubsiteID(0).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o.ID, it.ID
}

// fakeGW 内存网关（可编程结果）。
type fakeGW struct {
	submitRes   *supplyport.PurchaseResult
	submitErr   error
	queryRes    *supplyport.PurchaseOrderInfo
	queryErr    error
	submitCalls int
}

func (f *fakeGW) Submit(_ context.Context, _ supplyport.PurchaseRequest) (*supplyport.PurchaseResult, error) {
	f.submitCalls++
	return f.submitRes, f.submitErr
}
func (f *fakeGW) Query(_ context.Context, _ uint64, _ string) (*supplyport.PurchaseOrderInfo, error) {
	return f.queryRes, f.queryErr
}
func (f *fakeGW) CheckStock(_ context.Context, _ uint64, _, _ string) (int32, error) { return 10, nil }
func (f *fakeGW) Refund(_ context.Context, _ uint64, _ string) error              { return nil }
func (f *fakeGW) VerifyUpstreamCallback(_ context.Context, _ uint64, _ *supplyport.UpstreamCallbackAuth) (*supplyport.UpstreamCallbackResult, error) {
	return nil, supplyport.ErrCallbackNotSupported // 测试桩：回调通道不参与断言
}
func (f *fakeGW) FailStrategyOf(_ context.Context, _ uint64) string { return "auto_refund" }

// fakeAttach 记录交付调用（断言密文透传）。
type fakeAttach struct {
	calls int
	first [][]byte
}

func (f *fakeAttach) AttachUpstreamDelivery(_ context.Context, _, _, _ uint64, items []fulfillmentport.UpstreamDeliveryItem) error {
	f.calls++
	if f.calls == 1 {
		for _, it := range items {
			f.first = append(f.first, it.SealedContent)
		}
	}
	return nil
}

// fakeRefund 记录退款调用。
type fakeRefund struct {
	calls  int
	lastID uint64
}

func (f *fakeRefund) RefundOrder(_ context.Context, orderID uint64, _ money.Cents, _ string) error {
	f.calls++
	f.lastID = orderID
	return nil
}

var _ = time.Now

// TestStateMachine 状态机：合法迁移 + 非法拒绝 + 并发汇聚幂等。
func TestStateMachine(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	orderID, itemID := seedOrderItem(t, d)

	po, err := repo.CreatePending(ctx, itemID, 1, "P1", 2, "auto_refund", "T-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = orderID

	// 非法迁移：pending → refunded 拒绝
	if err := repo.MarkRefunded(ctx, po.ID, "R1"); !errors.Is(err, ErrTransitionDenied) {
		t.Fatalf("非法迁移应拒绝, got %v", err)
	}
	// 合法：pending → fulfilled
	if err := repo.MarkFulfilled(ctx, po.ID); err != nil {
		t.Fatal(err)
	}
	// 重复 fulfilled（并发汇聚）→ 返回错误但不产生状态异常（CAS）
	if err := repo.MarkFulfilled(ctx, po.ID); err == nil {
		t.Fatalf("终态重复迁移应报错")
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.Status != procurementorder.StatusFulfilled {
		t.Fatalf("状态错误: %s", po.Status)
	}

	// submitted → polling → manual（24h 卡死路径）
	po2, err := repo.CreatePending(ctx, itemID+100, 1, "P2", 1, "manual", "T-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSubmitted(ctx, po2.ID, "UP-9", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkPolling(ctx, po2.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkManual(ctx, po2.ID, "24h 卡死"); err != nil {
		t.Fatal(err)
	}
}

// TestCreatePendingIdempotent 同 order_item 重复建单拒绝。
func TestCreatePendingIdempotent(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	_, itemID := seedOrderItem(t, d)

	if _, err := repo.CreatePending(ctx, itemID, 1, "P1", 1, "auto_refund", "T"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreatePending(ctx, itemID, 1, "P1", 1, "auto_refund", "T"); !errors.Is(err, ErrDuplicatePurchase) {
		t.Fatalf("重复建单应拒绝, got %v", err)
	}
}

// TestDeliveredEncryptionNoPlaintext 到手即加密：received_content 全密文 + 交付透传密文。
func TestDeliveredEncryptionNoPlaintext(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	orderID, itemID := seedOrderItem(t, d)

	po, err := repo.CreatePending(ctx, itemID, 1, "P1", 2, "auto_refund", "T-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	cipher := newTestCipher(t)
	attach := &fakeAttach{}
	svc := &ProcureService{
		repo: repo, gw: &fakeGW{}, cipher: cipher,
		attach: attach, refund: &fakeRefund{}, log: slog.Default(),
	}
	cards := []string{"CARD-ALPHA-1", "CARD-ALPHA-2"}
	if err := svc.finalizeDelivered(ctx, po.ID, orderID, itemID, 10, 0, cards, 1000); err != nil {
		t.Fatal(err)
	}
	// 采购项密文：必须不含明文
	sealed, err := repo.ReceivedContent(ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 2 {
		t.Fatalf("密文行数错误: %d", len(sealed))
	}
	for _, s := range sealed {
		if string(s) == "CARD-ALPHA-1" || string(s) == "CARD-ALPHA-2" {
			t.Fatal("received_content 出现明文卡密")
		}
		// 可解密还原（AAD 绑定 product=10 subsite=0）
		plain, err := cipher.Open(s, 10, 0)
		if err != nil {
			t.Fatalf("密文无法解密: %v", err)
		}
		if plain != "CARD-ALPHA-1" && plain != "CARD-ALPHA-2" {
			t.Fatalf("解密内容错误: %q", plain)
		}
	}
	// 交付出口：透传的是密文
	if attach.calls != 1 || len(attach.first) != 2 {
		t.Fatalf("交付出口调用错误: calls=%d", attach.calls)
	}
	for _, s := range attach.first {
		if string(s) == "CARD-ALPHA-1" {
			t.Fatal("交付出口透传了明文")
		}
	}
	// 状态终态
	po, _ = repo.Get(ctx, po.ID)
	if po.Status != procurementorder.StatusFulfilled {
		t.Fatalf("状态错误: %s", po.Status)
	}
}

// TestFailStrategyAutoRefund rejected → refunding → 退款单创建。
func TestFailStrategyAutoRefund(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	orderID, itemID := seedOrderItem(t, d)

	po, err := repo.CreatePending(ctx, itemID, 1, "P1", 1, "auto_refund", "T-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRejected(ctx, po.ID, "余额不足"); err != nil {
		t.Fatal(err)
	}
	refund := &fakeRefund{}
	svc := &ProcureService{repo: repo, gw: &fakeGW{}, refund: refund, log: slog.Default()}
	if err := svc.applyFailStrategy(ctx, po.ID, "余额不足"); err != nil {
		t.Fatal(err)
	}
	if refund.calls != 1 || refund.lastID != orderID {
		t.Fatalf("退款传导错误: calls=%d order=%d", refund.calls, refund.lastID)
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.Status != procurementorder.StatusRefunding {
		t.Fatalf("状态应为 refunding: %s", po.Status)
	}
	// refunding → refunded（上游退款回填）
	if err := repo.MarkRefunded(ctx, po.ID, "UP-REF-1"); err != nil {
		t.Fatal(err)
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.UpstreamRefundID != "UP-REF-1" {
		t.Fatalf("上游退款单号未回填: %s", po.UpstreamRefundID)
	}
}

// TestFailStrategyManual manual 终态不触退款。
func TestFailStrategyManual(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	_, itemID := seedOrderItem(t, d)

	po, err := repo.CreatePending(ctx, itemID, 1, "P1", 1, "manual", "T-ORDER-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkRejected(ctx, po.ID, "人工处理"); err != nil {
		t.Fatal(err)
	}
	refund := &fakeRefund{}
	svc := &ProcureService{repo: repo, gw: &fakeGW{}, refund: refund, log: slog.Default()}
	if err := svc.applyFailStrategy(ctx, po.ID, "人工处理"); err != nil {
		t.Fatal(err)
	}
	if refund.calls != 0 {
		t.Fatalf("manual 策略不应退款: %d", refund.calls)
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.Status != procurementorder.StatusManual {
		t.Fatalf("状态应为 manual: %s", po.Status)
	}
}

// TestPollBackoff 轮询退避档位推进（30s → 60s → …耗尽移交巡检）。
func TestPollBackoff(t *testing.T) {
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	_, itemID := seedOrderItem(t, d)

	po, err := repo.CreatePending(ctx, itemID, 1, "P1", 1, "auto_refund", "T")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSubmitted(ctx, po.ID, "UP-1", time.Now().Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	gw := &fakeGW{queryRes: &supplyport.PurchaseOrderInfo{Status: "pending"}}
	svc := &ProcureService{repo: repo, gw: gw, log: slog.Default()}
	if err := svc.PollOne(ctx, po.ID); err != nil {
		t.Fatal(err)
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.RetryCount != 1 {
		t.Fatalf("退避档位应 +1: %d", po.RetryCount)
	}
	// 推满间隔表 → 移交巡检（不标记失败）
	for i := 0; i < len(pollIntervals)+2; i++ {
		_ = svc.PollOne(ctx, po.ID)
	}
	po, _ = repo.Get(ctx, po.ID)
	if po.Status != procurementorder.StatusPolling {
		t.Fatalf("耗尽应移交巡检(polling): %s", po.Status)
	}
}
