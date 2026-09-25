//go:build integration

package supply

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/testint"
)

func TestCatalogSnapshotSQLDialects(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *testint.Harness{"mysql": testint.MySQL, "postgres": testint.PG} {
		t.Run(name, func(t *testing.T) {
			h := open(t)
			r := NewSupplyRepoImpl(h.Data, newTestBox(t))
			conn := mustConn(t, r, h.Data, "catalog migration")
			s := NewAdminSupplyService(r, nil)
			a := &catalogTestAdapter{}
			attachCatalogAdapter(s, a)
			ctx := context.Background()
			ids := make(chan uint64, 12)
			var wg sync.WaitGroup
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					row, e := s.ensureCatalogSnapshot(ctx, conn.ID, false)
					if e != nil {
						t.Error(e)
						return
					}
					ids <- row.ID
					s.runCatalogSnapshot(row.ID)
				}()
			}
			wg.Wait()
			close(ids)
			var first uint64
			for id := range ids {
				if first == 0 {
					first = id
				}
				if id != first {
					t.Fatal("multiple snapshots created concurrently")
				}
			}
			ready := awaitCatalog(t, s, first, "ready")
			if a.calls.Load() != 1 {
				t.Fatal("duplicate catalog request", a.calls.Load())
			}
			if _, _, e := s.readCatalogSnapshot(ctx, conn, ready.Token); e != nil {
				t.Fatal(e)
			}
			// Actual migration must widen the column, not just loosen the Go validator.
			writer := catalog.NewProductRepoImpl(h.Data, nil)
			for _, name := range []string{strings.Repeat("中", 100), strings.Repeat("🔑", 100)} {
				saved, e := writer.CreateCategory(ctx, name, 0, "", 0)
				if e != nil {
					t.Fatal(e)
				}
				actual := h.Data.Client.Category.GetX(ctx, saved.ID)
				if actual.Name != name {
					t.Fatal("unicode category was truncated")
				}
			}
		})
	}
}

// Run in a separate test process with ZCARD_HTTPX_ALLOW_PRIVATE=1 for this
// loopback-only gateway. Production SSRF configuration is never changed.
func TestCatalogSlowUpstreamHTTP(t *testing.T) {
	if os.Getenv("ZCARD_HTTPX_ALLOW_PRIVATE") != "1" {
		t.Skip("local gateway integration requires explicit loopback test mode")
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shared/commodity/items" {
			t.Errorf("unexpected upstream call %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		calls.Add(1)
		select {
		case <-time.After(31 * time.Second):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":[{"id":1,"name":"全球海外邮箱--德国--俄罗斯--拉脱维亚--挪威 等国家本土高权重邮箱","children":[{"code":"p","name":"测试商品","price":10,"status":1}]}]}`))
	}))
	defer upstream.Close()
	repo, d := newTestRepo(t)
	conn := mustConn(t, repo, d, "slow upstream")
	sealed, e := repo.SealCredentials("acg_faka", upstream.URL, `{"app_id":"fixture","app_key":"fixture"}`)
	if e != nil {
		t.Fatal(e)
	}
	conn = d.Client.SupplyConnection.UpdateOneID(conn.ID).SetBaseURL(upstream.URL).SetDriver("acg_faka").SetCredentials(sealed).SaveX(context.Background())
	s := NewAdminSupplyService(repo, nil)
	handler := khttp.NewServer()
	adminv1.RegisterAdminSupplyServiceHTTPServer(handler, s)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/admin/supply/connections/%d/preview?async=true", conn.ID), nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	handler.ServeHTTP(rec, req)
	cancel()
	var reply adminv1.PreviewProductsReply
	if rec.Code != 200 || protojson.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Status != "pending" || reply.SnapshotId == "" {
		t.Fatalf("async route: %d %s", rec.Code, rec.Body)
	}
	if time.Since(start) > time.Second {
		t.Fatal("preview start waited for upstream")
	}
	var ready *adminv1.PreviewProductsReply
	for time.Since(start) < 40*time.Second {
		time.Sleep(250 * time.Millisecond)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/v1/admin/supply/connections/%d/preview?snapshot_id=%s", conn.ID, reply.SnapshotId), nil))
		var current adminv1.PreviewProductsReply
		if rec.Code != 200 || protojson.Unmarshal(rec.Body.Bytes(), &current) != nil {
			t.Fatalf("poll: %d %s", rec.Code, rec.Body)
		}
		if current.Status == "failed" {
			t.Fatal(current.Message)
		}
		if current.Status == "ready" {
			ready = &current
			break
		}
	}
	if ready == nil || ready.Total != 1 || calls.Load() != 1 {
		t.Fatalf("slow catalog failed: ready=%v calls=%d", ready, calls.Load())
	}
	t.Logf("31-second upstream completed in %.2fs after request cancellation; polling reused one upstream request", time.Since(start).Seconds())
}
