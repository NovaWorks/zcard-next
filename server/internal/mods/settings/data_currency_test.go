package settings

import (
	"context"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"google.golang.org/protobuf/proto"
)

func TestCurrencyPrecisionPatch(t *testing.T) {
	handle, err := db.SQLite.Open(fmt.Sprintf("file:currency%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	s := NewAdminCurrencyService(d)
	if err := EnsureDefaultCurrencies(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, prec := range []int32{4, 0, 2, 8} {
		got, err := s.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "CNY", Precision: proto.Int32(prec)})
		if err != nil || got.Precision != prec || !got.Enabled || got.RateJson != "1" {
			t.Fatalf("patch%d: %+v %v", prec, got, err)
		}
	}
	got, err := s.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "CNY", Enabled: proto.Bool(false)})
	if err != nil || got.Precision != 8 || got.Enabled {
		t.Fatalf("toggle: %+v %v", got, err)
	}
	for _, prec := range []int32{-1, 9} {
		if _, err := s.UpdateCurrency(ctx, &adminv1.UpdateCurrencyRequest{Code: "CNY", Precision: proto.Int32(prec)}); err == nil {
			t.Fatalf("accepted precision %d", prec)
		}
	}
}
