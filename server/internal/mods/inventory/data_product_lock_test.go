package inventory

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory/port"
	"testing"
)

func TestLockedProductBlocksRestockButAllowsOrderReservation(t *testing.T) {
	r, d := newTestRepo(t)
	ctx := context.Background()
	id := seedCards(t, d, 2)
	d.Client.Product.UpdateOneID(id).SetIsLocked(true).ExecX(ctx)
	if _, e := r.ImportConfirm(ctx, ImportInput{ProductID: id, Lines: []string{"new"}}); !data.IsProductLocked(e) {
		t.Fatal("restock bypassed lock", e)
	}
	reservation, e := r.Reserve(ctx, 0, []port.ReserveItem{{ProductID: id, Quantity: 1}})
	if e != nil || reservation == nil {
		t.Fatal("lock blocked order reservation", e)
	}
}
