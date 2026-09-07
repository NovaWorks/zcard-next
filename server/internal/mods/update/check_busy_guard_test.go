package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"
	"github.com/go-kratos/kratos/v3/errors"
)

func TestCheckBusyGuard(t *testing.T) {
	for _, phase := range []string{PhaseChecking, PhaseBackingUp, PhaseDownloading, PhaseApplying, PhaseRestarting, PhaseVerifying} {
		t.Run(phase, func(t *testing.T) {
			s := &Service{busy: true, st: Status{Phase: phase, Target: "v9.9.9", Progress: 42, Source: "test", Err: "retained"}}
			before := s.st
			if _, err := s.Check(context.Background()); err != ErrBusy {
				t.Fatalf("got %v", err)
			}
			if !reflect.DeepEqual(before, s.st) || !s.busy {
				t.Fatal("Check changed active state")
			}
			_, err := NewAdminUpdateService(s).CheckUpdate(context.Background(), nil)
			if errors.Code(err) != 409 || errors.Reason(err) != "update.BUSY" {
				t.Fatalf("busy API returned %v", err)
			}
		})
	}
	s := &Service{st: Status{Phase: PhaseRestarting, Target: "v9.9.9"}}
	if _, err := s.Check(context.Background()); err != ErrBusy {
		t.Fatalf("pending target: %v", err)
	}
}

func TestCheckExcludesConcurrentOperations(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		http.Error(w, "test manifest failure", 500)
	}))
	defer srv.Close()
	defer close(release)
	s := &Service{binPath: filepath.Join(t.TempDir(), "zcard"), st: Status{Phase: PhaseIdle}, probe: &updater.ProbeOutcome{Mode: "accel", Accel: srv.URL}, probedAt: time.Now()}
	done := make(chan error, 1)
	go func() { _, err := s.Check(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Check did not reach manifest")
	}
	for name, fn := range map[string]func(context.Context) error{
		"check": func(ctx context.Context) error { _, err := s.Check(ctx); return err },
		"apply": s.Apply, "rollback": s.Rollback,
	} {
		if err := fn(context.Background()); err != ErrBusy {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Let the blocked response fail, then verify the reservation is released.
	release <- struct{}{}
	if err := <-done; err == nil {
		t.Fatal("expected manifest error")
	}
	if s.st.Phase != PhaseFailed || s.busy || s.checking {
		t.Fatalf("unexpected final state: %+v", s.st)
	}
}

func TestPersistedHealthGateAndPreviousVersion(t *testing.T) {
	s := &Service{binPath: filepath.Join(t.TempDir(), "zcard"), st: Status{Phase: PhaseIdle}}
	for _, state := range []string{updater.StatePending, updater.StateOK} {
		raw, err := json.Marshal(updater.State{Status: state, FromVer: "v1.0.0", ToVer: cur()})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(s.binPath), "update.state"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if got := s.pendingTargetLocked(); got != (state == updater.StatePending) {
			t.Fatalf("%s blocked=%v", state, got)
		}
		st := s.Snapshot(context.Background())
		if state == updater.StatePending {
			if _, err := s.Check(context.Background()); err != ErrBusy {
				t.Fatalf("health gate: %v", err)
			}
			if st.Phase != PhaseVerifying {
				t.Fatalf("phase=%s", st.Phase)
			}
		} else if st.Phase != PhaseIdle {
			t.Fatalf("phase=%s", st.Phase)
		}
		if pb := toStatusPB(st); pb.PrevVersion != "v1.0.0" || pb.TargetVersion != cur() {
			t.Fatalf("lost version pair: %v", pb)
		}
	}
	s.st = Status{Phase: PhaseFailed, Target: "v9.9.9"}
	if s.pendingTargetLocked() {
		t.Fatal("failed update prevents rechecking")
	}
	s.busy = true
	s.st = Status{Phase: PhaseChecking}
	if st := s.Snapshot(context.Background()); st.Target != "" {
		t.Fatal("previous update target leaked into next update")
	}
}
