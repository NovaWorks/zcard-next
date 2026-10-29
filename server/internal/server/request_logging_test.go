package server

import (
	"bytes"
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"log/slog"
	"strings"
	"testing"
)

func TestRequestLoggingNeverFormatsCredentialsOrBuyerContent(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	secret := "fixture-secret-not-for-logs"
	req := &struct{ Credentials, Comments string }{secret, "private buyer comment"}
	want := errors.BadRequest("supply.INVALID", secret)
	called := false
	h := requestLogging(logger)(func(_ context.Context, got any) (any, error) { called = got == req; return nil, want })
	_, err := h(context.Background(), req)
	if !called || err != want {
		t.Fatal("logging changed request or error")
	}
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), req.Comments) || strings.Contains(output.String(), "Credentials") {
		t.Fatal("sensitive request was logged")
	}
	if !strings.Contains(output.String(), "supply.INVALID") {
		t.Fatal("request metadata missing")
	}
}
