package procurement

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"strings"
	"testing"
	"time"
)

func smsEnv(t *testing.T) (*ProcureService, *data.Data, *ent.SMSIntent) {
	t.Helper()
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Order.UpdateOneID(oid).SetStatus("paid").SetBaseCurrency("CNY").ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(iid).SetQuantity(1).SetDeliveryKind("sms_activation").ExecX(ctx)
	req := supplyport.SMSPurchase{ProductID: "opaque", Quantity: 1, DownstreamOrderNo: "sms_fixed", RequiredCapability: supplyport.SMSCapability, Currency: "CNY", MaxSupplyAmountCents: 123}
	raw, _ := json.Marshal(req)
	row := d.Client.SMSIntent.Create().SetOrderID(oid).SetOrderItemID(iid).SetUserID(1).SetConnectionID(9).SetConnectionIdentity("original").SetRequestNo(req.DownstreamOrderNo).SetRequestJSON(string(raw)).SetRequestHash(fmt.Sprintf("%x", sha256.Sum256(raw))).SetUpstreamOrderID("order-A").SetChargedAmount(123).SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SetPhase("query").SaveX(ctx)
	return &ProcureService{repo: repo, cipher: newTestCipher(t)}, d, row
}
func TestSMSSnapshotMonotonicEncryptionAndTerminalSettlement(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	snap := &supplyport.SMSSnapshot{SessionID: "opaque", State: "sms_received", Version: 2, SMSRevision: 1, OTPMessage: "短信纯文字", PhoneNumber: "+001-XY", Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}
	if e := s.smsSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	saved := d.Client.SMSIntent.GetX(ctx, row.ID)
	if strings.Contains(string(saved.SnapshotCipher), "短信") || strings.Contains(string(saved.SnapshotCipher), "001") {
		t.Fatal("plaintext stored")
	}
	if !saved.Received || d.Client.Order.GetX(ctx, row.OrderID).Status != "delivered" {
		t.Fatal("body-only SMS not delivered")
	}
	d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	old := *snap
	old.Version = 1
	old.OTPMessage = "old"
	if e := s.smsSnapshot(ctx, row, &old); e != nil {
		t.Fatal(e)
	}
	if d.Client.SMSIntent.GetX(ctx, row.ID).Version != 2 {
		t.Fatal("version regressed")
	}
	d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	terminal := *snap
	terminal.Version = 3
	terminal.State = "expired"
	terminal.OTPMessage = ""
	if e := s.smsSnapshot(ctx, row, &terminal); e != nil {
		t.Fatal(e)
	}
	saved = d.Client.SMSIntent.GetX(ctx, row.ID)
	if saved.Phase != "done" || saved.RetailRefundState != "none" {
		t.Fatal("paid expiry incorrectly refunded")
	}
	plain, e := s.cipher.Open(saved.SnapshotCipher, row.OrderItemID, row.SubsiteID)
	if e != nil || !strings.Contains(plain, "短信纯文字") {
		t.Fatal("aggregate erased")
	}
}
func TestSMSInvalidMoneyNeverTriggersRetailRefund(t *testing.T) {
	for _, kind := range []string{"partial", "currency", "reference", "unknown", "empty_completed"} {
		t.Run(kind, func(t *testing.T) {
			s, d, row := smsEnv(t)
			ctx := context.Background()
			snap := &supplyport.SMSSnapshot{SessionID: "s", State: "canceled", Version: 1, Currency: "CNY", PaidAmountCents: 123, RefundedAmountCents: 123, SettlementState: "refunded", RefundReference: "stable"}
			switch kind {
			case "partial":
				snap.RefundedAmountCents = 122
			case "currency":
				snap.Currency = "USD"
			case "reference":
				snap.RefundReference = ""
			case "unknown":
				snap.State = "unknown"
			case "empty_completed":
				snap.State = "completed"
				snap.SettlementState = "paid"
				snap.RefundedAmountCents = 0
			}
			if e := s.smsSnapshot(ctx, row, snap); e == nil {
				t.Fatal("invalid snapshot accepted")
			}
			if d.Client.SMSIntent.GetX(ctx, row.ID).RetailRefundState != "none" {
				t.Fatal("refund incorrectly queued")
			}
		})
	}
}

