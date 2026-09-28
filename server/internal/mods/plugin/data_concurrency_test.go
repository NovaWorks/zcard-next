package plugin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

// The purchase transaction deliberately locks the product BEFORE its first
// consistent read. P2 must preserve this order when integrating SubmitOrder.
func verifyFirstRuleBoundary(t *testing.T, ctx context.Context, d *data.Data, repo *Repo, id string, actor port.Actor) {
	t.Helper()
	product := d.Client.Product.Create().SetName("boundary").SetSlug(id + "-boundary").SetPrice(100).SaveX(ctx)
	level := d.Client.MemberLevel.Create().SetName("boundary").SetThresholdType("recharge").SetThresholdRecharge(0).SetThresholdConsume(0).SaveX(ctx)
	key := port.RuleKey{PluginID: id, ProductID: product.ID}
	save := port.SaveConfig{Key: key, Actor: actor, Expected: port.Expected{Generation: 2, SchemaVersion: 1}, Config: pc.Config{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIDs: []pc.Decimal{pc.Decimal(fmt.Sprint(level.ID))}}}
	done := make(chan error, 1)
	err := data.PurchaseTx(ctx, d, func(tx context.Context) error {
		if _, e := data.GuardProductWrite(tx, d, product.ID); e != nil {
			return e
		}
		before, e := repo.ListForProducts(tx, 0, []uint64{product.ID})
		if e != nil {
			return e
		}
		if len(before) != 0 {
			return fmt.Errorf("unexpected initial requirement")
		}
		started := make(chan struct{})
		go func() { close(started); _, e := repo.Save(ctx, save); done <- e }()
		<-started
		select {
		case e := <-done:
			return fmt.Errorf("save passed held product lock: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		before, e = repo.ListForProducts(tx, 0, []uint64{product.ID})
		if e != nil {
			return e
		}
		if len(before) != 0 {
			return fmt.Errorf("rule appeared inside prior purchase snapshot")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("rule writer failed to resume")
	}
	if err = data.PurchaseTx(ctx, d, func(tx context.Context) error {
		p, e := data.GuardProductWrite(tx, d, product.ID)
		if e != nil {
			return e
		}
		if p.PluginRuleRevision != 1 {
			return fmt.Errorf("anchor was not advanced")
		}
		rows, e := repo.ListForProducts(tx, 0, []uint64{product.ID})
		if e != nil {
			return e
		}
		if len(rows) != 1 || !rows[0].Required {
			return fmt.Errorf("next purchase missed first rule")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A deletion that takes the shared level lock first wins. The blocked save
	// must recheck existence after deletion commits and leave no dangling rule.
	p2 := d.Client.Product.Create().SetName("delete race").SetSlug(id + "-delete").SetPrice(100).SaveX(ctx)
	l2 := d.Client.MemberLevel.Create().SetName("delete race").SetThresholdType("recharge").SetThresholdRecharge(0).SetThresholdConsume(0).SaveX(ctx)
	save.Key.ProductID = p2.ID
	save.Config.AllowedLevelIDs = []pc.Decimal{pc.Decimal(fmt.Sprint(l2.ID))}
	err = data.RuleWriteTx(ctx, d, func(tx context.Context) error {
		c := data.Client(tx, d)
		if d.Dialect == db.SQLite {
			if _, e := c.MemberLevel.Update().Where(memberlevel.ID(l2.ID)).AddSort(0).Save(tx); e != nil {
				return e
			}
		} else {
			if _, e := c.MemberLevel.Query().Where(memberlevel.ID(l2.ID)).ForUpdate().Only(tx); e != nil {
				return e
			}
		}
		used, e := repo.LevelReferenced(tx, l2.ID)
		if e != nil {
			return e
		}
		if used {
			return fmt.Errorf("new level referenced")
		}
		go func() { _, e := repo.Save(ctx, save); done <- e }()
		select {
		case e := <-done:
			return fmt.Errorf("save passed held level lock: %v", e)
		case <-time.After(100 * time.Millisecond):
		}
		return c.MemberLevel.DeleteOneID(l2.ID).Exec(tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("save referenced deleted level")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("level writer failed to resume")
	}
	if used, e := repo.LevelReferenced(ctx, l2.ID); e != nil || used {
		t.Fatalf("dangling reference: %v %v", used, e)
	}
	if rule, e := repo.Get(ctx, save.Key); e != nil || rule.Requirement.Required || rule.Config.Revision != "0" {
		t.Fatalf("partial rule after level deletion: %+v %v", rule, e)
	}
}
