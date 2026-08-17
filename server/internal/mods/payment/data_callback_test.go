package payment

// M1b 交易闭环核心测试：支付回调分流（订单型/充值型）+ 余额支付扣款 + 充值到账。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rechargeorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	_ "modernc.org/sqlite"
)

// captureWriter 捕获 outbox 事件（验证事件载荷契约）。
type captureWriter struct{ evts []capturedEvent }

type capturedEvent struct {
	typ     string
	payload json.RawMessage
}

func (w *captureWriter) Write(ctx context.Context, module, typ, aggregateID, dedupeKey string, payload json.RawMessage) error {
	w.evts = append(w.evts, capturedEvent{typ: typ, payload: payload})
	return nil
}

func (w *captureWriter) has(typ string) bool {
	for _, e := range w.evts {
		if e.typ == typ {
			return true
		}
	}
	return false
}

// fakeLifecycle 订单生命周期假实现（记录 MarkPaid 调用；真实状态机由 order 模块测试覆盖）。
type fakeLifecycle struct {
	markPaidCalls []string
}

func (f *fakeLifecycle) MarkPaid(ctx context.Context, fact orderport.PaidFact) error {
	f.markPaidCalls = append(f.markPaidCalls, fact.OrderNo)
	return nil
}

func (f *fakeLifecycle) Cancel(ctx context.Context, orderNo, reason string, op orderport.Operator) error {
	return nil
}

// newCallbackEnv 回调测试环境：内存 SQLite + 渠道（external/wallet）+ 用户 + 余额。
func newCallbackEnv(t *testing.T) (*data.Data, *PaymentRepoImpl, *wallet.WalletRepoImpl, *captureWriter, *fakeLifecycle) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:paytest%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	ctx := context.Background()

	// 用户
	if _, err := client.User.Create().SetUsername("u1").SetStatus(user.StatusActive).Save(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	// 外部渠道（epay driver；凭据加密存储，测试配置 pid/key）
	epayCfg, _ := box.Seal([]byte(`{"pid":"1000","key":"testkey"}`), []byte("payment_channel:epay"))
	if _, err := client.PaymentChannel.Create().
		SetName("易支付").SetCode("epay").SetDriver("epay").
		SetConfig(epayCfg).SetEnabled(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	// 余额渠道（wallet driver）
	if _, err := client.PaymentChannel.Create().
		SetName("余额").SetCode("balance").SetDriver("wallet").
		SetConfig([]byte("{}")).SetEnabled(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	walletRepo := wallet.NewWalletRepoImpl(d)
	walletPort := wallet.ProvidePortWallet(walletRepo)
	writer := &captureWriter{}
	lifecycle := &fakeLifecycle{}
	repo := NewPaymentRepoImpl(d, box, NewRegistry(), lifecycle, walletPort, wallet.ProvidePortPoints(walletRepo), writer)
	return d, repo, walletRepo, writer, lifecycle
}

// seedPendingOrder 建待支付订单 + 外部渠道支付单。
func seedPendingOrder(t *testing.T, d *data.Data, channel string, amount int64) (*ent.Order, *ent.Payment) {
	t.Helper()
	ctx := context.Background()
	o, err := d.Client.Order.Create().
		SetOrderNo(fmt.Sprintf("S%d", time.Now().UnixNano())).
		SetSubsiteID(0).
		SetUserID(1).
		SetStatus(order.StatusPendingPayment).
		SetTotalAmount(amount).
		SetBaseCurrency("CNY").
		SetVersion(0).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Client.Payment.Create().
		SetOrderID(o.ID).SetChannel(channel).SetAmount(amount).
		SetStatus("pending").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o, p
}

// TestCallbackOrderBranch 订单型回调：payment success + lifecycle.MarkPaid 调用。
func TestCallbackOrderBranch(t *testing.T) {
	d, repo, _, writer, lifecycle := newCallbackEnv(t)
	ctx := context.Background()
	o, p := seedPendingOrder(t, d, "epay", 1000)

	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "T123", OrderNo: o.OrderNo,
		Amount: 1000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.Payment.Get(ctx, p.ID)
	if string(got.Status) != "success" || got.ChannelOrderNo != "T123" {
		t.Fatalf("支付单状态错误: %+v", got)
	}
	if len(lifecycle.markPaidCalls) != 1 || lifecycle.markPaidCalls[0] != o.OrderNo {
		t.Fatalf("MarkPaid 应调用一次: %+v", lifecycle.markPaidCalls)
	}
	// 幂等：已 success 直接 ACK，不重复 MarkPaid（重复回调不产生副作用）
	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "T123", OrderNo: o.OrderNo,
		Amount: 1000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.markPaidCalls) != 1 {
		t.Fatalf("幂等失败: %+v", lifecycle.markPaidCalls)
	}
	_ = writer

	// 金额篡改 → 拒绝（用新 pending 支付单验证四重校验——已 success 单走幂等 ACK）
	_, p2 := seedPendingOrder(t, d, "epay", 1000)
	if err := repo.HandleCallback(ctx, p2.ID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "T124", OrderNo: o.OrderNo,
		Amount: 1, Currency: "CNY", Success: true,
	}); err == nil {
		t.Fatal("篡改金额应拒绝")
	}
	// 篡改单保持 pending（事务回滚）
	got2, _ := d.Client.Payment.Get(ctx, p2.ID)
	if string(got2.Status) != "pending" {
		t.Fatalf("篡改拒绝后支付单应保持 pending: %s", got2.Status)
	}
}

