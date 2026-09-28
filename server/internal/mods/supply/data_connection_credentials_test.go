package supply

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

func TestConnectionURLChangePreservesCredentials(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepo(t)
	svc := NewAdminSupplyService(repo, nil)
	conn := mustConn(t, repo, nil, "URL edit")
	want, err := repo.OpenCredentials(conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://8.8.8.8/api/supply", "https://8.8.8.8", "https://8.8.8.8/"} {
		oldCiphertext := append([]byte(nil), conn.Credentials...)
		if _, err := svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: conn.ID, BaseUrl: base}); err != nil {
			t.Fatal(err)
		}
		conn, err = repo.GetConnection(ctx, conn.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.OpenCredentials(conn)
		if err != nil || got != want || conn.BaseURL != base || bytes.Equal(oldCiphertext, conn.Credentials) {
			t.Fatalf("URL edit did not rebind credentials: base=%s err=%v", base, err)
		}
		// The unchanged account can load a catalog after each address edit.
		a := &catalogTestAdapter{}
		attachCatalogAdapter(svc, a)
		row, err := svc.ensureCatalogSnapshot(ctx, conn.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		svc.runCatalogSnapshot(row.ID)
		awaitCatalog(t, svc, row.ID, "ready")
		if a.calls.Load() != 1 {
			t.Fatal("catalog was not requested")
		}
	}
}

func TestBrokenCredentialsRollbackAndRecovery(t *testing.T) {
	ctx := context.Background()
	repo, d := newTestRepo(t)
	svc := NewAdminSupplyService(repo, nil)
	conn := mustConn(t, repo, nil, "broken credentials")
	// Reproduce a connection saved by the old code: URL changed, ciphertext not.
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetBaseURL("https://8.8.8.8/api/supply").SaveX(ctx)
	_, err := svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: conn.ID, BaseUrl: "https://8.8.8.8", Name: "must rollback"})
	if !errors.Is(err, adapter.ErrCredentialsInvalid) {
		t.Fatalf("expected actionable credential error: %v", err)
	}
	stored := d.Client.SupplyConnection.GetX(ctx, conn.ID)
	if stored.BaseURL != conn.BaseURL || stored.Name != conn.Name || !bytes.Equal(stored.Credentials, conn.Credentials) {
		t.Fatal("failed credential rebind did not roll back the edit")
	}
	a := &catalogTestAdapter{}
	attachCatalogAdapter(svc, a)
	row, err := svc.ensureCatalogSnapshot(ctx, conn.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	svc.runCatalogSnapshot(row.ID)
	failed := awaitCatalog(t, svc, row.ID, "failed")
	if !strings.Contains(failed.Message, "重新填写完整的账号密钥") || strings.Contains(failed.Message, "规格或账号报价") || a.calls.Load() != 0 {
		t.Fatalf("misleading credential failure: %s", failed.Message)
	}
	// Re-entering credentials can repair an old broken connection and change URL
	// together, without attempting to decrypt the unusable old ciphertext.
	want := `{"api_key":"replacement","api_secret":"replacement-secret"}`
	if _, err := svc.UpdateConnection(ctx, &adminv1.UpdateConnectionRequest{Id: conn.ID, BaseUrl: "https://8.8.8.8", Credentials: want}); err != nil {
		t.Fatal(err)
	}
	stored = d.Client.SupplyConnection.GetX(ctx, conn.ID)
	if got, err := repo.OpenCredentials(stored); err != nil || got != want {
		t.Fatalf("credential recovery failed: %v", err)
	}
	row, err = svc.ensureCatalogSnapshot(ctx, conn.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	svc.runCatalogSnapshot(row.ID)
	awaitCatalog(t, svc, row.ID, "ready")
}
