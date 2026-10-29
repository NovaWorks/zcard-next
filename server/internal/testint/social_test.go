//go:build integration

package testint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
)

func TestSocialStorageMySQL(t *testing.T) { socialStorage(MySQL(t)) }
func TestSocialStoragePG(t *testing.T)    { socialStorage(PG(t)) }

// This test uses only shared storage contracts. It runs unchanged in the open
// client and private supplier against the real migration-created tables.
func socialStorage(h *Harness) {
	t, d, ctx := h.T, h.Data, context.Background()
	conn := d.Client.SupplyConnection.Create().SetName("social storage").SetDriver("zcard").SetBaseURL("https://supplier.example.test").SetCredentials([]byte("fixture cipher")).SaveX(ctx)
	rules := map[string]any{"version": float64(1), "profile": "Custom Comments", "fields": []any{map[string]any{"key": "comments", "type": "lines", "required": true}}, "quantity": map[string]any{"mode": "derived_lines", "min": float64(2)}, "billing": map[string]any{"mode": "quoted_total", "currency": "CNY"}}
	p := d.Client.Product.Create().SetName("Social").SetSlug("social-storage").SetProductKind("social_service").SetDeliveryKind("social_service").SetSocialSchema(rules).SaveX(ctx)
	if got := d.Client.Product.GetX(ctx, p.ID); !reflect.DeepEqual(got.SocialSchema, rules) {
		t.Fatal("product social JSON did not round-trip")
	}
	o := d.Client.Order.Create().SetOrderNo("social-storage-order").SetTotalAmount(200).SetBaseCurrency("CNY").SaveX(ctx)
	it := d.Client.OrderItem.Create().SetOrderID(o.ID).SetProductID(p.ID).SetQuantity(1).SetUnitPrice(200).SetAmount(200).SetFulfillmentType("upstream").SetDeliveryKind("social_service").SetSocialSchema(rules).SetSocialPurchaseSnapshot(`{"quote_id":"retail-quote","request_no":"original"}`).SaveX(ctx)
	if got := d.Client.OrderItem.GetX(ctx, it.ID); !reflect.DeepEqual(got.SocialSchema, rules) || got.SocialPurchaseSnapshot != it.SocialPurchaseSnapshot {
		t.Fatal("item schema/snapshot did not round-trip")
	}
	a := d.Client.SupplierAccount.Create().SetName("merchant A").SetAPIKey("social-a").SetAPISecret([]byte("cipher-a")).SetStatus("approved").SetBalanceCache(1000).SaveX(ctx)
	b := d.Client.SupplierAccount.Create().SetName("merchant B").SetAPIKey("social-b").SetAPISecret([]byte("cipher-b")).SetStatus("approved").SetBalanceCache(1000).SaveX(ctx)
	params := `{"comments":"first\nfirst\n第二行","link":"https://example.test/post"}`
	rawRules, e := json.Marshal(rules)
	if e != nil {
		t.Fatal(e)
	}
	q := d.Client.SocialQuote.Create().SetID("wholesale-storage-quote").SetRole("wholesale").SetAccountID(a.ID).SetProductID(p.ID).SetConnectionID(conn.ID).SetSchemaRevision("schema-revision").SetUpstreamProductID("opaque-service").SetParamsJSON(params).SetParamsCipher([]byte{0, 1, 255, 0, 7}).SetSchemaJSON(string(rawRules)).SetAmountCents(123).SetCostCents(77).SetExpiresAt(time.Now().Add(time.Minute).Unix()).SaveX(ctx)
	gotQ := d.Client.SocialQuote.GetX(ctx, q.ID)
	if gotQ.ParamsJSON != params || !bytes.Equal(gotQ.ParamsCipher, q.ParamsCipher) || gotQ.SchemaJSON != string(rawRules) || gotQ.AmountCents != 123 {
		t.Fatal("quote strings/binary did not round-trip")
	}
	createTask := func(c *ent.Client, id, role string, account uint64, no string) (*ent.SocialTask, error) {
		return c.SocialTask.Create().SetID(id).SetRole(role).SetAccountID(account).SetOrderID(o.ID).SetOrderItemID(it.ID).SetProductID(p.ID).SetConnectionID(conn.ID).SetQuoteID(q.ID).SetSchemaRevision("schema-revision").SetRequestNo(no).SetRequestHash("frozen-hash").SetRequestCipher([]byte{0, 255, 5, 0, 7}).SetUpstreamProductID("opaque-service").SetSnapshotCipher([]byte{1, 0, 254}).SetChargedAmount(123).Save(ctx)
	}
	task, e := createTask(d.Client, "wholesale-a", "wholesale", a.ID, "same-request")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = createTask(d.Client, "duplicate-a", "wholesale", a.ID, "same-request"); !ent.IsConstraintError(e) {
		t.Fatal("same account request was not unique", e)
	}
	if _, e = createTask(d.Client, "wholesale-b", "wholesale", b.ID, "same-request"); e != nil {
		t.Fatal("request namespace crossed accounts", e)
	}
	if _, e = createTask(d.Client, "retail-a", "retail", a.ID, "same-request"); e != nil {
		t.Fatal("request namespace crossed roles", e)
	}
	stored := d.Client.SocialTask.GetX(ctx, task.ID)
	if !bytes.Equal(stored.RequestCipher, task.RequestCipher) || !bytes.Equal(stored.SnapshotCipher, task.SnapshotCipher) {
		t.Fatal("task cipher bytes did not round-trip")
	}
	op := d.Client.SocialOperation.Create().SetID("operation-a").SetTaskID(task.ID).SetOperationID("same-operation").SetAction("refill").SetReference("凭证-1").SaveX(ctx)
	if got := d.Client.SocialOperation.GetX(ctx, op.ID); got.Reference != "凭证-1" {
		t.Fatal("operation UTF-8 receipt corrupted")
	}
	if _, e = d.Client.SocialOperation.Create().SetID("duplicate-operation").SetTaskID(task.ID).SetOperationID("same-operation").SetAction("cancel").Save(ctx); !ent.IsConstraintError(e) {
		t.Fatal("task operation id was not unique", e)
	}
	if _, e = d.Client.SocialOperation.Create().SetID("other-task-operation").SetTaskID("wholesale-b").SetOperationID("same-operation").SetAction("cancel").Save(ctx); e != nil {
		t.Fatal("operation namespace crossed tasks", e)
	}

	// Real concurrent UPDATE predicates must admit precisely one worker.
	start := make(chan struct{})
	var wg sync.WaitGroup
	var acquired atomic.Int32
	errs := make(chan error, 12)
	now := time.Now().Unix()
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			affected, e := d.Client.SocialTask.Update().Where(socialtask.ID(task.ID), socialtask.Version(task.Version), socialtask.LeaseUntilLTE(now)).SetLeaseToken(fmt.Sprintf("worker-%d", n)).SetLeaseUntil(now + 60).Save(ctx)
			if e != nil {
				errs <- e
				return
			}
			if affected == 1 {
				acquired.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal("lease CAS failed", e)
	}
	if acquired.Load() != 1 {
		t.Fatal("multiple workers acquired lease", acquired.Load())
	}
	if n, e := d.Client.SocialTask.Update().Where(socialtask.ID(task.ID), socialtask.LeaseToken("stale-worker")).SetState("completed").Save(ctx); e != nil || n != 0 {
		t.Fatal("stale worker wrote current lease", n, e)
	}

	// Every admission write must disappear when a later local write fails.
	abort := errors.New("force admission rollback")
	admit := func(ctx context.Context, rollback bool) error {
		c := data.Client(ctx, d)
		if e := c.SupplierAccount.UpdateOneID(a.ID).AddBalanceCache(-123).Exec(ctx); e != nil {
			return e
		}
		order, e := c.SupplyOrder.Create().SetAccountID(a.ID).SetDownstreamOrderNo("atomic-request").SetItems([]map[string]any{{"delivery_kind": "social_service", "product_id": p.ID}}).SetAmount(123).SetStatus("fulfilling").Save(ctx)
		if e != nil {
			return e
		}
		if e = c.SupplierLedgerEntry.Create().SetAccountID(a.ID).SetSupplyOrderID(order.ID).SetType("supply_pay").SetAmount(-123).SetReference("social-atomic-pay").Exec(ctx); e != nil {
			return e
		}
		if e = c.SocialQuote.UpdateOneID(q.ID).SetConsumedBy("atomic-request").Exec(ctx); e != nil {
			return e
		}
		if _, e = createTask(c, "atomic-task", "wholesale", a.ID, "atomic-request"); e != nil {
			return e
		}
		if rollback {
			return abort
		}
		return nil
	}
	if e = data.Tx(ctx, d, func(ctx context.Context) error { return admit(ctx, true) }); !errors.Is(e, abort) {
		t.Fatal("forced rollback did not propagate", e)
	}
	if d.Client.SupplierAccount.GetX(ctx, a.ID).BalanceCache != 1000 || d.Client.SocialQuote.GetX(ctx, q.ID).ConsumedBy != "" || d.Client.SupplierLedgerEntry.Query().CountX(ctx) != 0 {
		t.Fatal("rollback leaked money or quote consumption")
	}
	if exists, e := d.Client.SocialTask.Query().Where(socialtask.ID("atomic-task")).Exist(ctx); e != nil || exists {
		t.Fatal("rollback left a purchase task", e)
	}
	if exists, e := d.Client.SupplyOrder.Query().Where(supplyorder.DownstreamOrderNo("atomic-request")).Exist(ctx); e != nil || exists {
		t.Fatal("rollback left a supply order", e)
	}
	if e = data.Tx(ctx, d, func(ctx context.Context) error { return admit(ctx, false) }); e != nil {
		t.Fatal("atomic admission failed", e)
	}
	if d.Client.SupplierAccount.GetX(ctx, a.ID).BalanceCache != 877 || d.Client.SocialQuote.GetX(ctx, q.ID).ConsumedBy != "atomic-request" || d.Client.SupplierLedgerEntry.Query().CountX(ctx) != 1 {
		t.Fatal("committed money/quote not consistent")
	}
	if exists, e := d.Client.SocialTask.Query().Where(socialtask.ID("atomic-task")).Exist(ctx); e != nil || !exists {
		t.Fatal("committed purchase task missing", e)
	}
	if e = data.ApplyMigrations(ctx, d.DB, d.Dialect, h.DSN); e != nil {
		t.Fatal("repeat social migrations failed", e)
	}
}
