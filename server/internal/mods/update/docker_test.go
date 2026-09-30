package update

import (
	"context"
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDockerAPIUsesAuthenticatedHelperAndPersistsStatusAcrossServiceRestart(t *testing.T) {
	t.Setenv("ZCARD_CONTAINER", "1")
	// Darwin Unix socket paths must fit sockaddr_un.
	dir, err := os.MkdirTemp("/tmp", "zcard-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	t.Setenv("ZCARD_UPDATER_SOCKET", socket)
	t.Setenv("ZCARD_UPDATER_TOKEN", "test-secret")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var received map[string]string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/apply" {
			json.NewDecoder(r.Body).Decode(&received)
		}
		json.NewEncoder(w).Encode(dockerStatus{Ready: true, Protocol: 1, Phase: "verifying", Busy: true, Target: "v1.2.96", Previous: "v1.2.95"})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	old := settings.ServerVersion()
	settings.SetServerVersion("v1.2.95")
	t.Cleanup(func() { settings.SetServerVersion(old) })
	cfg := &conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: "file:/app/data/zcard.db"}}
	service := NewService(cfg, nil)
	result, err := NewAdminUpdateService(service).ApplyUpdate(context.Background(), &adminv1.ApplyUpdateRequest{Version: "v1.2.96"})
	if err != nil {
		t.Fatal(err)
	}
	if received["version"] != "v1.2.96" || received["current"] != "v1.2.95" || received["dialect"] != "sqlite" {
		t.Fatalf("payload: %v", received)
	}
	if !result.DockerUpdateReady || !result.Busy {
		t.Fatalf("status: %v", result)
	}
	restarted := NewService(cfg, nil).Snapshot(context.Background())
	if restarted.Target != "v1.2.96" || restarted.Phase != "verifying" || !restarted.Busy {
		t.Fatalf("lost job: %+v", restarted)
	}
}

func TestDockerBackupScopeRejectsExternalDatabase(t *testing.T) {
	for _, db := range []*conf.Data_Database{
		{Driver: "sqlite", Source: "file:/outside/db.sqlite"},
		{Driver: "mysql", Source: "zcard:password@tcp(external:3306)/zcard"},
		{Driver: "postgres", Source: "postgres://external/db"},
	} {
		service := NewService(&conf.Data{Database: db}, nil)
		if _, err := service.dockerDialect(); err == nil {
			t.Fatalf("unsafe backup scope: %s", db.Driver)
		}
	}
}
