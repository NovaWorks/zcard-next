package marketbinding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCredentialTransportBoundary(t *testing.T) {
	if c, e := New("http://public.example", ""); e == nil {
		c.Close()
		t.Fatal("HTTP allowed")
	}
	var targetCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer destination.Close()
	secret, _ := Random()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("missing authorization")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c, e := New(origin.URL, origin.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var out Sync
	if e = c.Call(context.Background(), "sync", secret, struct{}{}, &out); e == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("credential redirected")
	}
	if e = c.Call(context.Background(), "../operator", secret, struct{}{}, &out); e == nil {
		t.Fatal("arbitrary path")
	}
}
