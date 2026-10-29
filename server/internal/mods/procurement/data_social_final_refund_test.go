package procurement

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
)

type finalRefundClient struct {
	replaySocialClient
	operationStatus string
	operationCalls  int
}

func (f *finalRefundClient) QuerySocial(context.Context, []string) ([]supplyport.SocialOrder, error) {
	f.queryCalls++
	return []supplyport.SocialOrder{{SupplyOrderID: "A-task", DownstreamOrderNo: "original", Status: "canceled", Amount: 100, Charged: true, Snapshot: &supplyport.SocialSnapshot{State: "canceled", Version: 2, SettlementState: "refunded", PaidAmountCents: 100, RefundedAmountCents: 100, RefundReference: "A-full-refund", Currency: "CNY"}}}, nil
}

func (f *finalRefundClient) QuerySocialOperation(_ context.Context, _ string, operationID string) (*supplyport.SocialOperation, error) {
	f.operationCalls++
	return &supplyport.SocialOperation{OperationID: operationID, Status: f.operationStatus}, nil
}

type finalRefundGateway struct {
	fakeGW
	client *finalRefundClient
}

func (g *finalRefundGateway) OpenSocial(context.Context, uint64) (supplyport.SocialClient, error) {
	return g.client, nil
}

func TestSocialFullRefundWaitsForAcceptedOperationThenClosesWithoutAnotherCredit(t *testing.T) {
	for _, tc := range []struct{ initial, final string }{{"pending", "succeeded"}, {"unknown", "rejected"}} {
		t.Run(tc.initial+"_to_"+tc.final, func(t *testing.T) {
			s, d, row := socialEnv(t)
			ctx := context.Background()
			d.Client.User.Create().SetID(row.UserID).SetUsername("fully-refunded-buyer").SetStatus("active").SaveX(ctx)
			d.Client.Order.UpdateOneID(row.OrderID).SetTotalAmount(999).ExecX(ctx)
			d.Client.OrderItem.UpdateOneID(row.OrderItemID).SetQuantity(1).SetAmount(999).SetUnitPrice(999).SetPaidAmount(999).ExecX(ctx)
			d.Client.Payment.Create().SetOrderID(row.OrderID).SetChannel("wallet").SetAmount(999).SetChargedAmount(999).SetStatus("success").SaveX(ctx)
			wr := wallet.NewWalletRepoImpl(d)
			refunder := payment.NewPaymentRepoImpl(d, nil, nil, nil, wallet.ProvidePortWallet(wr), nil, data.NewOutboxWriter(d), nil, nil, nil)
			s.refund = refunder
			conn := d.Client.SupplyConnection.Create().SetID(row.ConnectionID).SetName("A").SetDriver("zcard").SetStatus("active").SetBaseURL("https://original.test").SetCredentials([]byte("same-A-account")).SaveX(ctx)
			d.Client.SocialTask.UpdateOneID(row.ID).SetConnectionIdentity(data.SocialConnectionIdentity(conn)).SetState("canceled").SetSettlementState("refunded").SetRefundedAmount(100).SetRefundReference("A-full-refund").SetPhase("refund").SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
			op := d.Client.SocialOperation.Create().SetID("original-operation-row").SetTaskID(row.ID).SetOperationID("original-cancel-request").SetUpstreamOperationID("A-original-operation").SetAction("cancel").SetStatus(tc.initial).SaveX(ctx)
			refundID, err := refunder.RefundSocial(ctx, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			refunded := d.Client.SocialTask.GetX(ctx, row.ID)
			if refunded.Phase != "query" || refunded.RetailRefundedAmount != 999 || refunded.RetailRefundState != "succeeded" {
				t.Fatal("full refund discarded the accepted operation or did not credit the buyer")
			}
			client := &finalRefundClient{operationStatus: tc.initial}
			s.gw = &finalRefundGateway{client: client}
			d.Client.SocialTask.UpdateOneID(row.ID).SetNextRunAt(0).ExecX(ctx)
			s.RunSocial(ctx)
			if d.Client.SocialTask.GetX(ctx, row.ID).Phase != "query" {
				t.Fatal("task closed before its accepted operation was resolved")
			}
			client.operationStatus = tc.final
			d.Client.SocialTask.UpdateOneID(row.ID).SetNextRunAt(0).ExecX(ctx)
			s.RunSocial(ctx)
			finished := d.Client.SocialTask.GetX(ctx, row.ID)
			if finished.Phase != "done" || d.Client.SocialOperation.GetX(ctx, op.ID).Status != tc.final {
				t.Fatalf("settled task kept polling after its operation finished: phase=%s operation=%s", finished.Phase, d.Client.SocialOperation.GetX(ctx, op.ID).Status)
			}
			s.RunSocial(ctx)
			if replay, err := refunder.RefundSocial(ctx, row.ID); err != nil || replay != refundID {
				t.Fatalf("settled refund replay changed receipt: id=%d err=%v", replay, err)
			}
			if finished.RefundID != refundID || finished.RetailRefundedAmount != 999 || finished.RefundedAmount != 100 || finished.RefundReference != "A-full-refund" || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 999 || d.Client.RefundOrder.Query().CountX(ctx) != 1 || d.Client.WalletTransaction.Query().CountX(ctx) != 1 || d.Client.OutboxEvent.Query().CountX(ctx) != 1 || client.queryCalls != 2 || client.operationCalls != 2 || len(client.requests) != 0 {
				t.Fatal("operation completion changed funds, refund evidence, or continued upstream work")
			}
		})
	}
}
