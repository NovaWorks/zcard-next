package supply

import (
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/upstreamsource"
	"testing"
)

func TestNavigationDoesNotChangeCardConfiguration(t *testing.T) {
	v := &cardContext{conn: &ent.SupplyConnection{}, product: &ent.Product{ID: 7}, mapping: &ent.SupplyMapping{}}
	before := cardConfiguration(v)
	v.product.UpstreamNavigation = upstreamsource.Navigation(9, "CODE", "1111")
	if cardConfiguration(v) != before {
		t.Fatal("navigation invalidated purchase quote")
	}
}
