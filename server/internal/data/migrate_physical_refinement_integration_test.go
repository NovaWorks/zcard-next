//go:build integration

package data

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
)

func TestPhysicalRefinementHistoricalUpgradeMySQL(t *testing.T) {
	physicalRefinementHistoricalUpgrade(t, db.MySQL, "ZCARD_TEST_MYSQL_DSN")
}

func TestPhysicalRefinementHistoricalUpgradePG(t *testing.T) {
	physicalRefinementHistoricalUpgrade(t, db.Postgres, "ZCARD_TEST_PG_DSN")
}

func physicalRefinementHistoricalUpgrade(t *testing.T, dialect db.Dialect, env string) {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv(env))
	if adminDSN == "" {
		t.Skipf("integration DSN %s not configured", env)
	}
	admin, err := dialect.Open(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	target := fmt.Sprintf("zcard_physical_upgrade_%d", time.Now().UnixNano())
	dsn := adminDSN
	if dialect == db.MySQL {
		if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+target+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
			t.Fatal(err)
		}
		defer admin.ExecContext(ctx, "DROP DATABASE "+target)
		base, query, _ := strings.Cut(dsn, "?")
		dsn = base[:strings.LastIndex(base, "/")+1] + target
		if query != "" {
			dsn += "?" + query
		}
	} else {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+target); err != nil {
			t.Fatal(err)
		}
		defer admin.ExecContext(ctx, "DROP SCHEMA "+target+" CASCADE")
		query := u.Query()
		query.Set("search_path", target)
		u.RawQuery = query.Encode()
		dsn = u.String()
	}
	d, cleanup, err := NewData(&conf.Data{Database: &conf.Data_Database{Driver: string(dialect), Source: dsn, MaxOpenConns: 5, MaxIdleConns: 5}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	physicalRefinementUpgradePreservesCommerceAndPreferences(t, d, dialect, dsn)
}
