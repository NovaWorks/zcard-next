package procurement

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
	"testing"
	"time"
)

func socialEnv(t *testing.T) (*ProcureService, *data.Data, *ent.SocialTask) {
	t.Helper()
	repo, d := newProcureTestData(t)
	ctx := context.Background()
	oid, iid := seedOrderItem(t, d)
	d.Client.Order.UpdateOneID(oid).SetBaseCurrency("CNY").ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(iid).SetDeliveryKind("social_service").ExecX(ctx)
	row := d.Client.SocialTask.Create().SetID(uuid.NewString()).SetRole("retail").SetOrderID(oid).SetOrderItemID(iid).SetUserID(1).SetConnectionID(9).SetQuoteID("q").SetSchemaRevision("s").SetRequestNo("original").SetRequestHash("hash").SetUpstreamProductID("p").SetUpstreamOrderID("A-task").SetChargedAmount(100).SetRetailPaidAmount(999).SetLeaseToken("lease").SetLeaseUntil(time.Now().Add(time.Minute).Unix()).SetPhase("query").SaveX(ctx)
	return &ProcureService{repo: repo, cipher: newTestCipher(t)}, d, row
}
func TestSocialSnapshotDoesNotInferRefundFromPartialAndPreservesPackageAbsence(t *testing.T) {
	s, d, row := socialEnv(t)
	ctx := context.Background()
	zero := int64(0)
	snap := &supplyport.SocialSnapshot{State: "partial", Version: 1, Remains: &zero, SettlementState: "review", PaidAmountCents: 100, Currency: "CNY"}
	if e := s.socialSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	saved := d.Client.SocialTask.GetX(ctx, row.ID)
	if saved.Phase != "query" || saved.RefundedAmount != 0 || saved.RetailRefundState != "none" {
		t.Fatal("partial inferred a refund")
	}
	raw, e := s.cipher.Open(saved.SnapshotCipher, row.OrderItemID, 0)
	if e != nil {
		t.Fatal(e)
	}
	var got supplyport.SocialSnapshot
	_ = json.Unmarshal([]byte(raw), &got)
	if got.Quantity != nil || got.CompletedQuantity != nil || got.Remains == nil {
		t.Fatal("package quantity fabricated or remains lost")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	snap.Version = 2
	snap.SettlementState = "partially_refunded"
	snap.RefundedAmountCents = 50
	snap.RefundReference = "A-refund-1"
	if e = s.socialSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	saved = d.Client.SocialTask.GetX(ctx, row.ID)
	if saved.Phase != "refund" || saved.NextRunAt != 0 {
		t.Fatal("confirmed refund not scheduled")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	snap.Version = 3
	snap.RefundedAmountCents = 60
	if e = s.socialSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	if d.Client.SocialTask.GetX(ctx, row.ID).Phase != "review" {
		t.Fatal("reused refund reference advanced balance")
	}
}
func TestSocialRetailOwnerActionIsolationAndCostRedaction(t *testing.T) {
	s, d, row := socialEnv(t)
	base := context.Background()
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetCanCancel(true).ExecX(base)
	no := d.Client.Order.GetX(base, row.OrderID).OrderNo
	owner := identity.WithClaims(base, &authn.Claims{Subject: 1, Realm: authn.RealmUser})
	svc := NewStoreSocialOrderService(s)
	for _, ctx := range []context.Context{base, identity.WithClaims(base, &authn.Claims{Subject: 2, Realm: authn.RealmUser}), identity.WithClaims(base, &authn.Claims{Subject: 1, Realm: authn.RealmAdmin}), tenancy.WithContext(owner, tenancy.Context{SubsiteID: 99})} {
		if _, e := svc.Get(ctx, &storefrontv1.SocialRetailOrderRequest{OrderNo: no}); e == nil {
			t.Fatal("foreign service access accepted")
		}
	}
	req := &storefrontv1.SocialRetailActionRequest{OrderNo: no, Action: "cancel", RequestId: uuid.NewString()}
	for i := 0; i < 2; i++ {
		if _, e := svc.Act(owner, req); e != nil {
			t.Fatal(e)
		}
	}
	if d.Client.SocialOperation.Query().CountX(base) != 1 {
		t.Fatal("duplicate action created")
	}
	req.Action = "refill"
	if _, e := svc.Act(owner, req); e == nil {
		t.Fatal("operation key changed action")
	}
	snap := supplyport.SocialSnapshot{State: "processing", Version: 1, PaidAmountCents: 100, RefundReference: "private-wholesale-reference", Currency: "CNY"}
	raw, _ := json.Marshal(snap)
	sealed, e := s.cipher.Seal(string(raw), row.OrderItemID, 0)
	if e != nil {
		t.Fatal(e)
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetSnapshotCipher(sealed).ExecX(base)
	got, e := svc.Get(owner, &storefrontv1.SocialRetailOrderRequest{OrderNo: no})
	if e != nil {
		t.Fatal(e)
	}
	if got.PaidAmountCents != 999 || got.Snapshot.AsMap()["paid_amount_cents"] != "999" || got.Snapshot.AsMap()["refund_reference"] != nil {
		t.Fatal("wholesale cost/reference leaked", got)
	}
}

type replaySocialClient struct {
	requests   []string
	queryCalls int
	loseFirst  bool
}

func (f *replaySocialClient) SocialSchema(context.Context, string) (*supplyport.SocialSchema, error) {
	return nil, fmt.Errorf("unused")
}
func (f *replaySocialClient) QuoteSocial(context.Context, string, supplyport.SocialParameters, string) (*supplyport.SocialQuote, error) {
	return nil, fmt.Errorf("unused")
}
func (f *replaySocialClient) CreateSocial(_ context.Context, req supplyport.SocialPurchase) (*supplyport.SocialOrder, error) {
	raw, _ := json.Marshal(req)
	f.requests = append(f.requests, string(raw))
	if f.loseFirst && len(f.requests) == 1 {
		return nil, fmt.Errorf("response lost after A accepted")
	}
	return &supplyport.SocialOrder{SupplyOrderID: "A-original", DownstreamOrderNo: req.DownstreamOrderNo, Status: "pending", Amount: 100, Charged: true}, nil
}
func (f *replaySocialClient) QuerySocial(context.Context, []string) ([]supplyport.SocialOrder, error) {
	f.queryCalls++
	return []supplyport.SocialOrder{{SupplyOrderID: "A-original", DownstreamOrderNo: "original", Status: "processing", Amount: 100, Charged: true, Snapshot: &supplyport.SocialSnapshot{State: "processing", Version: 1, SettlementState: "paid", PaidAmountCents: 100, Currency: "CNY"}}}, nil
}
func (f *replaySocialClient) ActSocial(context.Context, string, string, string) (*supplyport.SocialOperation, error) {
	return nil, fmt.Errorf("unused")
}
func (f *replaySocialClient) QuerySocialOperation(context.Context, string, string) (*supplyport.SocialOperation, error) {
	return nil, fmt.Errorf("unused")
}

type replaySocialGateway struct {
	fakeGW
	client *replaySocialClient
}

func (g *replaySocialGateway) OpenSocial(context.Context, uint64) (supplyport.SocialClient, error) {
	return g.client, nil
}
func TestSocialLostAcceptanceReplaysFrozenRequestAndKnownOrderOnlyQueries(t *testing.T) {
	s, d, row := socialEnv(t)
	ctx := context.Background()
	conn := d.Client.SupplyConnection.Create().SetID(9).SetName("A").SetDriver("zcard").SetStatus("active").SetBaseURL("https://original.test").SetCredentials([]byte("secret-cipher")).SaveX(ctx)
	req := supplyport.SocialPurchase{QuoteID: "A-quote", ProductID: "p", SchemaRevision: "s", Parameters: supplyport.SocialParameters{"comments": "one\none\ntwo"}, DownstreamOrderNo: "original", RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 100, Currency: "CNY"}
	raw, _ := json.Marshal(req)
	sealed, e := s.cipher.Seal(string(raw), row.OrderItemID, 0)
	if e != nil {
		t.Fatal(e)
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetUpstreamOrderID("").SetConnectionIdentity(data.SocialConnectionIdentity(conn)).SetPhase("purchase").SetLeaseUntil(0).SetNextRunAt(0).SetRequestCipher(sealed).SetRequestHash(fmt.Sprintf("%x", sha256.Sum256(raw))).ExecX(ctx)
	client := &replaySocialClient{loseFirst: true}
	s.gw = &replaySocialGateway{client: client}
	s.RunSocial(ctx)
	if d.Client.SocialTask.GetX(ctx, row.ID).UpstreamOrderID != "" {
		t.Fatal("invented upstream receipt")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	s.RunSocial(ctx)
	if len(client.requests) != 2 || client.requests[0] != client.requests[1] || d.Client.SocialTask.GetX(ctx, row.ID).UpstreamOrderID != "A-original" {
		t.Fatal("lost acceptance changed purchase identity")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	s.RunSocial(ctx)
	if len(client.requests) != 2 || client.queryCalls != 1 {
		t.Fatal("known order purchased again")
	}
	d.Client.SupplyConnection.UpdateOneID(conn.ID).SetCredentials([]byte("different-account")).ExecX(ctx)
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	s.RunSocial(ctx)
	if d.Client.SocialTask.GetX(ctx, row.ID).Phase != "review" || client.queryCalls != 1 {
		t.Fatal("changed account queried original purchase")
	}
}

func TestSocialCompletionCommitsNativeDeliveryEventOnce(t *testing.T) {
	s, d, row := socialEnv(t)
	ctx := context.Background()
	s.outbox = data.NewOutboxWriter(d)
	snap := &supplyport.SocialSnapshot{State: "completed", Version: 1, SettlementState: "paid", PaidAmountCents: 100, Currency: "CNY"}
	if e := s.socialSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	if d.Client.Order.GetX(ctx, row.OrderID).Status != "delivered" || d.Client.OrderItem.GetX(ctx, row.OrderItemID).FulfillmentStatus != "delivered" || d.Client.OutboxEvent.Query().CountX(ctx) != 1 {
		t.Fatal("native delivery not committed")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(time.Now().Add(time.Minute).Unix()).ExecX(ctx)
	if e := s.socialSnapshot(ctx, row, snap); e != nil {
		t.Fatal(e)
	}
	if d.Client.OutboxEvent.Query().CountX(ctx) != 1 {
		t.Fatal("duplicate delivery event")
	}
}

func TestSocialTransientFailuresKeepOriginalObligationRecoverable(t *testing.T) {
	s, d, row := socialEnv(t)
	ctx := context.Background()
	row = d.Client.SocialTask.UpdateOneID(row.ID).SetAttempts(99).SaveX(ctx)
	s.socialFailure(ctx, row, fmt.Errorf("temporary network outage"))
	saved := d.Client.SocialTask.GetX(ctx, row.ID)
	if saved.Phase != "query" || saved.RequestNo != row.RequestNo || saved.UpstreamOrderID != row.UpstreamOrderID || saved.Attempts != 100 || saved.NextRunAt <= time.Now().Unix() || saved.NextRunAt > time.Now().Add(641*time.Second).Unix() {
		t.Fatal("transient outage stranded or changed original purchase")
	}
}
