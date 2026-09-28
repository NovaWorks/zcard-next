package plugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/middleware"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMarketInstallBoundary(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	p, key := testPackages(t)
	s.manager.packages = p
	f := signedPackageID(t, key, "online-test", "0.2.0", nil)
	var origin string
	mode := "ok"
	revision := pc.Decimal("1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mc.Prefix + "/capabilities":
			json.NewEncoder(w).Encode(mc.Capabilities{APIVersion: "1", FreeDistribution: true, MaxEntries: 128})
		case mc.Prefix + "/plugins":
			entries := []mc.Entry{{Name: "test", Descriptor: f.artifact.Descriptor, Signature: f.signature, Manifest: f.artifact.Manifest, ArtifactPath: mc.Prefix + "/artifacts/" + f.artifact.Descriptor.ArchiveSHA256}}
			if mode == "withdrawn" {
				entries = []mc.Entry{}
			}
			e, _ := mc.Sign(mc.Catalog{APIVersion: "1", Origin: origin, Revision: revision, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), Entries: entries}, "test-artifact", key)
			if mode == "bad-signature" {
				e.Signature = make([]byte, ed25519.SignatureSize)
			}
			json.NewEncoder(w).Encode(e)
		default:
			if mode == "corrupt" {
				w.Write([]byte("corrupt"))
			} else {
				w.Write(f.archive)
			}
		}
	}))
	defer server.Close()
	origin = server.URL
	t.Setenv("ZCARD_MARKET_TEST_ORIGIN", origin)
	if e := s.manager.repo.configureMarket(ctx, origin); e != nil {
		t.Fatal(e)
	}
	cmd := &adminv1.OperatePluginRequest{PluginId: "online-test", OperationId: randomOperationID(), Action: "import", TargetDigest: f.artifact.Descriptor.ArchiveSHA256, ExpectedGeneration: "0", ApprovedScopes: f.artifact.Manifest.Scopes}
	commandJSON, _ := protojson.Marshal(cmd)
	body, _ := json.Marshal(marketSelection{Origin: origin, Version: "0.2.0", Command: commandJSON})
	for _, m := range []string{"corrupt", "bad-signature"} {
		mode = m
		if _, e := s.marketInstall(ctx, bytes.NewReader(body), origin, false); e == nil {
			t.Fatal(m)
		}
		state, _ := s.manager.Status(ctx, "online-test")
		if state.State.DesiredGeneration != 0 {
			t.Fatal("failed download changed state")
		}
	}
	mode = "ok"
	if _, e := s.marketInstall(ctx, bytes.NewReader(body), origin, true); e != nil {
		t.Fatal(e)
	}
	state, _ := s.manager.Status(ctx, "online-test")
	if state.State.DesiredGeneration != 0 {
		t.Fatal("preview installed")
	}
	if _, e := s.marketInstall(ctx, bytes.NewReader(body), origin, false); e != nil {
		t.Fatal(e)
	}
	// A validly signed older release is not an online upgrade.
	previous := f
	f = signedPackageID(t, key, "online-test", "0.1.0", nil)
	revision = "2"
	lower := adminv1.OperatePluginRequest{PluginId: cmd.PluginId, ApprovedScopes: append([]string(nil), cmd.ApprovedScopes...)}
	lower.Action = "upgrade"
	lower.OperationId = randomOperationID()
	lower.ExpectedGeneration = "1"
	lower.TargetDigest = f.artifact.Descriptor.ArchiveSHA256
	lowerJSON, _ := protojson.Marshal(&lower)
	lowerBody, _ := json.Marshal(marketSelection{Origin: origin, Version: "0.1.0", Command: lowerJSON})
	if _, e := s.marketInstall(ctx, bytes.NewReader(lowerBody), origin, false); e == nil {
		t.Fatal("online downgrade accepted")
	}
	f = signedPackageID(t, key, "online-test", "0.3.0", nil)
	revision = "3"
	mode = "corrupt"
	lower.OperationId = randomOperationID()
	lower.TargetDigest = f.artifact.Descriptor.ArchiveSHA256
	lowerJSON, _ = protojson.Marshal(&lower)
	lowerBody, _ = json.Marshal(marketSelection{Origin: origin, Version: "0.3.0", Command: lowerJSON})
	if _, e := s.marketInstall(ctx, bytes.NewReader(lowerBody), origin, false); e == nil {
		t.Fatal("corrupt upgrade accepted")
	}
	state, _ = s.manager.Status(ctx, "online-test")
	if state.DesiredDigest != previous.artifact.Descriptor.ArchiveSHA256 || state.State.DesiredGeneration != 1 {
		t.Fatal("failed upgrade changed current version")
	}
	mode = "withdrawn"
	revision = "4"
	if _, e := s.catalog(ctx, origin); e != nil {
		t.Fatal(e)
	}
	server.Close()
	if _, e := s.marketInstall(ctx, bytes.NewReader(body), origin, false); e != nil {
		t.Fatal("offline retry failed", e)
	}
	cmd.ApprovedScopes = nil
	commandJSON, _ = protojson.Marshal(cmd)
	bad, _ := json.Marshal(marketSelection{Origin: origin, Version: "0.2.0", Command: commandJSON})
	if _, e := s.marketInstall(ctx, bytes.NewReader(bad), origin, false); e == nil {
		t.Fatal("operation ID mutated")
	}
	// All custom routes run authorization before reading attacker-controlled bodies.
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		status int
	}{{"anonymous", context.Background(), 401}, {"other-site", tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 42}), 403}, {"operator", authn.WithClaims(ctx, &authn.Claims{Subject: 2, RoleID: 2, Realm: authn.RealmAdmin}), 403}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
				return func(_ context.Context, in any) (any, error) { return next(tc.ctx, in) }
			}))
			s.RegisterMarket(srv)
			for _, route := range []struct{ method, path string }{{"GET", "config"}, {"PUT", "config"}, {"GET", "catalog"}, {"POST", "inspect"}, {"POST", "install"}} {
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, httptest.NewRequest(route.method, marketPrefix+"/"+route.path, bytes.NewBufferString("bad json")))
				if w.Code != tc.status {
					t.Fatalf("%s %s = %d %s", route.method, route.path, w.Code, w.Body)
				}
			}
		})
	}
}
