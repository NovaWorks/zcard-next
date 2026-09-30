package audit

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/risklockkey"
)

// DSNs must point to disposable databases. Apply the real migration chain, not Schema.Create.
func TestFetchPasswordSQLDialects(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			source := os.Getenv("ZCARD_FETCH_TEST_" + strings.ToUpper(driver) + "_DSN")
			if source == "" {
				t.Skip("isolated database not configured")
			}
			d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: driver, Source: source, MaxOpenConns: 8}})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			ctx := context.Background()
			if err = data.ApplyMigrations(ctx, d.DB, d.Dialect, source); err != nil {
				t.Fatal(err)
			}
			r := NewAuditRepo(d, testLogger())
			key := "fetch:dialect:" + time.Now().Format("150405.000000000")
			for i := 1; i <= 4; i++ {
				s, err := r.RecordFetchFailure(ctx, key)
				if err != nil || s.Failures != i || s.Locked() {
					t.Fatal(s, err)
				}
			}
			if err = r.ResetFetchFailures(ctx, key); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := NewAuditRepo(d, testLogger()).RecordFetchFailure(ctx, key); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			s, err := r.FetchPasswordState(ctx, key)
			if err != nil || !s.Locked() || s.Failures != 5 {
				t.Fatal(s, err)
			}
			_, err = d.Client.RiskLockKey.Update().Where(risklockkey.KeyHash(hashKey(key))).SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			s, err = r.RecordFetchFailure(ctx, key)
			if err != nil || s.Failures != 1 || s.Locked() {
				t.Fatal(s, err)
			}
		})
	}
}
