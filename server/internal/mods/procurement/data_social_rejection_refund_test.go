package procurement

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

type unchargedRejectionClient struct {
	replaySocialClient
}

func (f *unchargedRejectionClient) CreateSocial(_ context.Context, req supplyport.SocialPurchase) (*supplyport.SocialOrder, error) {
	raw, _ := json.Marshal(req)
	f.requests = append(f.requests, string(raw))
	if len(f.requests) == 1 {
		return nil, fmt.Errorf("response lost after A persisted its uncharged rejection receipt")
	}
	// A's receipt exists even though no provider order was created or charged.
	return &supplyport.SocialOrder{
		SupplyOrderID: "A-owned-rejection-receipt", DownstreamOrderNo: req.DownstreamOrderNo,
		Status: "rejected", Charged: false, Amount: 0, ErrorCode: "price_changed",
		Snapshot: &supplyport.SocialSnapshot{State: "rejected", Version: 1, SettlementState: "refunded", PaidAmountCents: 0, RefundedAmountCents: 0, Currency: "CNY"},
	}, nil
}

type unchargedRejectionGateway struct {
	fakeGW
	client *unchargedRejectionClient
}

func (g *unchargedRejectionGateway) OpenSocial(context.Context, uint64) (supplyport.SocialClient, error) {
	return g.client, nil
}

func TestSocialUnchargedRejectedReceiptRefundsActualWalletPaidOnce(t *testing.T) {
	s, d, row := socialEnv(t)
	ctx := context.Background()
	d.Client.User.Create().SetID(row.UserID).SetUsername("rejected-service-buyer").SetStatus("active").SaveX(ctx)
	d.Client.Order.UpdateOneID(row.OrderID).SetTotalAmount(999).ExecX(ctx)
	d.Client.OrderItem.UpdateOneID(row.OrderItemID).SetQuantity(1).SetAmount(999).SetUnitPrice(999).SetPaidAmount(999).ExecX(ctx)
	d.Client.Payment.Create().SetOrderID(row.OrderID).SetChannel("wallet").SetAmount(999).SetChargedAmount(999).SetStatus("success").SaveX(ctx)
	wr := wallet.NewWalletRepoImpl(d)
	if err := data.Tx(ctx, d, func(ctx context.Context) error {
		if err := wr.CreditInTx(ctx, wallet.Entry{UserID: row.UserID, Direction: "in", Type: "adjust", Amount: 999, Reference: "rejection-test-funding"}); err != nil {
			return err
		}
		return wr.DebitInTx(ctx, wallet.Entry{UserID: row.UserID, Direction: "out", Type: "order_pay", Amount: 999, Reference: "rejection-test-payment", OrderID: row.OrderID})
	}); err != nil {
		t.Fatal(err)
	}
	if got := d.Client.WalletAccount.Query().OnlyX(ctx).Available; got != 0 {
		t.Fatalf("real wallet payment did not debit 999 cents: balance %d", got)
	}
	refunder := payment.NewPaymentRepoImpl(d, nil, nil, nil, wallet.ProvidePortWallet(wr), nil, data.NewOutboxWriter(d), nil, nil, nil)
	s.refund = refunder
	conn := d.Client.SupplyConnection.Create().SetID(row.ConnectionID).SetName("A").SetDriver("zcard").SetStatus("active").SetBaseURL("https://original.test").SetCredentials([]byte("same-A-account")).SaveX(ctx)
	req := supplyport.SocialPurchase{QuoteID: "A-original-quote", ProductID: row.UpstreamProductID, SchemaRevision: row.SchemaRevision, Parameters: supplyport.SocialParameters{"link": "https://example.test/original", "quantity": "5000"}, DownstreamOrderNo: row.RequestNo, RequiredCapability: supplyport.SocialCapability, MaxSupplyAmountCents: 100, Currency: "CNY"}
	raw, _ := json.Marshal(req)
	sealed, err := s.cipher.Seal(string(raw), row.OrderItemID, row.SubsiteID)
	if err != nil {
		t.Fatal(err)
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetUpstreamOrderID("").SetChargedAmount(0).SetConnectionIdentity(data.SocialConnectionIdentity(conn)).SetPhase("purchase").SetLeaseUntil(0).SetNextRunAt(0).SetRequestCipher(sealed).SetRequestHash(fmt.Sprintf("%x", sha256.Sum256(raw))).ExecX(ctx)
	client := &unchargedRejectionClient{}
	s.gw = &unchargedRejectionGateway{client: client}

	s.RunSocial(ctx)
	if d.Client.SocialTask.GetX(ctx, row.ID).UpstreamOrderID != "" || d.Client.RefundOrder.Query().CountX(ctx) != 0 {
		t.Fatal("lost response invented an A receipt or refunded before confirmation")
	}
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	s.RunSocial(ctx)
	observed := d.Client.SocialTask.GetX(ctx, row.ID)
	if len(client.requests) != 2 || client.requests[0] != client.requests[1] {
		t.Fatal("rejection receipt retry changed the original purchase")
	}
	if observed.UpstreamOrderID != "A-owned-rejection-receipt" || observed.State != "rejected" || observed.ChargedAmount != 0 || observed.RefundedAmount != 0 || observed.SettlementState != "refunded" || observed.RefundReference != "rejected:A-owned-rejection-receipt" || observed.Phase != "refund" {
		t.Fatalf("uncharged rejection did not become authoritative refund evidence: %+v", observed)
	}

	// The worker uses the actual native payment/refund/wallet implementation.
	d.Client.SocialTask.UpdateOneID(row.ID).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	s.RunSocial(ctx)
	finished := d.Client.SocialTask.GetX(ctx, row.ID)
	if finished.RetailRefundedAmount != 999 || finished.RetailRefundState != "succeeded" || finished.Phase != "done" || finished.RefundID == 0 {
		t.Fatalf("buyer's full actual payment was not refunded: %+v", finished)
	}
	for i := 0; i < 2; i++ {
		id, err := refunder.RefundSocial(ctx, row.ID)
		if err != nil || id != finished.RefundID {
			t.Fatalf("same receipt did not replay the original refund: id=%d err=%v", id, err)
		}
		s.RunSocial(ctx)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 999 || d.Client.RefundOrder.Query().CountX(ctx) != 1 || d.Client.WalletTransaction.Query().Where(wallettransaction.Type("refund")).CountX(ctx) != 1 || d.Client.OutboxEvent.Query().CountX(ctx) != 1 || d.Client.Order.GetX(ctx, row.OrderID).Status != "refunded" || d.Client.OrderItem.GetX(ctx, row.OrderItemID).RefundedAmount != 999 {
		t.Fatal("uncharged receipt duplicated the credit or left native refund state inconsistent")
	}
	if len(client.requests) != 2 || client.queryCalls != 0 {
		t.Fatal("rejected service was repurchased or queried as a provider order")
	}
}
