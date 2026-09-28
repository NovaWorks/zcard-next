package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/pluginstorage"
)

func TestPublicationCrashBoundaries(t *testing.T) {
	for _, point := range []string{"staged", "preparing", "ready", "published"} {
		t.Run(point, func(t *testing.T) {
			s, _, ctx, f := serviceFixture(t)
			m := s.manager
			c := command(f, "enable", 1)
			if point != "staged" {
				op, _, err := m.repo.begin(ctx, c, commandHash(c))
				if err != nil {
					t.Fatal(err)
				}
				if point != "preparing" {
					op.TargetGeneration = 2
					op.Phase = port.PhaseReady
					if err = m.repo.intent(ctx, op, true, false); err != nil {
						t.Fatal(err)
					}
					if point == "published" {
						m.coordinator.mu.Lock()
						m.coordinator.publishLocked(c.PluginID, 2, &testRuntime{})
						m.coordinator.mu.Unlock()
					}
				}
			}
			recovered := NewManager(NewRepo(m.repo.data, NewCoordinator(), nil), m.packages, &testLoader{})
			if err := recovered.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := recovered.Status(ctx, c.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			committed := point == "ready" || point == "published"
			expected := uint64(1)
			if committed {
				expected = 2
			}
			if state.State.DesiredEnabled != committed || state.State.ObservedGeneration != expected || state.State.ReconciliationPending {
				t.Fatalf("wrong recovery at %s: %+v", point, state)
			}
			if _, err = recovered.Operate(ctx, c); err != nil {
				t.Fatal("same operation retry", err)
			}
			state, err = recovered.Status(ctx, c.PluginID)
			if err != nil || state.State.ObservedGeneration != 2 {
				t.Fatalf("retry duplicated or lost operation: %+v %v", state, err)
			}
		})
	}
}

func TestArtifactRetentionAndDatabaseRestore(t *testing.T) {
	p, key := testPackages(t)
	ctx := context.Background()
	var digests []string
	for i := 0; i < 8; i++ {
		f := signedPackage(t, key, fmt.Sprintf("0.1.%d", i), nil)
		if _, err := p.Stage(ctx, f.descriptor, f.signature, bytes.NewReader(f.archive)); err != nil {
			t.Fatal(err)
		}
		sha := f.artifact.Descriptor.ArchiveSHA256
		digests = append(digests, sha)
		stamp := time.Now().Add(time.Duration(i-20) * time.Hour)
		if err := os.Chtimes(filepath.Join(p.root, f.artifact.Manifest.ID, sha), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	f := signedPackage(t, key, "0.1.9", nil)
	if _, err := p.Stage(ctx, f.descriptor, f.signature, bytes.NewReader(f.archive)); err == nil {
		t.Fatal("unbounded versions")
	}
	stale := filepath.Join(p.root, f.artifact.Manifest.ID, ".staging-abandoned")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(stale, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := p.Prune(ctx, map[string]bool{digests[0]: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("abandoned staging retained")
	}
	for i, sha := range digests {
		_, r, err := p.Open(ctx, sha)
		keep := i == 0 || i >= 3
		if keep != (err == nil) {
			t.Fatalf("retention[%d] keep=%v error=%v", i, keep, err)
		}
		if r != nil {
			r.Close()
		}
	}
	s, d, scope, installed := serviceFixture(t)
	old := s.manager.packages.(*FilePackages)
	oldRoot := t.TempDir()
	secret, err := pluginstorage.EnsureKey(oldRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	restored := t.TempDir()
	if err = os.CopyFS(filepath.Join(restored, "plugins"), os.DirFS(old.root)); err != nil {
		t.Fatal(err)
	}
	if err = os.CopyFS(filepath.Join(restored, "secrets"), os.DirFS(filepath.Join(oldRoot, "secrets"))); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(restored, "secrets/order-idempotency/v1.key"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(restored, "restored.db")
	if _, err = d.DB.ExecContext(scope, "VACUUM INTO ?", database); err != nil {
		t.Fatal(err)
	}
	copyDB, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: database}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	store, err := NewFilePackages(filepath.Join(restored, "plugins"), old.keys, old.host)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(NewRepo(copyDB, NewCoordinator(), nil), store, nil)
	if err = manager.InitializeStorage(scope, restored); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Status(scope, installed.artifact.Manifest.ID)
	if err != nil || after.State.ObservedGeneration != 1 || after.State.DesiredEnabled {
		t.Fatalf("restore mismatch: %+v %v", after, err)
	}
	got, err := pluginstorage.EnsureKey(restored, false)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatal("restore rotated host key", err)
	}
}

func TestRecoveryDisableSupersedesPendingOperation(t *testing.T) {
	s, _, ctx, f := serviceFixture(t)
	pending := command(f, "enable", 1)
	op, _, err := s.manager.repo.begin(ctx, pending, commandHash(pending))
	if err != nil {
		t.Fatal(err)
	}
	op.TargetGeneration = 2
	op.Phase = port.PhaseReady
	if err = s.manager.repo.intent(ctx, op, true, false); err != nil {
		t.Fatal(err)
	}
	disable := command(f, "disable", 2)
	disable.TargetDigest = ""
	if _, err = s.manager.Operate(ctx, disable); err != nil {
		t.Fatal(err)
	}
	result, err := s.manager.Operate(ctx, pending)
	if err == nil || result.Command.OperationID != pending.OperationID || result.Phase != port.PhaseFailed {
		t.Fatalf("superseded retry returned another command: %+v %v", result, err)
	}
	state, err := s.manager.Status(ctx, f.artifact.Manifest.ID)
	if err != nil || state.State.DesiredGeneration != 3 || state.State.DesiredEnabled {
		t.Fatalf("old intent reactivated: %+v %v", state, err)
	}
}
