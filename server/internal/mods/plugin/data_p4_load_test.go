package plugin

import (
	"bytes"
	"context"
	entbase "entgo.io/ent"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestP4ConcurrentGenerationsAndResourceBudget(t *testing.T) {
	m, d, ctx, key := p4Manager(t, t.TempDir())
	f := signedPackage(t, key, "0.1.0", nil)
	f2 := signedPackage(t, key, "0.1.1", nil)
	if _, err := m.Import(ctx, command(f, "import", 0), f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Operate(ctx, command(f, "enable", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.packages.Stage(ctx, f2.descriptor, f2.signature, bytes.NewReader(f2.archive)); err != nil {
		t.Fatal(err)
	}
	level := d.Client.MemberLevel.Create().SetName("P4").SetAcquireMode("manual").SaveX(ctx)
	var items []port.PurchaseItem
	for i := 0; i < 3; i++ {
		p := d.Client.Product.Create().SetName("P4").SetSlug(fmt.Sprint("p4-", i)).SetPrice(100).SaveX(ctx)
		items = append(items, port.PurchaseItem{ProductID: p.ID, Quantity: 1})
		if _, err := m.repo.Save(ctx, port.SaveConfig{Key: port.RuleKey{ProductID: p.ID, PluginID: f.artifact.Manifest.ID}, Actor: command(f, "enable", 1).Actor, Expected: port.Expected{Generation: 2, SchemaVersion: 1}, Config: pc.Config{SchemaVersion: 1, Revision: "0", Enabled: true, AllowedLevelIDs: []pc.Decimal{decimal(level.ID)}}}); err != nil {
			t.Fatal(err)
		}
	}
	gate := NewRequiredGate(m.repo)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutines := runtime.NumGoroutine()
	iterations := 500
	if os.Getenv("ZCARD_P4_SOAK") == "1" {
		iterations = 25000
	}
	start := time.Now()
	var wg sync.WaitGroup
	fail := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s, err := gate.Begin(ctx)
				if err != nil {
					fail <- err
					return
				}
				decisions, err := s.Check(ctx, port.PurchaseInput{UserID: 1, LevelID: level.ID, Channel: "storefront", Items: items})
				s.Release()
				if err != nil {
					fail <- err
					return
				}
				if len(decisions) != 3 {
					fail <- fmt.Errorf("missing decisions")
					return
				}
				for _, v := range decisions {
					if v["generation"] != decisions[0]["generation"] || v["digest"] != decisions[0]["digest"] || v["config_revision"] != "1" {
						fail <- fmt.Errorf("mixed snapshot: %+v", decisions)
						return
					}
				}
			}
		}()
	}
	for gen := uint64(2); gen < 42; gen++ {
		target := f2
		if gen%2 == 1 {
			target = f
		}
		if _, err := m.Operate(ctx, command(target, "upgrade", gen)); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+32<<20 {
		t.Fatalf("heap growth %d", after.HeapAlloc-before.HeapAlloc)
	}
	if runtime.NumGoroutine() > goroutines+8 {
		t.Fatal("goroutines leaked")
	}
	m.coordinator.mu.Lock()
	defer m.coordinator.mu.Unlock()
	if m.coordinator.draining != 0 || len(m.coordinator.slots) != 1 {
		t.Fatal("retired generations leaked")
	}
	for _, s := range m.coordinator.slots {
		if s.refs != 0 {
			t.Fatal("lease leaked")
		}
	}
	t.Logf("%d requests / %d hooks / 40 upgrades: elapsed=%s heap_before=%d heap_after=%d goroutines_before=%d after=%d draining=0", iterations*4, iterations*12, time.Since(start), before.HeapAlloc, after.HeapAlloc, goroutines, runtime.NumGoroutine())
}

func TestP4PublishedRuntimeOnConfirmationFailure(t *testing.T) {
	m, d, ctx, key := p4Manager(t, t.TempDir())
	f := signedPackage(t, key, "0.1.0", nil)
	if _, err := m.Import(ctx, command(f, "import", 0), f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Operate(ctx, command(f, "enable", 1)); err != nil {
		t.Fatal(err)
	}
	next := signedPackage(t, key, "0.1.1", func(files map[string][]byte) {
		files["main.js"] = []byte(`function evaluate(i){return {allow:false,reason:'MEMBER_LEVEL_DENIED'}}`)
	})
	var inject = true
	d.Client.InstalledPlugin.Use(func(next entbase.Mutator) entbase.Mutator {
		return entbase.MutateFunc(func(ctx context.Context, mutation entbase.Mutation) (entbase.Value, error) {
			if _, ok := mutation.Field("observed_generation"); ok && mutation.Op().Is(entbase.OpUpdateOne) && inject {
				inject = false
				return nil, errors.New("P4 confirmation failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	c := command(next, "upgrade", 2)
	if _, err := m.Import(ctx, c, next.descriptor, next.signature, bytes.NewReader(next.archive)); err == nil {
		t.Fatal("hidden commit failure")
	}
	state, err := m.Status(ctx, c.PluginID)
	if err != nil || state.State.ObservedGeneration != 3 || !state.State.ReconciliationPending {
		t.Fatalf("wrong observed state %+v %v", state, err)
	}
	l, err := m.coordinator.Acquire(ctx, c.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := l.Evaluate(ctx, pc.Input{SchemaVersion: 1, Hook: pc.HookOrderPreCreate, PluginID: c.PluginID, Generation: "3", SubsiteID: "0", ProductID: "1", SKUID: "0", Quantity: 1, Channel: "storefront", Member: pc.Member{Authenticated: true, EffectiveLevelID: "1"}, Config: pc.Config{SchemaVersion: 1, Revision: "1", Enabled: true, AllowedLevelIDs: []pc.Decimal{"1"}}})
	l.Release()
	if err != nil || out.Allow || out.Reason != pc.MemberLevelDenied {
		t.Fatalf("new actual script not executed %+v %v", out, err)
	}
	if _, err = m.Operate(ctx, c); err != nil {
		t.Fatal(err)
	}
	state, err = m.Status(ctx, c.PluginID)
	if err != nil || state.State.ObservedGeneration != 3 || state.State.ReconciliationPending {
		t.Fatalf("duplicate switch %+v %v", state, err)
	}
}