type normalizedSMSClient struct {
	purchaseCalls int
	lost          bool
	actionStatus  string
	actionIDs     []string
}

func (f *normalizedSMSClient) CreateSMS(_ context.Context, r supplyport.SMSPurchase) (*supplyport.SMSOrder, error) {
	f.purchaseCalls++
	if f.lost && f.purchaseCalls == 1 {
		return nil, fmt.Errorf("response lost after accept")
	}
	if r.DownstreamOrderNo != "sms_fixed" {
		return nil, fmt.Errorf("intent changed")
	}
	return &supplyport.SMSOrder{SupplyOrderID: "original", Status: "fulfilling", Charged: true, Amount: 123}, nil
}
func (f *normalizedSMSClient) QuerySMS(context.Context, []string) ([]supplyport.SMSOrder, error) {
	return nil, nil
}
func (f *normalizedSMSClient) ActSMS(_ context.Context, _ string, _ string, id string) (*supplyport.SMSOperation, error) {
	f.actionIDs = append(f.actionIDs, id)
	return &supplyport.SMSOperation{OperationID: id, Status: f.actionStatus}, nil
}
func TestSMSPurchaseLostResponseKeepsIntentAndLeaseFence(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SMSIntent.UpdateOneID(row.ID).SetPhase("purchase").ClearUpstreamOrderID().SetChargedAmount(0).ExecX(ctx)
	row = d.Client.SMSIntent.GetX(ctx, row.ID)
	f := &normalizedSMSClient{lost: true}
	s.smsPurchase(ctx, row, f)
	saved := d.Client.SMSIntent.GetX(ctx, row.ID)
	if saved.Phase != "purchase" || saved.UpstreamOrderID != nil || saved.RetailRefundState != "none" {
		t.Fatal("unknown outcome treated as rejection")
	}
	d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseToken("lease2").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	row = d.Client.SMSIntent.GetX(ctx, row.ID)
	s.smsPurchase(ctx, row, f)
	saved = d.Client.SMSIntent.GetX(ctx, row.ID)
	if saved.Phase != "query" || *saved.UpstreamOrderID != "original" || f.purchaseCalls != 2 {
		t.Fatal("did not recover original receipt")
	}
	stale := *row
	stale.LeaseToken = "stale"
	s.smsReview(ctx, &stale, "stale")
	if d.Client.SMSIntent.GetX(ctx, row.ID).Phase != "query" {
		t.Fatal("stale worker committed")
	}
}
func TestSMSRejectedOperationStopsPendingAndPreservesSession(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	op := d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("fixed-operation").SetAction("cancel").SaveX(ctx)
	f := &normalizedSMSClient{actionStatus: "pending"}
	if e := s.smsReplayOperation(ctx, row, f); e != nil {
		t.Fatal(e)
	}
	f.actionStatus = "rejected"
	if e := s.smsReplayOperation(ctx, row, f); e != nil {
		t.Fatal(e)
	}
	if d.Client.SMSOperation.GetX(ctx, op.ID).Status != "rejected" || f.actionIDs[0] != f.actionIDs[1] {
		t.Fatal("operation did not converge")
	}
	if d.Client.SMSIntent.GetX(ctx, row.ID).Phase != "query" {
		t.Fatal("operation rejection terminated session")
	}
}

type smsWorkerGateway struct {
	supplyport.UpstreamGateway
	supplyport.SMSGateway
	client supplyport.SMSClient
	opens  int
}

