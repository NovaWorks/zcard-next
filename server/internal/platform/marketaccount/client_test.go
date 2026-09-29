package marketaccount

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicClientStrictNetworkAndQueries(t *testing.T) {
	for _, q := range []string{"limit=0", "limit=101", "limit=01", "cursor=", "limit=1&limit=2", "unknown=1"} {
		if _, e := ParseProductQuery(q); e == nil {
			t.Fatal(q)
		}
	}
	if _, e := ParseProductQuery("q=plugin&limit=20"); e != nil {
		t.Fatal(e)
	}
	var handler http.HandlerFunc
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r) }))
	defer srv.Close()
	client, e := NewClient(srv.URL, srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	handler = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("anonymous request had credentials")
		}
		json.NewEncoder(w).Encode(ProductPage{Items: []Product{}, NextCursor: ""})
	}
	var out ProductPage
	if e = client.Call(context.Background(), "products", "", ProductQuery{}, &out); e != nil {
		t.Fatal(e)
	}
	if e = client.Call(context.Background(), "products", "", ProductQuery{Limit: 101}, &out); e != ErrInvalid {
		t.Fatal(e)
	}
	handler = func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://127.0.0.1:1/private", 302) }
	if e = client.Call(context.Background(), "products", "", ProductQuery{}, &out); e != ErrUnavailable {
		t.Fatal(e)
	}
	handler = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", MaxResponseBytes+1))) }
	if e = client.Call(context.Background(), "products", "", ProductQuery{}, &out); e != ErrUnavailable {
		t.Fatal(e)
	}
	handler = func(w http.ResponseWriter, r *http.Request) { http.Error(w, "SECRET-REMOTE-STACK", 500) }
	if e = client.Call(context.Background(), "products", "", ProductQuery{}, &out); e == nil || strings.Contains(e.Error(), "SECRET") {
		t.Fatal(e)
	}
	denied, _ := NewClient(srv.URL, "")
	if denied != nil {
		defer denied.Close()
		if e = denied.Call(context.Background(), "products", "", ProductQuery{}, &out); e == nil {
			t.Fatal("loopback without exact seam")
		}
	}
}
