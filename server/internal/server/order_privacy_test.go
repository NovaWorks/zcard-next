package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrderAndPaymentResponsesDisableSharedCaching(t *testing.T) {
	handler := privateOrderResponseFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, path := range []string{
		"/api/v1/storefront/cart",
		"/api/v1/storefront/orders/ORDER-1",
		"/api/v1/storefront/orders/ORDER-1/access-recover",
		"/api/v1/storefront/payments",
		"/api/v1/storefront/payment/quote",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
			t.Fatalf("%s returned cache policy %q", path, got)
		}
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/storefront/products", nil))
	if got := public.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("public catalog cache policy changed: %s", got)
	}
	if got := public.Header().Get("Vary"); got != "Authorization" {
		t.Fatalf("catalog must vary by login: %q", got)
	}
	for _, path := range []string{"/api/v1/storefront/products", "/api/v1/storefront/products/1"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer member")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
			t.Fatalf("member price shared by cache: %s %q", path, got)
		}
	}
}
