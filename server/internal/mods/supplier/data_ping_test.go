package supplier

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestSupplyPingAuthenticationAndBalance(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	seedCompatAccount(t, repo, "zcard", "funded", "secret", 12345)
	seedCompatAccount(t, repo, "zcard", "zero", "secret", 0)
	if _, err := repo.CreateAccount(t.Context(), "pending", "pending", "secret", "", "zcard", ""); err != nil {
		t.Fatal(err)
	}
	blocked := seedCompatAccount(t, repo, "zcard", "blocked", "secret", 999)
	repo.data.Client.SupplierAccount.UpdateOneID(blocked.ID).SetIPWhitelist([]string{"192.0.2.2"}).SaveX(t.Context())
	server := khttp.NewServer(khttp.Filter(SupplyAuthFilter(repo, 0)))
	supplyv1.RegisterSupplyServiceHTTPServer(server, svc)
	for i, tc := range []struct {
		name, key, secret, reason string
		status                    int
		balance                   int64
	}{
		{"anonymous", "", "", "", 200, -1},
		{"funded", "funded", "secret", "", 200, 12345},
		{"zero", "zero", "secret", "", 200, 0},
		{"wrong signature", "funded", "wrong", "invalid_signature", 401, 0},
		{"unknown account", "missing", "secret", "unknown_key", 401, 0},
		{"pending account", "pending", "secret", "account_disabled", 401, 0},
		{"IP whitelist", "blocked", "secret", "account_disabled", 401, 0},
		{"partial headers", "funded", "", "missing_headers", 401, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/supply/ping", nil)
			if tc.key != "" {
				req.Header.Set("X-Supply-Key", tc.key)
			}
			if tc.secret != "" {
				ts, nonce := strconv.FormatInt(time.Now().Unix(), 10), fmt.Sprint(i)
				req.Header.Set("X-Supply-Timestamp", ts)
				req.Header.Set("X-Supply-Nonce", nonce)
				req.Header.Set("X-Supply-Signature", supplySign(tc.secret, req.Method, req.URL.Path, "", ts, nonce, nil))
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status != 200 {
				if !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "balance") {
					t.Fatalf("bad auth response: %s", w.Body.String())
				}
				return
			}
			var reply supplyv1.PingReply
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || !reply.Ok || reply.Balance != tc.balance {
				t.Fatalf("wrong balance: %s err=%v", w.Body.String(), err)
			}
			if (tc.key != "") != (reply.Currency == "CNY") {
				t.Fatal("known balance currency missing or anonymous account disclosed")
			}
			if tc.key != "" {
				replay := httptest.NewRecorder()
				server.ServeHTTP(replay, req)
				if replay.Code != 401 || !strings.Contains(replay.Body.String(), "nonce_replay") {
					t.Fatal("signed ping replay was accepted")
				}
			}
		})
	}
}
