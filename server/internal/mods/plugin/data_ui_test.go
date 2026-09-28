package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/middleware"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestInspectSignedPackageDoesNotPublishOrApprove(t *testing.T) {
	s, d, ctx, f := serviceFixture(t)
	cmd := &adminv1.OperatePluginRequest{PluginId: f.artifact.Manifest.ID, OperationId: randomOperationID(), Action: "upgrade", TargetDigest: f.artifact.Descriptor.ArchiveSHA256, ExpectedGeneration: "1"}
	input := &adminv1.ImportPluginRequest{Command: cmd, DescriptorJson: f.descriptor, Signature: f.signature, Archive: f.archive}
	out, err := s.InspectPlugin(ctx, input)
	if err != nil || out.Digest != cmd.TargetDigest || len(out.Scopes) == 0 {
		t.Fatalf("inspect: %+v %v", out, err)
	}
	state, err := s.manager.Status(ctx, cmd.PluginId)
	if err != nil || state.State.DesiredGeneration != 1 || state.State.DesiredEnabled {
		t.Fatalf("inspection changed state: %+v %v", state, err)
	}
	if n := d.Client.PluginOperation.Query().CountX(ctx); n != 1 {
		t.Fatalf("inspection created an operation: %d", n)
	}
	input.Signature = make([]byte, 64)
	if _, err = s.InspectPlugin(ctx, input); err == nil {
		t.Fatal("untrusted package previewed")
	}
	input.Signature = f.signature
	s.az.(testAuthorizer)["plugin:manage"] = false
	if _, err = s.InspectPlugin(ctx, input); err == nil {
		t.Fatal("read-only caller inspected package")
	}
	result, err := s.ListPlugins(ctx, &emptypb.Empty{})
	if err != nil || result.InstanceManagementAllowed {
		t.Fatal("read-only lifecycle capability exposed")
	}
}

type uiAuthorizer struct {
	testAuthorizer
	permissions []string
}

func (a *uiAuthorizer) PermissionsOf(context.Context, uint64) ([]string, error) {
	return a.permissions, nil
}

func TestContributionETagReauthorizesAndScopesToIdentity(t *testing.T) {
	s, d, ctx, _ := serviceFixture(t)
	p := d.Client.Product.Create().SetName("etag").SetSlug("etag").SetPrice(100).SaveX(ctx)
	az := &uiAuthorizer{testAuthorizer: s.az.(testAuthorizer), permissions: []string{"plugin:read", "catalog:write"}}
	s.az = az
	claims := authn.ClaimsFromContext(ctx)
	site := tenancy.Main()
	srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, in any) (any, error) {
			return next(tenancy.WithContext(authn.WithClaims(ctx, claims), site), in)
		}
	}))
	adminv1.RegisterAdminPluginServiceHTTPServer(srv, s)
	get := func(tag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/contributions?product_id="+strconv.FormatUint(p.ID, 10), nil)
		req.Header.Set("If-None-Match", tag)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	first := get("")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatalf("missing etag: %d %s", first.Code, first.Body)
	}
	tag := first.Header().Get("ETag")
	// v1 deliberately returns an authenticated 200, never an unchecked private 304.
	same := get(tag)
	if same.Code != 200 || same.Header().Get("ETag") != tag {
		t.Fatal("unstable authenticated response")
	}
	claims = &authn.Claims{Subject: 2, RoleID: 1, Realm: authn.RealmAdmin}
	if get(tag).Header().Get("ETag") == tag {
		t.Fatal("identity not included")
	}
	claims = authn.ClaimsFromContext(ctx)
	az.permissions = append(az.permissions, "plugin:configure")
	if get(tag).Header().Get("ETag") == tag {
		t.Fatal("permissions not included")
	}
	az.testAuthorizer["plugin:read"] = false
	denied := get(tag)
	if denied.Code != 403 || denied.Header().Get("ETag") != "" {
		t.Fatalf("stale etag bypassed authorization: %d", denied.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(denied.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["contributions"]; exists {
		t.Fatal("private contributions leaked")
	}
}
