//go:build integration

package supplier

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
)

func TestLegacySupplySKUCurrentReadMySQL(t *testing.T) {
	runLegacySupplySKUCurrentRead(testint.MySQL(t))
}
func TestLegacySupplySKUCurrentReadPG(t *testing.T) { runLegacySupplySKUCurrentRead(testint.PG(t)) }

func runLegacySupplySKUCurrentRead(h *testint.Harness) {
	t, d := h.T, h.Data
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if h.D == db.MySQL {
		var isolation string
		if err := d.DB.QueryRowContext(ctx, "SELECT @@transaction_isolation").Scan(&isolation); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(isolation, "REPEATABLE-READ") {
			t.Fatalf("this stale-snapshot regression needs MySQL REPEATABLE READ; got %s", isolation)
		}
	}
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSupplierRepoImpl(d, box)
	account := seedCompatAccount(t, repo, "zcard", "current-read", "test-secret", 10000)
	cat := &fakeCatalog{prods: []catalogport.SupplierProduct{{ID: 1, Name: "membership", Price: 1000, Status: 1}}}
	svc := &SupplyAPIService{repo: repo, reader: cat}
	securityStock(t, svc, 1)

	writerLocked, readerSnapshot, releaseWriter := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseWriter) })
	defer release()
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- data.Tx(ctx, d, func(tx context.Context) error {
			if _, err := data.GuardProductWrite(tx, d, 1); err != nil {
				return err
			}
			if _, err := data.Client(tx, d).ProductSku.Create().SetProductID(1).SetName("monthly").SetSpecValues(map[string]string{}).Save(tx); err != nil {
				return err
			}
			close(writerLocked)
			select {
			case <-releaseWriter:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-writerLocked:
	case err := <-writerDone:
		t.Fatalf("SKU writer did not acquire product lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	type result struct {
		out *fulfillOutcome
		err error
	}
	readerDone := make(chan result, 1)
	go func() {
		var out *fulfillOutcome
		err := data.Tx(ctx, d, func(tx context.Context) error {
			// Under MySQL RR this read establishes a snapshot before the SKU
			// commits. The subsequent product lock must wait for that writer.
			n, err := data.Client(tx, d).ProductSku.Query().Where(productsku.ProductID(1)).Count(tx)
			if err != nil || n != 0 {
				return fmt.Errorf("pre-commit SKU snapshot: count=%d err=%v", n, err)
			}
			close(readerSnapshot)
			out, _, err = svc.fulfillOrderTx(tx, account.ID, 1, 1, "wait-for-sku", "", "")
			return err
		})
		readerDone <- result{out, err}
	}()
	select {
	case <-readerSnapshot:
	case result := <-readerDone:
		t.Fatalf("supply reader failed before snapshot: %v", result.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case result := <-readerDone:
		t.Fatalf("supply escaped the product write lock: %v %v", result.out, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	// An early test failure also releases the writer before database cleanup.
	release()
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case result := <-readerDone:
		if result.err != nil || result.out == nil || !result.out.rejected || result.out.errCode != "product_unavailable" {
			t.Fatalf("newly committed SKU escaped the supply guard: %v %v", result.out, result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if balance, err := repo.BalanceOf(ctx, account.ID); err != nil || balance != 10000 {
		t.Fatalf("rejected supply changed the balance: %d %v", balance, err)
	}
	if n, err := d.Client.SupplyOrder.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("rejected supply created orders: %d %v", n, err)
	}
	if n, err := d.Client.Card.Query().Where(card.StatusEQ(card.StatusAvailable)).Count(ctx); err != nil || n != 1 {
		t.Fatalf("rejected supply consumed card stock: %d %v", n, err)
	}
}
