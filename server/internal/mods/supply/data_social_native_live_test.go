package supply

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
)

// Freeze the authenticated read for local import/quote checks. This fixture has
// no live mutation implementation even if the journey changes in the future.
type readOnlyLiveCatalog struct{ rows []supplyport.SocialService }

func (c readOnlyLiveCatalog) SocialServices(context.Context) ([]supplyport.SocialService, error) {
	return c.rows, nil
}
func (readOnlyLiveCatalog) CreateProviderSocial(context.Context, supplyport.SocialProviderPurchase) (*supplyport.SocialProviderOrder, error) {
	return nil, errors.New("live test excludes provider purchases")
}
func (readOnlyLiveCatalog) QueryProviderSocial(context.Context, string) (*supplyport.SocialProviderOrder, error) {
	return nil, errors.New("live test excludes provider order access")
}
func (readOnlyLiveCatalog) ActProviderSocial(context.Context, string, string, string) (*supplyport.SocialOperation, error) {
	return nil, errors.New("live test excludes provider mutations")
}
func (readOnlyLiveCatalog) QueryProviderSocialOperation(context.Context, string, string) (*supplyport.SocialOperation, error) {
	return nil, errors.New("live test excludes provider operations")
}

func TestCSN19LiveImportAndQuoteReadOnly(t *testing.T) {
	key := os.Getenv("ZCARD_CSN19_LIVE_KEY")
	if key == "" {
		t.Skip("set ZCARD_CSN19_LIVE_KEY for read-only provider validation")
	}
	ctx, cancel := context.WithTimeout(identity.WithClaims(context.Background(), &authn.Claims{Subject: 7, Realm: authn.RealmUser}), 90*time.Second)
	defer cancel()
	repo, d := newTestRepo(t) // In-memory SQLite; credentials never persist to disk.
	writer := catalog.NewProductRepoImpl(d, nil)
	syncer := NewSyncService(repo, writer, writer, nil, nil, nil, slog.Default())
	admin := NewAdminSupplyService(repo, syncer)
	credentials, _ := json.Marshal(map[string]string{"api_key": key})
	connection, err := admin.CreateConnection(ctx, &adminv1.CreateConnectionRequest{Name: "19csn read-only live test", Driver: "csn19", BaseUrl: "https://19csn.com/api", Credentials: string(credentials), ExchangeRate: 1, PriceMarkupPercent: 10, AutoSyncPrice: true})
	if err != nil {
		t.Fatal("create isolated connection:", err)
	}
	gateway := NewGateway(repo, nil, writer)
	client, err := gateway.OpenSocial(ctx, connection.Id)
	if err != nil {
		t.Fatal("open native client:", err)
	}
	native := client.(*nativeSocialClient)
	services, err := native.provider.SocialServices(ctx)
	if err != nil {
		t.Fatal("read authenticated services:", err)
	}
	native.provider = readOnlyLiveCatalog{rows: services}
	for _, service := range services {
		if service.Schema.Profile != "Default" || !service.Schema.Purchasable || service.Schema.Billing.Currency != "CNY" {
			continue
		}
		raw, _ := json.Marshal(service.Schema)
		var private map[string]any
		_ = json.Unmarshal(raw, &private)
		if _, err := syncer.ImportOne(ctx, native.conn, &adapter.Product{ID: service.ID, Name: service.Name, IsActive: true, ProductKind: supplyport.SocialProductKind, DeliveryKind: supplyport.SocialDelivery, SocialSchema: private, Stock: -2}, nil, PriceModeChannel, 0, 0); err != nil {
			t.Fatal("import live service into isolated database:", err)
		}
		product := d.Client.Product.Query().OnlyX(ctx)
		public, err := catalog.NewStoreCatalogService(catalog.NewCatalogUsecase(writer), nil, nil).GetProduct(ctx, &storefrontv1.GetProductRequest{Id: product.ID})
		if err != nil || public.GetProductKind() != supplyport.SocialProductKind {
			t.Fatal("read imported storefront service:", err)
		}
		if _, exposed := public.SocialSchema.AsMap()["billing"].(map[string]any)["rate"]; exposed {
			t.Fatal("catalog exposed provider rate")
		}
		schema, err := native.SocialSchema(ctx, service.ID)
		if err != nil {
			t.Fatal("read local purchase schema:", err)
		}
		quantity := max(1, *service.Schema.Quantity.Min)
		quote, err := native.QuoteSocial(ctx, service.ID, supplyport.SocialParameters{"link": "https://example.com/read-only-test", "quantity": strconv.FormatInt(quantity, 10)}, schema.Revision)
		if err != nil || quote == nil || quote.AmountCents <= 0 || quote.Currency != "CNY" {
			t.Fatal("local quote from live account price:", err)
		}
		amount, _, err := gateway.PriceSocialRetail(ctx, connection.Id, product.ID, int64(quote.AmountCents))
		if err != nil || amount < int64(quote.AmountCents) {
			t.Fatal("local retail pricing:", err)
		}
		if d.Client.Order.Query().CountX(ctx) != 0 || d.Client.SocialTask.Query().CountX(ctx) != 0 {
			t.Fatal("read-only journey created an order or procurement task")
		}
		t.Logf("live catalog=%d; encrypted connection, isolated import, public catalog and CNY quote passed; no order submitted", len(services))
		return
	}
	t.Fatal("live account has no supported CNY Default service")
}
