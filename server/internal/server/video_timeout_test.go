package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVideoTimeoutIsScoped(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/media/video", "/api/v1/admin/media/upload"} {
		requestDeadlineFilter(30*time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Fatal("missing deadline")
			}
			remaining := time.Until(deadline)
			if path == "/api/v1/admin/media/video" {
				if remaining < 9*time.Minute {
					t.Fatal(remaining)
				}
			} else if remaining > 30*time.Second {
				t.Fatal(remaining)
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
	}
}
