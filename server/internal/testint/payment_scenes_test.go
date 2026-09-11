//go:build integration

package testint

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/paymentchannel"
	"testing"
)

func TestPaymentScenesMySQL(t *testing.T) { runPaymentScenes(MySQL(t)) }
func TestPaymentScenesPG(t *testing.T)    { runPaymentScenes(PG(t)) }
func runPaymentScenes(h *Harness) {
	t := h.T
	ctx := context.Background()
	c := h.Data.Client
	ch := c.PaymentChannel.Create().SetName("旧渠道").SetCode("scene-original").SetDriver("epay").SetConfig([]byte("encrypted")).SetAllowPurchase(false).SetAllowMemberRecharge(false).SetAllowSupplyRecharge(false).SaveX(ctx)
	// Simulate an older writer: omit all three new columns. Defaults must preserve usability.
	_, err := h.Data.DB.ExecContext(ctx, `INSERT INTO payment_channels (name, code, driver, config, subsite_id, fee, fee_type, fee_bearer, sort, enabled, icon, created_at, updated_at) SELECT name, 'scene-legacy', driver, config, subsite_id, fee, fee_type, fee_bearer, sort, enabled, icon, created_at, updated_at FROM payment_channels WHERE code = 'scene-original'`)
	if err != nil {
		t.Fatal(err)
	}
	old := c.PaymentChannel.Query().Where(paymentchannel.Code("scene-legacy")).OnlyX(ctx)
	if !old.AllowPurchase || !old.AllowMemberRecharge || !old.AllowSupplyRecharge {
		t.Fatal("legacy SQL insert defaults incorrect")
	}
	c.PaymentChannel.UpdateOneID(ch.ID).SetName("更名").ExecX(ctx)
	saved := c.PaymentChannel.GetX(ctx, ch.ID)
	if saved.AllowPurchase || saved.AllowMemberRecharge || saved.AllowSupplyRecharge {
		t.Fatal("unrelated update reset restrictions")
	}
}
