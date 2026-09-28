package memberlevel

import (
	"context"
	"errors"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginrulelevelref"
	pluginport "github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

type testPluginReferences struct {
	data    *data.Data
	failure bool
}

func (s testPluginReferences) ListForProducts(context.Context, uint64, []uint64) ([]pluginport.Requirement, error) {
	return nil, nil
}
func (s testPluginReferences) LevelReferenced(ctx context.Context, id uint64) (bool, error) {
	if s.failure {
		return false, errors.New("reference store unavailable")
	}
	return data.Client(ctx, s.data).PluginRuleLevelRef.Query().Where(pluginrulelevelref.LevelID(id)).Exist(ctx)
}
func TestDeleteLevelRequiresGlobalPluginRelease(t *testing.T) {
	d, _, _, wallet := newMemberLevelEnv(t)
	repo := ProvideProtectedMemberLevelRepo(d, walletPorts{repo: wallet}, testPluginReferences{data: d})
	ctx := tenancy.WithContext(context.Background(), tenancy.Main())
	lv := d.Client.MemberLevel.Create().SetName("referenced").SetThresholdType("recharge").SetThresholdRecharge(0).SetThresholdConsume(0).SaveX(ctx)
	ref := d.Client.PluginRuleLevelRef.Create().SetPluginID("member-purchase-gate").SetSubsiteID(99).SetProductID(42).SetLevelID(lv.ID).SaveX(ctx)
	if err := repo.DeleteLevel(ctx, lv.ID); err == nil {
		t.Fatal("main site deleted a level referenced by another site")
	}
	d.Client.PluginRuleLevelRef.DeleteOneID(ref.ID).ExecX(ctx)
	repo.pluginRules = testPluginReferences{data: d, failure: true}
	if err := repo.DeleteLevel(ctx, lv.ID); err == nil {
		t.Fatal("reference read failure allowed deletion")
	}
	repo.pluginRules = testPluginReferences{data: d}
	if err := repo.DeleteLevel(ctx, lv.ID); err != nil {
		t.Fatal(err)
	}
}