// TestCallbackRechargeBranch 充值型回调：充值单 success + 余额入账（含赠送）+ 事件 + 幂等。
func TestCallbackRechargeBranch(t *testing.T) {
	d, repo, walletRepo, writer, _ := newCallbackEnv(t)
	ctx := context.Background()
	ro, err := d.Client.RechargeOrder.Create().
		SetUserID(1).SetAmount(10000).SetGiftAmount(500).
		SetStatus(rechargeorder.StatusPending).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Client.Payment.Create().
		SetRechargeOrderID(ro.ID).SetChannel("epay").SetAmount(10000).
		SetStatus("pending").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "R1", Amount: 10000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Client.RechargeOrder.Get(ctx, ro.ID)
	if string(got.Status) != "success" || got.PaymentID != p.ID {
		t.Fatalf("充值单状态错误: %+v", got)
	}
	avail, _, _ := walletRepo.GetBalance(ctx, 1)
	if avail != 10500 {
		t.Fatalf("充值到账错误: %d (want 10500 = 本金+赠送)", avail)
	}
	if !writer.has(events.RechargeSucceeded) {
		t.Fatal("应发布 recharge.succeeded 事件")
	}
	// 幂等重放：不重复入账
	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "R1", Amount: 10000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	avail, _, _ = walletRepo.GetBalance(ctx, 1)
	if avail != 10500 {
		t.Fatalf("幂等重放重复入账: %d", avail)
	}
}

