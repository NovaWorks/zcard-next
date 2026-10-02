//go:build integration

package supply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
	"github.com/google/uuid"
)

func TestCatalogSnapshotMySQLLargePayloadReuse(t *testing.T) {
	h := testint.MySQL(t)
	ctx := context.Background()
	// Keep the isolated test pool on one session so the catalog transaction
	// uses MySQL's default-sized 256 KiB sort buffer.
	h.Data.DB.SetMaxOpenConns(1)
	h.Data.DB.SetMaxIdleConns(1)
	if _, err := h.Data.DB.ExecContext(ctx, "SET SESSION sort_buffer_size = 262144"); err != nil {
		t.Fatal(err)
	}
	r := NewSupplyRepoImpl(h.Data, newTestBox(t))
	s := NewAdminSupplyService(r, nil)
	conn := mustConn(t, r, h.Data, "large catalog")
	now := time.Now()
	identity := catalogIdentity(conn)
	create := func(connectionID, tenant uint64, id string, expires int64, payload json.RawMessage) *ent.SupplyCatalogSnapshot {
		t.Helper()
		row, err := h.Data.Client.SupplyCatalogSnapshot.Create().
			SetConnectionID(connectionID).SetSubsiteID(tenant).SetIdentity(id).
			SetToken(uuid.NewString()).SetStatus("ready").SetLoadedCount(1).
			SetExpiresAt(expires).SetCreatedAt(now.Add(-time.Minute)).SetPayload(payload).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	small := json.RawMessage(`{"categories":[],"products":{}}`)
	for range 4 {
		create(conn.ID, 0, identity, now.Add(time.Hour).Unix(), small)
	}
	description := strings.Repeat("catalog detail ", 150000)
	payload, err := json.Marshal(catalogPayload{Products: map[string]adapter.Product{
		"large": {ID: "large", Name: "large catalog product", Description: description},
	}})
	if err != nil {
		t.Fatal(err)
	}
	latest := create(conn.ID, 0, identity, now.Add(time.Hour).Unix(), payload)
	// Newer rows outside the tenant, credential identity or expiry window must
	// not replace the latest valid snapshot.
	create(conn.ID, 99, identity, now.Add(time.Hour).Unix(), small)
	create(conn.ID, 0, "old-credential-identity", now.Add(time.Hour).Unix(), small)
	create(conn.ID, 0, identity, now.Add(-time.Minute).Unix(), small)
	// Multiple suppliers make the real connection/tenant index selective. The
	// unmodified query naturally filesorts without any index hints.
	for supplier := range 16 {
		other := mustConn(t, r, h.Data, fmt.Sprintf("other catalog %d", supplier))
		for range 8 {
			create(other.ID, 0, catalogIdentity(other), now.Add(time.Hour).Unix(), small)
		}
	}
	if _, err := h.Data.DB.ExecContext(ctx, "ANALYZE TABLE supply_catalog_snapshots"); err != nil {
		t.Fatal(err)
	}
	t.Logf("payload=%d bytes; sort_buffer_size=262144", len(payload))

	row, err := s.ensureCatalogSnapshot(ctx, conn.ID, false)
	if err != nil {
		t.Fatalf("reuse large catalog snapshot: %v", err)
	}
	if row.ID != latest.ID || row.Token != latest.Token || row.Status != "ready" {
		t.Fatalf("reused wrong snapshot: id=%d token=%q status=%q; want id=%d", row.ID, row.Token, row.Status, latest.ID)
	}
	entry, err := decodeCatalog(row, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.byCode) != 1 || entry.byCode["large"].Description != description {
		t.Fatal("large catalog payload did not round trip completely")
	}
	refreshed, err := s.ensureCatalogSnapshot(ctx, conn.ID, true)
	if err != nil {
		t.Fatalf("refresh large catalog snapshot: %v", err)
	}
	if refreshed.ID == latest.ID || refreshed.Token == latest.Token || refreshed.Status != "pending" {
		t.Fatalf("refresh did not create a new pending snapshot: id=%d token=%q status=%q", refreshed.ID, refreshed.Token, refreshed.Status)
	}
	pending, err := s.ensureCatalogSnapshot(ctx, conn.ID, true)
	if err != nil || pending.ID != refreshed.ID {
		t.Fatalf("refresh did not reuse pending snapshot: row=%v err=%v", pending, err)
	}
}
