package catalog

import (
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"strings"
	"testing"
)

func TestSMSPublicCatalogHidesLegacyChannelTitle(t *testing.T) {
	name := "SmSCode · 接码服务"
	desc := "<h1>" + name + "</h1><p>服务说明</p>"
	public := toStorefrontProduct(&port.Product{ProductKind: "sms_channel", Name: name, Description: desc}, nil, 0)
	feed := toSupplierProduct(&ent.Product{ProductKind: "sms_channel", Name: name, Description: desc})
	if public.Name != "接码服务" || feed.Name != "接码服务" || strings.Contains(public.Description, "SmSCode") || strings.Contains(feed.Description, "SmSCode") {
		t.Fatal("channel identity leaked to customer or reseller catalog")
	}
}
