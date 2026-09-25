package adapter

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestImportFailuresAreClassifiedAndRedacted(t *testing.T) {
	for _, tc := range []struct {
		err          error
		code         string
		retry, pause bool
	}{
		{context.DeadlineExceeded, "TIMEOUT", true, false},
		{&net.DNSError{Err: "secret", Name: "private.example", IsTimeout: true}, "NETWORK_ERROR", true, false},
		{&httpError{Status: 401, Message: "api_secret=secret"}, "AUTH_FAILED", false, true},
		{&httpError{Status: 403, Message: "secret"}, "ACCESS_DENIED", false, true},
		{&httpError{Status: 429, Message: "secret", RetryAfter: time.Minute}, "RATE_LIMITED", true, false},
		{&httpError{Status: 503, Message: "secret"}, "UPSTREAM_UNAVAILABLE", true, false},
		{errors.New("secret invalid SKU"), "INVALID_PRODUCT", false, false},
	} {
		f := ClassifyImportError(tc.err)
		if f.Code != tc.code || f.Retry != tc.retry || f.Pause != tc.pause {
			t.Fatalf("%s: %+v", tc.code, f)
		}
		if f.Summary == tc.err.Error() {
			t.Fatal("raw upstream error exposed")
		}
	}
	f := ClassifyImportError(&httpError{Status: 429, RetryAfter: time.Minute})
	if f.After != time.Minute || parseRetryAfter("60") != time.Minute {
		t.Fatal("Retry-After lost")
	}
	_, err := parseResp([]byte(`{"code":401,"msg":"secret"}`))
	if f := ClassifyImportError(err); f.Code != "AUTH_FAILED" {
		t.Fatal(f)
	}
}