// TestCallbackWalletBalance 余额渠道订单支付：事务内扣款 + MarkPaid。
func TestCallbackWalletBalance(t *testing.T) {
	d, repo, walletRepo, _, lifecycle := newCallbackEnv(t)
	ctx := context.Background()
	// 预存余额 5000
	if err := walletRepo.CreditInTx(ctx, wallet.Entry{
		UserID: 1, Direction: "in", Type: "adjust", Amount: 5000, Reference: "seed:1",
	}); err != nil {
		t.Fatal(err)
	}
	o, p := seedPendingOrder(t, d, "balance", 3000)
	if err := repo.HandleCallback(ctx, p.ID, CallbackFact{
		Channel: "balance", ChannelOrderNo: "W1", OrderNo: o.OrderNo,
		Amount: 3000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	avail, _, _ := walletRepo.GetBalance(ctx, 1)
	if avail != 2000 {
		t.Fatalf("余额扣款错误: %d (want 2000)", avail)
	}
	if len(lifecycle.markPaidCalls) != 1 {
		t.Fatalf("MarkPaid 应调用: %+v", lifecycle.markPaidCalls)
	}
	// 余额不足：拒绝（用新支付单；余额 2000 需付 9999）
	_, p2 := seedPendingOrder(t, d, "balance", 9999)
	if err := repo.HandleCallback(ctx, p2.ID, CallbackFact{
		Channel: "balance", ChannelOrderNo: "W2", OrderNo: o.OrderNo,
		Amount: 9999, Currency: "CNY", Success: true,
	}); err == nil {
		t.Fatal("余额不足应拒绝")
	}
	// 扣款失败事务回滚：余额不变，支付单回 pending，订单不推进
	avail, _, _ = walletRepo.GetBalance(ctx, 1)
	if avail != 2000 {
		t.Fatalf("失败路径余额被改动: %d", avail)
	}
	got2, _ := d.Client.Payment.Get(ctx, p2.ID)
	if string(got2.Status) != "pending" {
		t.Fatalf("失败路径支付单应回滚 pending: %s", got2.Status)
	}
	if len(lifecycle.markPaidCalls) != 1 {
		t.Fatalf("失败路径不应推进订单: %+v", lifecycle.markPaidCalls)
	}
}

var _ = paymentchannel.FieldCode
var _ = time.Now

// TestCreateRechargePaymentFlow 充值支付端到端：payer 建单+渠道发起 → 回调 → 到账。
func TestCreateRechargePaymentFlow(t *testing.T) {
	d, repo, walletRepo, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	ro, err := d.Client.RechargeOrder.Create().
		SetUserID(1).SetAmount(10000).SetGiftAmount(0).
		SetStatus(rechargeorder.StatusPending).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := repo.CreateRechargePayment(ctx, ro.ID, "epay", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if info.PaymentID == 0 || info.Type != "params" || info.Payload == "" {
		t.Fatalf("支付发起结果错误: %+v", info)
	}
	// 回调 → 到账
	if err := repo.HandleCallback(ctx, info.PaymentID, CallbackFact{
		Channel: "epay", ChannelOrderNo: "RCH-1", Amount: 10000, Currency: "CNY", Success: true,
	}); err != nil {
		t.Fatal(err)
	}
	avail, _, _ := walletRepo.GetBalance(ctx, 1)
	if avail != 10000 {
		t.Fatalf("充值到账错误: %d", avail)
	}
	// 余额渠道充值拒绝
	if _, err := repo.CreateRechargePayment(ctx, ro.ID, "balance", 10000); err == nil {
		t.Fatal("充值不应支持余额渠道")
	}
}

// TestRechargeGiftPoints 充值赠送积分接线（回调事务内积分入账，幂等重放只入一次）。
func TestRechargeGiftPoints(t *testing.T) {
	d, repo, walletRepo, _, _ := newCallbackEnv(t)
	ctx := context.Background()
	ro, err := d.Client.RechargeOrder.Create().
		SetUserID(1).SetAmount(10000).SetGiftAmount(0).SetGiftPoints(200).
		SetStatus("pending").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Client.Payment.Create().
		SetRechargeOrderID(ro.ID).SetChannel("epay").SetAmount(10000).
		SetStatus("pending").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fact := CallbackFact{Channel: "epay", ChannelOrderNo: "R1", Amount: 10000, Currency: "CNY", Success: true}
	if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
		t.Fatal(err)
	}
	pts, err := walletRepo.GetPoints(ctx, 1)
	if err != nil || pts != 200 {
		t.Fatalf("赠送积分未入账: %d %v", pts, err)
	}
	// 幂等重放：积分不重复入账
	if err := repo.HandleCallback(ctx, p.ID, fact); err != nil {
		t.Fatal(err)
	}
	pts, _ = walletRepo.GetPoints(ctx, 1)
	if pts != 200 {
		t.Fatalf("幂等重放重复入账积分: %d", pts)
	}
}

// TestParseCallbackFormJSONNumber JSON 回调体数字字面保持（P2-09 epusdt）：
// 10.00 必须保持 "10.00" 而非塌成 "10"——验签按原文重算（签名不变式 §5.5）。
func TestParseCallbackFormJSONNumber(t *testing.T) {
	body := []byte(`{"order_id":"S123","amount":10.00,"status":2,"note":"x"}`)
	r, _ := http.NewRequest(http.MethodPost, "/payments/callback/epusdt", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	m, err := parseCallbackForm(r, body)
	if err != nil {
		t.Fatal(err)
	}
	if m["amount"] != "10.00" {
		t.Fatalf("数字字面被破坏: %q（应为 10.00）", m["amount"])
	}
	if m["status"] != "2" || m["order_id"] != "S123" {
		t.Fatalf("字段解析错位: %+v", m)
	}
	// 字符串字段原样
	if m["note"] != "x" {
		t.Fatalf("字符串字段错位: %q", m["note"])
	}
}
