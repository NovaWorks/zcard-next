package payment

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
)

func TestPhysicalRefundUsesInventorySnapshotAfterSettingChange(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracked=%t", tracked), func(t *testing.T) {
			ctx := context.Background()
			d, repo, _, _, _, _ := newCallbackEnv(t)
			repo.outbox = data.NewOutboxWriter(d)
			p := d.Client.Product.Create().SetName("Parcel").SetSlug("snapshot-refund").SetGoodsType("physical").SetPrice(500).SetPhysicalStock(4).SetTrackInventory(!tracked).SaveX(ctx)
			o := d.Client.Order.Create().SetOrderNo("SNAPSHOT-REFUND").SetUserID(1).SetCommerceVersion(1).SetStatus("paid").SetTotalAmount(500).SaveX(ctx)
			item := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(500).SetAmount(500).SetPaidAmount(500).SetGoodsType("physical").SetInventoryTracked(tracked).SetFulfillmentType("shipping").SaveX(ctx)
			req := &adminv1.CreateRefundRequest{Channel: "wallet", RequestKey: "snapshot-refund-key", AmountCents: 500, ExpectedRefundedCents: refundPtr(0), ItemAllocationsJson: fmt.Sprintf(`[{"item_id":%d,"amount_cents":500,"cancel_quantity":1}]`, item.ID)}
			first, err := repo.RefundPhysical(ctx, o.ID, 7, req)
			if err != nil {
				t.Fatal(err)
			}
			again, err := repo.RefundPhysical(ctx, o.ID, 7, req)
			if err != nil || first.ID != again.ID {
				t.Fatalf("refund replay %v", err)
			}
			wantStock, wantMovements := int64(4), 0
			if tracked {
				wantStock, wantMovements = 5, 1
			}
			if d.Client.Product.GetX(ctx, p.ID).PhysicalStock != wantStock || d.Client.PhysicalStockMovement.Query().CountX(ctx) != wantMovements || d.Client.WalletAccount.Query().OnlyX(ctx).Available != 500 || d.Client.OrderItem.GetX(ctx, item.ID).CanceledQuantity != 1 {
				t.Fatal("refund did not preserve snapshot, inventory, wallet and cancellation exactly once")
			}
		})
	}
}