func (g *smsWorkerGateway) OpenSMS(context.Context, uint64, string) (supplyport.SMSClient, error) {
	g.opens++
	return g.client, nil
}
func TestSMSWorkerRecoversWithoutSalesAndRespectsAccountLease(t *testing.T) {
	t.Setenv("ZCARD_SMS_SALES_ENABLED", "false")
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SupplyConnection.Create().SetID(row.ConnectionID).SetName("original").SetDriver("zcard").SetBaseURL("https://example.invalid").SetCredentials([]byte("sealed")).SetSmsLeaseUntil(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	d.Client.SMSIntent.UpdateOneID(row.ID).SetPhase("purchase").ClearUpstreamOrderID().SetChargedAmount(0).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	f := &normalizedSMSClient{}
	g := &smsWorkerGateway{client: f}
	s.gw = g
	s.RunSMS(ctx)
	if g.opens != 0 || f.purchaseCalls != 0 {
		t.Fatal("account lease bypassed")
	}
	d.Client.SupplyConnection.UpdateOneID(row.ConnectionID).SetSmsLeaseUntil(0).ExecX(ctx)
	s.RunSMS(ctx)
	saved := d.Client.SMSIntent.GetX(ctx, row.ID)
	if g.opens != 1 || f.purchaseCalls != 1 || saved.Phase != "query" {
		t.Fatal("disabled sales prevented historical recovery")
	}
	s.RunSMS(ctx)
	if f.purchaseCalls != 1 {
		t.Fatal("task acquired before next scheduled run")
	}
	if d.Client.SupplyConnection.GetX(ctx, row.ConnectionID).SmsLeaseUntil != 0 {
		t.Fatal("account lease not released")
	}
}

type smsFault struct {
	status int
	code   string
	delay  time.Duration
}

func (f smsFault) Error() string                            { return "safe failure" }
func (f smsFault) SMSFailure() (int, string, time.Duration) { return f.status, f.code, f.delay }
func TestSMSRetryAfterAndConflictDisposition(t *testing.T) {
	for _, kind := range []string{"rate_limit", "allocating", "concurrent_update", "identity_conflict", "action_pending"} {
		t.Run(kind, func(t *testing.T) {
			s, d, row := smsEnv(t)
			ctx := context.Background()
			f := smsFault{status: 429, delay: 2 * time.Minute}
			switch kind {
			case "concurrent_update":
				f = smsFault{status: 409, code: "order.CONCURRENT_UPDATE"}
			case "allocating":
				f = smsFault{status: 409, code: "supply.ALLOCATING"}
			case "identity_conflict":
				f = smsFault{status: 409, code: "supply.IDEMPOTENCY_CONFLICT"}
			case "action_pending":
				f = smsFault{status: 409, code: "supply.ACTION_PENDING"}
			}
			s.smsFailure(ctx, row, f)
			saved := d.Client.SMSIntent.GetX(ctx, row.ID)
			if kind == "rate_limit" && saved.NextRunAt < time.Now().Add(119*time.Second).Unix() {
				t.Fatal("Retry-After ignored")
			}
			review := kind == "identity_conflict" || kind == "action_pending"
			if (saved.Phase == "review") != review {
				t.Fatalf("wrong disposition %s", saved.Phase)
			}
			if saved.RetailRefundState != "none" {
				t.Fatal("transport failure caused refund")
			}
		})
	}
}

type mixedSourceSMS struct {
	normalizedSMSClient
	calls [][]string
}

func (f *mixedSourceSMS) QuerySMS(_ context.Context, ids []string) ([]supplyport.SMSOrder, error) {
	f.calls = append(f.calls, append([]string(nil), ids...))
	for _, id := range ids {
		if id == "B-string-id" {
			return nil, fmt.Errorf("source B temporarily unavailable")
		}
	}
	out := []supplyport.SMSOrder{}
	for _, id := range ids {
		out = append(out, supplyport.SMSOrder{SupplyOrderID: id, DownstreamOrderNo: "sms_fixed", Amount: 123, Fulfillment: &supplyport.SMSFulfillment{Kind: supplyport.SMSDelivery, SMS: &supplyport.SMSSnapshot{SessionID: "opaque-A", State: "sms_received", Version: 1, SMSRevision: 1, PhoneNumber: "001 A", OTPCode: "00AB", Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}}})
	}
	return out, nil
}
func TestSMSMixedSourcesIsolateFailuresAndCancelRace(t *testing.T) {
	s, d, a := smsEnv(t)
	ctx := context.Background()
	b := d.Client.SMSIntent.Create().SetOrderID(a.OrderID + 1).SetOrderItemID(a.OrderItemID + 1).SetUserID(1).SetConnectionID(a.ConnectionID).SetConnectionIdentity(a.ConnectionIdentity).SetRequestNo("sms_B").SetRequestJSON("{}").SetRequestHash("hash").SetUpstreamOrderID("B-string-id").SetChargedAmount(123).SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SetPhase("query").SaveX(ctx)
	// Cancellation was sent before a new SMS arrived. Its rejection must not erase the SMS.
	d.Client.SMSOperation.Create().SetIntentID(a.ID).SetOperationID("original-cancel").SetAction("cancel").SaveX(ctx)
	f := &mixedSourceSMS{normalizedSMSClient: normalizedSMSClient{actionStatus: "rejected"}}
	s.smsQueryBatch(ctx, f, []*ent.SMSIntent{a, b})
	aa, bb := d.Client.SMSIntent.GetX(ctx, a.ID), d.Client.SMSIntent.GetX(ctx, b.ID)
	if len(f.calls) != 3 || !aa.Received || aa.Phase != "query" || aa.RetailRefundState != "none" || bb.Attempts != 1 || bb.Received {
		t.Fatal("one source blocked/contaminated another")
	}
	if d.Client.SMSOperation.Query().OnlyX(ctx).Status != "rejected" {
		t.Fatal("cancel race not reconciled")
	}
	plain, e := s.cipher.Open(aa.SnapshotCipher, aa.OrderItemID, aa.SubsiteID)
	if e != nil || !strings.Contains(plain, "00AB") {
		t.Fatal("OTP semantics changed", e)
	}
}

type reviewSMSClient struct {
	normalizedSMSClient
	queryErr     error
	actionErr    error
	queries      int
	snapshot     *supplyport.SMSSnapshot
	beforeAction func()
}

func (f *reviewSMSClient) QuerySMS(_ context.Context, ids []string) ([]supplyport.SMSOrder, error) {
	f.queries++
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	out := []supplyport.SMSOrder{}
	for _, id := range ids {
		out = append(out, supplyport.SMSOrder{SupplyOrderID: id, DownstreamOrderNo: "sms_fixed", Amount: 123, Fulfillment: &supplyport.SMSFulfillment{Kind: supplyport.SMSDelivery, SMS: f.snapshot}})
	}
	return out, nil
}
func (f *reviewSMSClient) ActSMS(context.Context, string, string, string) (*supplyport.SMSOperation, error) {
	if f.beforeAction != nil {
		f.beforeAction()
	}
	return nil, f.actionErr
}
func TestSMSBatchRateLimitDoesNotFanOut(t *testing.T) {
	s, d, a := smsEnv(t)
	ctx := context.Background()
	b := d.Client.SMSIntent.Create().SetOrderID(a.OrderID + 1).SetOrderItemID(a.OrderItemID + 1).SetUserID(1).SetConnectionID(a.ConnectionID).SetConnectionIdentity(a.ConnectionIdentity).SetRequestNo("B").SetRequestJSON("{}").SetRequestHash("h").SetUpstreamOrderID("B").SetChargedAmount(123).SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SetPhase("query").SaveX(ctx)
	f := &reviewSMSClient{queryErr: smsFault{status: 429, delay: time.Minute}}
	s.smsQueryBatch(ctx, f, []*ent.SMSIntent{a, b})
	if f.queries != 1 {
		t.Fatalf("rate-limit amplified into %d calls", f.queries)
	}
	for _, id := range []uint64{a.ID, b.ID} {
		if d.Client.SMSIntent.GetX(ctx, id).NextRunAt < time.Now().Add(59*time.Second).Unix() {
			t.Fatal("missing batch backoff")
		}
	}
}
func TestSMSExpiredWorkerCannotOverwriteOperation(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	op := d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("cancel").SetAttempts(12).SaveX(ctx)
	f := &reviewSMSClient{actionErr: fmt.Errorf("old network failure"), beforeAction: func() {
		d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseToken("new-worker").ExecX(ctx)
		d.Client.SMSOperation.UpdateOneID(op.ID).SetStatus("succeeded").ExecX(ctx)
	}}
	_ = s.smsReplayOperation(ctx, row, f)
	if d.Client.SMSOperation.GetX(ctx, op.ID).Status != "succeeded" {
		t.Fatal("stale failure overwrote confirmed operation")
	}
}
func TestSMSOperationFailureDoesNotBlockConfirmedRefund(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("cancel").SaveX(ctx)
	f := &reviewSMSClient{actionErr: smsFault{status: 409, code: "supply.ACTION_PENDING"}, snapshot: &supplyport.SMSSnapshot{SessionID: "s", State: "canceled", Version: 1, Currency: "CNY", PaidAmountCents: 123, RefundedAmountCents: 123, SettlementState: "refunded", RefundReference: "confirmed"}}
	s.smsQueryBatch(ctx, f, []*ent.SMSIntent{row})
	if d.Client.SMSIntent.GetX(ctx, row.ID).Phase != "refund" {
		t.Fatal("operation error blocked verified retail refund")
	}
}
func TestSMSTerminalOperationReviewRemainsRecoverable(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("finish").SetStatus("review").SaveX(ctx)
	snap := &supplyport.SMSSnapshot{SessionID: "s", State: "completed", Version: 1, SMSRevision: 1, OTPMessage: "text", Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}
	if e := s.smsSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	if d.Client.SMSIntent.GetX(ctx, row.ID).Phase != "review" {
		t.Fatal("unfinished operation hidden in completed task")
	}
}

func TestSMSCancelledWorkerRetriesInsteadOfReview(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("cancel").SaveX(ctx)
	f := &reviewSMSClient{actionErr: context.Canceled, beforeAction: cancel, snapshot: &supplyport.SMSSnapshot{SessionID: "s", State: "waiting_sms", Version: 1, Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}}
	s.smsQueryBatch(ctx, f, []*ent.SMSIntent{row})
	if saved := d.Client.SMSIntent.GetX(context.Background(), row.ID); saved.Phase != "query" || saved.Attempts != 1 {
		t.Fatalf("ordinary cancellation requires manual recovery: %s %d", saved.Phase, saved.Attempts)
	}
}

func TestSMSOperationBackoffAccumulatesAcrossHealthySnapshots(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SMSOperation.Create().SetIntentID(row.ID).SetOperationID("original").SetAction("cancel").SaveX(ctx)
	f := &reviewSMSClient{actionErr: fmt.Errorf("temporary action failure"), snapshot: &supplyport.SMSSnapshot{SessionID: "s", State: "waiting_sms", Version: 1, Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}}
	for i := 1; i <= 3; i++ {
		d.Client.SMSIntent.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
		row = d.Client.SMSIntent.GetX(ctx, row.ID)
		s.smsQueryBatch(ctx, f, []*ent.SMSIntent{row})
		if saved := d.Client.SMSIntent.GetX(ctx, row.ID); saved.Attempts != i {
			t.Fatalf("backoff reset by healthy snapshot: %d want %d", saved.Attempts, i)
		}
	}
}

func TestSMSMissingOrderIdentityDoesNotBlockOtherRows(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	bad := d.Client.SMSIntent.Create().SetOrderID(row.OrderID + 1).SetOrderItemID(row.OrderItemID + 1).SetUserID(1).SetConnectionID(row.ConnectionID).SetConnectionIdentity(row.ConnectionIdentity).SetRequestNo("bad").SetRequestJSON("{}").SetRequestHash("h").SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SetPhase("query").SaveX(ctx)
	f := &reviewSMSClient{snapshot: &supplyport.SMSSnapshot{SessionID: "s", State: "waiting_sms", Version: 1, Currency: "CNY", PaidAmountCents: 123, SettlementState: "paid"}}
	s.smsQueryBatch(ctx, f, []*ent.SMSIntent{bad, row})
	if d.Client.SMSIntent.GetX(ctx, bad.ID).Phase != "review" || d.Client.SMSIntent.GetX(ctx, row.ID).Version != 1 {
		t.Fatal("corrupt row blocked healthy session")
	}
}
