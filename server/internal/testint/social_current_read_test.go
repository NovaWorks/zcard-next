//go:build integration

package testint

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/socialtask"
)

// A connection-row mutex alone does not refresh an established MySQL RR
// snapshot. Duplicate provider bindings must be checked with a current read.
func TestSocialProviderBindingCurrentReadMySQL(t *testing.T) {
	h := MySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := h.Data.Client
	conn := c.SupplyConnection.Create().SetName("binding-current-read").SetDriver("zcard").SetBaseURL("https://supplier.example.test").SetCredentials([]byte("fixture")).SaveX(ctx)
	task := c.SocialTask.Create().SetID("binding-original").SetAccountID(1).SetConnectionID(conn.ID).SetQuoteID("quote-original").SetSchemaRevision("schema-original").SetRequestNo("request-original").SetRequestHash("hash-original").SetUpstreamProductID("service-original").SaveX(ctx)
	tx, err := c.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Establish the old snapshot before another transaction finishes binding.
	if _, err = tx.SocialTask.Get(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	const providerID = "9007199254740999"
	if err = c.SocialTask.UpdateOneID(task.ID).SetUpstreamOrderID(providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	stale, err := tx.SocialTask.Query().Where(socialtask.ConnectionID(conn.ID), socialtask.UpstreamOrderID(providerID)).Exist(ctx)
	if err != nil || stale {
		t.Fatal("negative control did not retain the old RR snapshot", stale, err)
	}
	if err = tx.SupplyConnection.UpdateOneID(conn.ID).AddRetryMax(0).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	latest, err := tx.SocialTask.Query().Where(socialtask.ConnectionID(conn.ID), socialtask.UpstreamOrderID(providerID)).Limit(1).ForUpdate().All(ctx)
	if err != nil || len(latest) != 1 || latest[0].ID != task.ID {
		t.Fatal("current-read duplicate check missed the committed provider binding", len(latest), err)
	}
}
