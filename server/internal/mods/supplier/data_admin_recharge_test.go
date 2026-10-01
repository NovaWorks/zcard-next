package supplier

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

func TestAdminSupplierRechargeHTTPAmountAndIdempotency(t *testing.T) {
	r, d := newSupplierTestData(t)
	id := seedAccount(t, r)
	s := NewAdminSupplierService(r, nil)
	server := kratoshttp.NewServer(kratoshttp.Middleware(validate.Validator(func(req any) error {
		return fieldbehavior.ValidateRequiredFields(req.(proto.Message))
	})))
	adminv1.RegisterAdminSupplierServiceHTTPServer(server, s)
	post := func(body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/admin/supplier/accounts/%d/recharge", id), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		server.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("HTTP %d, want %d: %s", w.Code, want, w.Body.String())
		}
	}
	// The actual frontend contract: integer cents and one reference per operation.
	post(`{"amount_cents":3000,"reference":"first","remark":"30元"}`, 200)
	post(`{"amount_cents":3000,"reference":"first","remark":"retry"}`, 200)
	post(`{"amount_cents":3000,"reference":"second"}`, 200)
	post(`{"amount_cents":4000,"reference":"first"}`, 409)
	post(`{"amount":3000,"reference":"old-field"}`, 400)
	post(`{"amount_cents":-1,"reference":"negative"}`, 400)
	post(fmt.Sprintf(`{"amount_cents":%d,"reference":"oversized"}`, money.MaxCents+1), 400)
	post(`{"amount_cents":3000,"reference":"`+strings.Repeat("a", 81)+`"}`, 400)
	if got, _ := r.BalanceOf(context.Background(), id); got != 16000 {
		t.Fatalf("balance %d, want 16000 (exactly two new credits)", got)
	}
	if n := d.Client.SupplierLedgerEntry.Query().CountX(context.Background()); n != 3 {
		t.Fatalf("ledger entries %d, want seed + two credits", n)
	}
	// Legacy callers without references can still make two independent equal deposits.
	post(`{"amount_cents":100}`, 200)
	post(`{"amount_cents":100}`, 200)
	if got, _ := r.BalanceOf(context.Background(), id); got != 16200 {
		t.Fatalf("legacy deposits collided: %d", got)
	}
}

func TestSupplierPricingRejectsArchivedProduct(t *testing.T) {
	r, d := newSupplierTestData(t)
	ctx := context.Background()
	id := seedAccount(t, r)
	p := d.Client.Product.Create().SetName("deleted").SetSlug("deleted").SetPrice(100).SetStatus(-1).SaveX(ctx)
	if err := r.UpsertPriceRule(ctx, id, p.ID, 0, 0, "product", 100, 0); err == nil {
		t.Fatal("archived product returned to supplier pricing")
	}
}
