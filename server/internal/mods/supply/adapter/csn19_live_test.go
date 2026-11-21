package adapter

import (
	"context"
	"os"
	"testing"
	"time"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// Opt-in live validation invokes only balance/services. Keep the credential in
// the process environment; never put it in a fixture, test name, or log output.
func TestCSN19LiveReadOnly(t *testing.T) {
	key := os.Getenv("ZCARD_CSN19_LIVE_KEY")
	if key == "" {
		t.Skip("set ZCARD_CSN19_LIVE_KEY for read-only provider validation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	a, err := New("csn19", "https://19csn.com/api/v2", Credentials{APIKey: key}, nil)
	if err != nil {
		t.Fatal("construct live adapter:", err)
	}
	account, err := a.Ping(ctx)
	if err != nil {
		t.Fatal("read live balance:", err)
	}
	t.Logf("balance read succeeded; currency=%s", account.Currency)
	provider := a.(supplyport.SocialProvider)
	services, err := provider.SocialServices(ctx)
	if err != nil {
		t.Fatal("read live services:", err)
	}
	profiles := map[string]int{}
	purchasable := 0
	for _, service := range services {
		profiles[service.Schema.Profile]++
		if service.Schema.Billing.Currency != account.Currency {
			t.Fatal("service currency does not match live account")
		}
		if service.Schema.Purchasable {
			purchasable++
		}
	}
	if len(services) == 0 {
		t.Fatal("live account returned no services")
	}
	t.Logf("services parsed=%d; purchasable=%d; profiles=%v", len(services), purchasable, profiles)
}
