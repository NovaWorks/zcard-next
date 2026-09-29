package affiliate

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"
	"testing"
)

func TestPhysicalCommissionCumulativeRefundAndLatePaidEvent(t *testing.T) {
	ctx := context.Background()
	repo, d := newAffiliateData(t)
	w := wallet.NewWalletRepoImpl(d)
	s := NewAffiliateService(repo, wallet.ProvidePortWallet(w), fakeSettings{}, nil, nil)
	o := d.Client.Order.Create().SetOrderNo("PHYS-COMMISSION").SetStatus("paid").SetCommerceVersion(1).SetTotalAmount(1300).SetShippingAmount(300).SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(1).SetQuantity(1).SetUnitPrice(1000).SetAmount(1000).SetPaidAmount(1000).SetGoodsType("physical").SetFulfillmentType("shipping").SaveX(ctx)
	paid := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"invite_l1":7,"total_cents":1000}`, o.ID))}
	refunded := events.Envelope{Payload: []byte(fmt.Sprintf(`{"order_id":%d,"commerce_version":1}`, o.ID))}
	d.Client.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(400).ExecX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(400).SetChannel("wallet").SetStatus("succeeded").SaveX(ctx)
	if e := s.OnOrderRefunded(ctx, refunded); e != nil {
		t.Fatal(e)
	}
	if e := s.OnOrderPaid(ctx, paid); e != nil {
		t.Fatal(e)
	}
	c := d.Client.AffiliateCommission.Query().Where(affiliatecommission.Tier(1)).OnlyX(ctx)
	if c.Amount != 30 {
		t.Fatalf("late-paid reconciliation = %d", c.Amount)
	}
	if e := s.confirmOne(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 30 {
		t.Fatal("confirmation paid stale pre-refund amount")
	}
	d.Client.OrderItem.UpdateOneID(it.ID).SetRefundedAmount(700).ExecX(ctx)
	d.Client.RefundOrder.Create().SetOrderID(o.ID).SetAmount(300).SetChannel("wallet").SetStatus("succeeded").SaveX(ctx)
	if e := s.OnOrderRefunded(ctx, refunded); e != nil {
		t.Fatal(e)
	}
	if e := s.OnOrderRefunded(ctx, refunded); e != nil {
		t.Fatal(e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 15 || d.Client.AffiliateCommission.GetX(ctx, c.ID).Amount != 15 {
		t.Fatal("incremental commission reversal incorrect")
	}
	if e := s.OnOrderPaid(ctx, paid); e != nil {
		t.Fatal(e)
	}
	if d.Client.WalletAccount.Query().OnlyX(ctx).Available != 15 {
		t.Fatal("late duplicate paid changed balance")
	}
}
