package plugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func p4Manager(t *testing.T, root string) (*Manager, *data.Data, context.Context, ed25519.PrivateKey) {
	t.Helper()
	d, closeDB, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: filepath.Join(root, "db.sqlite")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeDB)
	ctx := tenancy.WithContext(context.Background(), tenancy.Main())
	if err = d.Client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte("P4 isolated subprocess fixture"))
	key := ed25519.NewKeyFromSeed(seed[:])
	p, err := NewFilePackages(filepath.Join(root, "plugins"), map[string]ed25519.PublicKey{"test-artifact": key.Public().(ed25519.PublicKey)}, pc.Host{CoreVersion: "1.2.89", APIVersion: "1", Capabilities: map[string]bool{pc.HookOrderPreCreate: true}})
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(NewRepo(d, NewCoordinator(), nil), p, NewRuntimeLoader(p)), d, ctx, key
}

// The helper uses the real persisted transitions and real runtime, then waits
// for its parent to SIGKILL it. No production fault-injection switch is shipped.
func TestP4CrashHelper(t *testing.T) {
	root := os.Getenv("ZCARD_P4_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("ZCARD_P4_CRASH_POINT")
	m, d, ctx, key := p4Manager(t, root)
	f := signedPackage(t, key, "0.1.0", nil)
	if _, err := m.Import(ctx, command(f, "import", 0), f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Operate(ctx, command(f, "enable", 1)); err != nil {
		t.Fatal(err)
	}
	d.Client.PluginRequirement.Create().SetPluginID(f.artifact.Manifest.ID).SetProductID(42).SetSubsiteID(0).SetRequired(true).SetRevision(1).SaveX(ctx)
	next := signedPackage(t, key, "0.1.1", nil)
	if _, err := m.packages.Stage(ctx, next.descriptor, next.signature, bytes.NewReader(next.archive)); err != nil {
		t.Fatal(err)
	}
	c := command(next, "upgrade", 2)
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(root, "command.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if point != "staged" {
		op, _, err := m.repo.begin(ctx, c, commandHash(c))
		if err != nil {
			t.Fatal(err)
		}
		op.TargetGeneration = 3
		op.Phase = port.PhaseReady
		if err = m.repo.intent(ctx, op, true, false); err != nil {
			t.Fatal(err)
		}
		if point != "intent" {
			rt, err := m.loader.Prepare(ctx, next.artifact)
			if err != nil {
				t.Fatal(err)
			}
			m.coordinator.mu.Lock()
			old := m.coordinator.publishLocked(c.PluginID, 3, rt)
			m.coordinator.mu.Unlock()
			if old != nil {
				old.Close()
			}
			if point == "confirmed" {
				if err = m.repo.confirm(ctx, op); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ready"), []byte(point), 0600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestP4RealProcessCrashRecovery(t *testing.T) {
	for _, point := range []string{"staged", "intent", "published", "confirmed"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestP4CrashHelper$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "ZCARD_P4_CRASH_ROOT="+root, "ZCARD_P4_CRASH_POINT="+point)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			deadline := time.Now().Add(20 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatalf("helper timeout: %s", out.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			m, d, ctx, _ := p4Manager(t, root)
			if err := m.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			var c Command
			raw, _ := os.ReadFile(filepath.Join(root, "command.json"))
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			state, err := m.Status(ctx, c.PluginID)
			expected := uint64(3)
			if point == "staged" {
				expected = 2
			}
			if err != nil || state.State.ObservedGeneration != expected || !state.State.DesiredEnabled || state.State.ReconciliationPending {
				t.Fatalf("state %+v: %v", state, err)
			}
			if d.Client.PluginRequirement.Query().CountX(ctx) != 1 {
				t.Fatal("lost durable requirement")
			}
			if _, err = m.Operate(ctx, c); err != nil {
				t.Fatal(err)
			}
			state, err = m.Status(ctx, c.PluginID)
			if err != nil || state.State.ObservedGeneration != 3 {
				t.Fatalf("retry %+v %v", state, err)
			}
			t.Logf("SIGKILL at %s: recovered generation %d, same-operation retry generation 3, requirement retained", point, expected)
		})
	}
}

func TestP4CoreCompatibilityPreflight(t *testing.T) {
	m, _, ctx, key := p4Manager(t, t.TempDir())
	f := signedPackage(t, key, "0.1.0", nil)
	if _, err := m.Import(ctx, command(f, "import", 0), f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckCoreCompatibility(ctx); err != nil {
		t.Fatal(err)
	}
	m.packages.(*FilePackages).host.CoreVersion = "2.0.0"
	if err := m.CheckCoreCompatibility(ctx); err == nil {
		t.Fatal("incompatible target accepted")
	}
}
