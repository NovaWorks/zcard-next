package plugin

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestMultipartImportAuthorizationAndLimits(t *testing.T) {
	s, _, ctx, _ := serviceFixture(t)
	anonymous := khttp.NewServer()
	s.RegisterImport(anonymous)
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/import", bytes.NewBufferString("not multipart"))
	rec := httptest.NewRecorder()
	anonymous.ServeHTTP(rec, bad)
	if rec.Code != 401 {
		t.Fatalf("authorization must precede body parse: %d %s", rec.Code, rec.Body)
	}
	middlewareCalled := false
	srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(request context.Context, in any) (any, error) {
			tr, ok := transport.FromServerContext(request)
			if !ok || tr.Operation() != ImportOperation {
				t.Fatal("custom route skipped operation metadata")
			}
			middlewareCalled = true
			return next(ctx, in)
		}
	}))
	s.RegisterImport(srv)
	_, signer := testPackages(t)
	f := signedPackageID(t, signer, "upload-fixture", "0.1.0", nil)
	cmd := &adminv1.OperatePluginRequest{OperationId: randomOperationID(), PluginId: f.artifact.Manifest.ID, Action: "import", TargetDigest: f.artifact.Descriptor.ArchiveSHA256, ExpectedGeneration: "0", ApprovedScopes: f.artifact.Manifest.Scopes}
	commandJSON, err := protojson.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	send := func(parts [][]byte) *httptest.ResponseRecorder {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		names := []string{"command", "descriptor", "signature", "archive"}
		for i, part := range parts {
			p, e := w.CreateFormFile(names[i], names[i])
			if e != nil {
				t.Fatal(e)
			}
			if _, e = p.Write(part); e != nil {
				t.Fatal(e)
			}
		}
		w.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/import", &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		out := httptest.NewRecorder()
		srv.ServeHTTP(out, req)
		return out
	}
	result := send([][]byte{commandJSON, f.descriptor, bytes.Repeat([]byte{1}, 65), f.archive})
	if result.Code != 413 {
		t.Fatalf("oversize signature accepted: %d %s", result.Code, result.Body)
	}
	result = send([][]byte{commandJSON, f.descriptor, f.signature, f.archive})
	if result.Code != 200 || !middlewareCalled {
		t.Fatalf("authenticated import: %d %s", result.Code, result.Body)
	}
	state, err := s.manager.Status(ctx, f.artifact.Manifest.ID)
	if err != nil || state.State.DesiredGeneration != 1 || state.State.DesiredEnabled {
		t.Fatalf("upload falsely activated: %+v %v", state, err)
	}
}
