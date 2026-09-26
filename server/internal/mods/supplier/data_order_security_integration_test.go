//go:build integration

package supplier

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
)

func TestSupplyConcurrentMySQL(t *testing.T) { concurrentSupplyDatabase(t, testint.MySQL(t)) }
func TestSupplyConcurrentPG(t *testing.T)    { concurrentSupplyDatabase(t, testint.PG(t)) }

func concurrentSupplyDatabase(t *testing.T, h *testint.Harness) {
	box, err := crypto.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSupplierRepoImpl(h.Data, box)
	svc := &SupplyAPIService{repo: repo, reader: &fakeCatalog{prods: []port.SupplierProduct{{ID: 1, Name: "fixture", Price: 1000, Status: 1}}}}
	exerciseConcurrentSupply(t, svc)
	// The actual migrated index must allow the same downstream number for B.
	b := seedCompatAccount(t, repo, "zcard", "concurrent-b", "secret", 10000)
	if _, err := svc.fulfillOrder(context.Background(), b.ID, 1, 1, "one-order", "", ""); err != nil {
		t.Fatal(err)
	}
	if balance, err := repo.BalanceOf(context.Background(), b.ID); err != nil || balance != 9000 {
		t.Fatalf("independent account charge: %d %v", balance, err)
	}
}
