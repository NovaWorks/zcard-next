package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestContainerRejectsMutationsBeforeSideEffects(t *testing.T) {
	t.Setenv("ZCARD_CONTAINER", "1")
	ctx := context.Background()
	dir := t.TempDir()
	bin := filepath.Join(dir, "zcard")
	if err := os.WriteFile(bin, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Service{binPath: bin, st: Status{Phase: PhaseIdle}}
	for name, fn := range map[string]func(context.Context) error{"apply": s.Apply, "rollback": s.Rollback} {
		if err := fn(ctx); !errors.Is(err, updater.ErrContainerUpdate) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	api := NewAdminUpdateService(s)
	if _, err := api.ApplyUpdate(ctx, nil); kerrors.Code(err) != 403 || kerrors.Reason(err) != "update.CONTAINER" {
		t.Fatalf("apply API: %v", err)
	}
	if _, err := api.RollbackUpdate(ctx, nil); kerrors.Code(err) != 403 || kerrors.Reason(err) != "update.CONTAINER" {
		t.Fatalf("rollback API: %v", err)
	}
	if s.busy || s.st.Phase != PhaseIdle {
		t.Fatalf("state changed: %+v", s.st)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || string(raw) != "original" {
		t.Fatal("mutation touched binary directory")
	}
	if s.supervisorKind(ctx) != "docker" {
		t.Fatal("wrong deployment kind")
	}
	if s.DisabledErr() != nil {
		t.Fatal("read-only update checks must remain available")
	}
}

func TestContainerCheckUsesSignedManifestWithoutFalseUpdates(t *testing.T) {
	t.Setenv("ZCARD_CONTAINER", "1")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldKey, oldVersion := updater.DefaultPublicKeyHex, settings.ServerVersion()
	t.Cleanup(func() { updater.DefaultPublicKeyHex = oldKey; settings.SetServerVersion(oldVersion) })
	updater.DefaultPublicKeyHex = hex.EncodeToString(pub)
	raw, err := updater.SignManifest(priv, "v1.2.58", "stable", "test", []updater.FileEntry{{Name: "zcard-linux-amd64", Size: 1, SHA256: "test"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer srv.Close()
	for _, tc := range []struct {
		version string
		want    bool
	}{{"v1.2.58", false}, {"v1.2.57", true}, {"dev", false}} {
		settings.SetServerVersion(tc.version)
		s := &Service{st: Status{Phase: PhaseIdle}, probe: &updater.ProbeOutcome{Mode: "accel", Accel: srv.URL}, probedAt: time.Now()}
		result, err := NewAdminUpdateService(s).CheckUpdate(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.HasUpdate != tc.want || result.CurrentVersion != tc.version || result.LatestVersion != "v1.2.58" {
			t.Fatalf("unexpected result: %+v", result)
		}
		if s.st.HasUpdate != tc.want || s.st.Phase != PhaseIdle {
			t.Fatalf("unexpected status: %+v", s.st)
		}
	}
}
