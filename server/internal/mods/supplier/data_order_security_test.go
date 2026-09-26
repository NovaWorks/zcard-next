package supplier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func securityStock(t *testing.T, svc *SupplyAPIService, count int) {
	t.Helper()
	ctx := context.Background()
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := inventory.NewCardRepoImpl(svc.repo.data, cipher)
	svc.inv, svc.cards = repo, repo
	if _, err := svc.repo.data.Client.Product.Create().SetID(1).SetName("security fixture").SetSlug("security-fixture").SetPrice(1000).Save(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		plain := fmt.Sprintf("SECRET-%d", i)
		encrypted, err := cipher.Seal(plain, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.repo.data.Client.Card.Create().SetProductID(1).SetContent(encrypted).SetContentHash(cipher.ContentHash(plain)).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func paidSecurityOrder(t *testing.T, repo *SupplierRepoImpl, account uint64, no string) *ent.SupplyOrder {
	t.Helper()
	ctx := context.Background()
	o, err := repo.CreateSupplyOrder(ctx, account, no, []map[string]any{{"product_id": 1, "quantity": 1}}, 600)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.LedgerEntry(ctx, account, o.ID, "supply_pay", -600, "supply_order:"+no, "legacy debit"); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkSupplyOrderPaid(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	return o
}

// Real HTTP authentication and encrypted inventory: another account cannot read,
// cancel, refund, or reuse the owner's delivery through an idempotency collision.
func TestSupplyOrderHTTPAccountIsolation(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	securityStock(t, svc, 4)
	a := seedCompatAccount(t, repo, "zcard", "scope-a", "secret-a", 10000)
	b := seedCompatAccount(t, repo, "zcard", "scope-b", "secret-b", 10000)
	ctx := context.Background()
	original, err := svc.fulfillOrder(ctx, a.ID, 1, 1, "same-no", "", "")
	if err != nil {
		t.Fatal(err)
	}
	paid := paidSecurityOrder(t, repo, a.ID, "a-paid")
	server := khttp.NewServer(khttp.Filter(SupplyAuthFilter(repo, 0)))
	supplyv1.RegisterSupplyServiceHTTPServer(server, svc)
	nonce := 0
	call := func(method, path, body string, signed bool) (int, string) {
		t.Helper()
		nonce++
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if signed {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			n := fmt.Sprint(nonce)
			r.Header.Set("X-Supply-Key", b.APIKey)
			r.Header.Set("X-Supply-Timestamp", ts)
			r.Header.Set("X-Supply-Nonce", n)
			r.Header.Set("X-Supply-Signature", supplySign("secret-b", method, r.URL.Path, r.URL.RawQuery, ts, n, []byte(body)))
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	path := fmt.Sprintf("/api/supply/orders/%d", original.order.ID)
	if code, _ := call("GET", path, "", false); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	code, body := call("GET", fmt.Sprintf("/api/supply/orders?start=%d&end=%d", start.Unix(), end.Unix()), "", true)
	if code != 200 || strings.Contains(body, "same-no") || strings.Contains(body, "a-paid") {
		t.Fatalf("list leaks: %d %s", code, body)
	}
	for _, tc := range []struct{ method, path string }{{"GET", path}, {"GET", "/api/supply/orders/999999"}, {"POST", fmt.Sprintf("/api/supply/orders/%d/cancel", paid.ID)}, {"POST", path + "/refund"}} {
		code, body = call(tc.method, tc.path, "", true)
		if code != 404 || strings.Contains(body, "SECRET-") {
			t.Fatalf("foreign access: %d %s", code, body)
		}
	}
	balanceA, _ := repo.BalanceOf(ctx, a.ID)
	if balanceA != 8400 {
		t.Fatalf("foreign mutation: %d", balanceA)
	}
	code, body = call("POST", "/api/supply/orders", `{"product_id":"1","quantity":1,"downstream_order_no":"same-no"}`, true)
	if code != 200 || strings.Contains(body, original.cards[0]) {
		t.Fatalf("cross-account create: %d %s", code, body)
	}
	own, err := repo.GetSupplyOrderByNo(ctx, b.ID, "same-no")
	if err != nil || own.ID == original.order.ID {
		t.Fatalf("distinct account order: %v", err)
	}
	balanceB, _ := repo.BalanceOf(ctx, b.ID)
	if balanceB != 9000 {
		t.Fatalf("B must pay: %d", balanceB)
	}
	code, body = call("POST", "/api/supply/orders", `{"product_id":"1","quantity":1,"downstream_order_no":"same-no"}`, true)
	after, _ := repo.BalanceOf(ctx, b.ID)
	if code != 200 || after != balanceB {
		t.Fatalf("retry charged twice: %d %s", code, body)
	}
	code, _ = call("POST", "/api/supply/orders", `{"product_id":"1","quantity":2,"downstream_order_no":"same-no"}`, true)
	if code != 409 {
		t.Fatalf("changed retry: %d", code)
	}
	all, err := repo.ListSupplyOrders(ctx, start, end)
	if err != nil || len(all) != 3 {
		t.Fatalf("internal/admin full scope: %d %v", len(all), err)
	}
	if _, err := svc.GetOrder(ctx, &supplyv1.GetSupplyOrderRequest{Id: fmt.Sprint(original.order.ID)}); kerrors.Code(err) != 401 {
		t.Fatalf("missing context must fail closed: %v", err)
	}
	if _, err := repo.GetAccountSupplyOrder(ctx, 0, original.order.ID); err == nil {
		t.Fatal("zero account must not mean all accounts")
	}
}

func TestSupplyRefundBoundedByPayment(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	a := seedCompatAccount(t, repo, "zcard", "refund", "secret", 10000)
	ctx := context.WithValue(context.Background(), accountCtxKey{}, a.ID)
	o := paidSecurityOrder(t, repo, a.ID, "paid")
	cancel, err := svc.CancelOrder(ctx, &supplyv1.CancelSupplyOrderRequest{Id: fmt.Sprint(o.ID)})
	if err != nil || !cancel.Ok {
		t.Fatalf("cancel: %v %v", cancel, err)
	}
	refund, err := svc.RefundOrder(ctx, &supplyv1.RefundSupplyOrderRequest{Id: fmt.Sprint(o.ID)})
	if err != nil || !refund.Ok {
		t.Fatalf("repeat refund: %v %v", refund, err)
	}
	balance, _ := repo.BalanceOf(ctx, a.ID)
	if balance != 10000 {
		t.Fatalf("double refund: %d", balance)
	}
	unpaid, err := repo.CreateSupplyOrder(ctx, a.ID, "unpaid", []map[string]any{}, 600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.RefundUndelivered(ctx, a.ID, unpaid.ID); err != nil || !ok {
		t.Fatalf("unpaid cancel: %v", err)
	}
	balance, _ = repo.BalanceOf(ctx, a.ID)
	if balance != 10000 {
		t.Fatalf("unpaid credit: %d", balance)
	}
	legacy := paidSecurityOrder(t, repo, a.ID, "legacy")
	if err := repo.LedgerEntry(ctx, a.ID, legacy.ID, "supply_refund", 600, "supply_order:legacy:cancel", "old cancel"); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSupplyOrderRejected(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.RefundUndelivered(ctx, a.ID, legacy.ID); err != nil || !ok {
		t.Fatalf("legacy refund: %v", err)
	}
	balance, _ = repo.BalanceOf(ctx, a.ID)
	if balance != 10000 {
		t.Fatalf("legacy double refund: %d", balance)
	}
	legacyDelivered := paidSecurityOrder(t, repo, a.ID, "legacy-delivered")
	if err := repo.UpdateSupplyOrderItems(ctx, legacyDelivered.ID, []map[string]any{{"product_id": 1, "quantity": 1, "card_ids": []uint64{42}}}); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.RefundUndelivered(ctx, a.ID, legacyDelivered.ID); err != nil || ok {
		t.Fatalf("legacy delivery snapshot must block refund: %v", err)
	}
	delivered, err := svc.fulfillOrder(ctx, a.ID, 1, 1, "delivered", "", "")
	if err != nil {
		t.Fatal(err)
	}
	refund, err = svc.RefundOrder(ctx, &supplyv1.RefundSupplyOrderRequest{Id: fmt.Sprint(delivered.order.ID)})
	if err != nil || refund.Ok || refund.ErrorCode != "refund_not_allowed" {
		t.Fatalf("delivered refund: %v %v", refund, err)
	}
	balance, _ = repo.BalanceOf(ctx, a.ID)
	if balance != 8400 {
		t.Fatalf("delivered credit: %d", balance)
	}
}

type brokenContent struct{}

func (brokenContent) Contents(context.Context, []uint64, uint64, uint64) ([]string, error) {
	return nil, errors.New("injected decryption failure")
}

func TestSupplyDeliveryRollback(t *testing.T) {
	for _, stage := range []string{"stock", "decrypt", "snapshot", "ledger", "refund-status"} {
		t.Run(stage, func(t *testing.T) {
			svc, repo, _ := newCompatEnv(t)
			securityStock(t, svc, 1)
			a := seedCompatAccount(t, repo, "zcard", "rollback", "secret", 10000)
			ctx := context.Background()
			quantity := int32(1)
			switch stage {
			case "stock":
				quantity = 2
			case "decrypt":
				svc.cards = brokenContent{}
			case "snapshot":
				repo.data.Client.SupplyOrder.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpUpdateOne) {
							if _, exists := m.Field("items"); exists {
								return nil, errors.New("snapshot failure")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
			case "ledger":
				repo.data.Client.SupplierAccount.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpUpdateOne) {
							return nil, errors.New("balance cache failure")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "refund-status":
				o := paidSecurityOrder(t, repo, a.ID, "refund-rollback")
				repo.data.Client.SupplyOrder.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return nil, errors.New("status failure") })
				})
				if ok, err := repo.RefundUndelivered(ctx, a.ID, o.ID); err == nil || ok {
					t.Fatal("status failure must surface")
				}
				balance, _ := repo.BalanceOf(ctx, a.ID)
				if balance != 9400 {
					t.Fatalf("refund not rolled back: %d", balance)
				}
				return
			}
			out, err := svc.fulfillOrder(ctx, a.ID, 1, quantity, "rollback-order", "", "")
			if stage == "stock" {
				if err != nil || !out.rejected {
					t.Fatalf("stock result: %v %v", out, err)
				}
			} else if err == nil {
				t.Fatal("failure must surface")
			}
			balance, _ := repo.BalanceOf(ctx, a.ID)
			count, e := repo.data.Client.SupplyOrder.Query().Count(ctx)
			if e != nil || balance != 10000 || count != 0 {
				t.Fatalf("order/payment rollback: balance=%d rows=%d err=%v", balance, count, e)
			}
			_, ledger, e := repo.ListLedger(ctx, a.ID, 1, 100)
			if e != nil || ledger != 1 {
				t.Fatalf("ledger rollback: %d %v", ledger, e)
			}
			cards, e := repo.data.Client.Card.Query().Where(card.StatusEQ(card.StatusAvailable)).Count(ctx)
			if e != nil || cards != 1 {
				t.Fatalf("card rollback: %d %v", cards, e)
			}
		})
	}
}

func TestSupplyInvalidAmountAndCallbackOwnership(t *testing.T) {
	svc, repo, cat := newCompatEnv(t)
	a := seedCompatAccount(t, repo, "zcard", "invalid", "secret", 10000)
	ctx := context.Background()
	for _, tc := range []struct {
		price int64
		qty   int32
	}{{-1, 1}, {0, 1}, {math.MaxInt64, 2}, {1000, -1}, {1000, 0}} {
		cat.prods[0].Price = tc.price
		if _, err := svc.fulfillOrder(ctx, a.ID, 1, tc.qty, "invalid", "", ""); err == nil {
			t.Fatalf("invalid amount accepted: %v", tc)
		}
	}
	balance, _ := repo.BalanceOf(ctx, a.ID)
	if balance != 10000 {
		t.Fatalf("invalid amount changed balance: %d", balance)
	}
	o := paidSecurityOrder(t, repo, a.ID, "callback")
	if _, err := repo.CreateCallback(ctx, o.ID, a.ID+1, "callback", "https://example.com/callback", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverCallback(ctx, o.ID); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatalf("mismatched callback must stop before network: %v", err)
	}
}

// Invoked both locally with SQLite and against dedicated MySQL/PostgreSQL databases.
func exerciseConcurrentSupply(t *testing.T, svc *SupplyAPIService) {
	t.Helper()
	repo := svc.repo
	ctx := context.Background()
	securityStock(t, svc, 10)
	a := seedCompatAccount(t, repo, "zcard", "concurrent", "secret", 10000)
	run := func(fn func(int) error) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		start := make(chan struct{})
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; errs <- fn(i) }()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run(func(i int) error { _, err := svc.fulfillOrder(ctx, a.ID, 1, 1, "one-order", "", ""); return err })
	balance, _ := repo.BalanceOf(ctx, a.ID)
	count, err := repo.data.Client.SupplyOrder.Query().Where(supplyorder.AccountID(a.ID)).Count(ctx)
	if err != nil || count != 1 || balance != 9000 {
		t.Fatalf("concurrent duplicate: rows=%d balance=%d %v", count, balance, err)
	}
	run(func(i int) error { return repo.Recharge(ctx, a.ID, 100, fmt.Sprintf("parallel-credit:%d", i), "test") })
	balance, _ = repo.BalanceOf(ctx, a.ID)
	if balance != 10200 {
		t.Fatalf("lost credits: %d", balance)
	}
	o := paidSecurityOrder(t, repo, a.ID, "concurrent-cancel")
	run(func(i int) error {
		ok, err := repo.RefundUndelivered(ctx, a.ID, o.ID)
		if err == nil && !ok {
			return errors.New("refund rejected")
		}
		return err
	})
	balance, _ = repo.BalanceOf(ctx, a.ID)
	if balance != 10200 {
		t.Fatalf("concurrent double refund: %d", balance)
	}
}

func TestSupplyConcurrentSQLite(t *testing.T) {
	svc, _, _ := newCompatEnv(t)
	svc.repo.data.DB.SetMaxOpenConns(1)
	exerciseConcurrentSupply(t, svc)
}

func TestCompatOrderAccountIsolation(t *testing.T) {
	for _, protocol := range []string{"dujiao_next", "acg_faka"} {
		t.Run(protocol, func(t *testing.T) {
			svc, repo, _ := newCompatEnv(t)
			securityStock(t, svc, 4)
			a := seedCompatAccount(t, repo, protocol, "compat-a", "secret-a", 10000)
			b := seedCompatAccount(t, repo, protocol, "compat-b", "secret-b", 10000)
			server := khttp.NewServer()
			RegisterDujiaoCompat(server, svc)
			RegisterAcgFakaCompat(server, svc)
			call := func(acc *ent.SupplierAccount, secret, method, path, body string, form url.Values) (int, map[string]any) {
				t.Helper()
				if protocol == "acg_faka" {
					form.Set("app_id", acc.APIKey)
					form.Set("app_key", secret)
					form.Set("sign", acgTestSign(form, secret))
					body = form.Encode()
				}
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if protocol == "dujiao_next" {
					ts := fmt.Sprint(time.Now().Unix())
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Dujiao-Next-Api-Key", acc.APIKey)
					req.Header.Set("Dujiao-Next-Timestamp", ts)
					req.Header.Set("Dujiao-Next-Signature", dujiaoSign(secret, method, path, ts, []byte(body)))
				} else {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				w := httptest.NewRecorder()
				server.ServeHTTP(w, req)
				var result map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatalf("response: %s", w.Body.String())
				}
				return w.Code, result
			}
			path, body := "/api/v1/upstream/orders", `{"sku_id":1,"quantity":1,"downstream_order_no":"shared"}`
			if protocol == "acg_faka" {
				path = "/shared/commodity/trade"
			}
			ids := []string{}
			secrets := []string{}
			for i, acc := range []*ent.SupplierAccount{a, b} {
				secret := []string{"secret-a", "secret-b"}[i]
				status, m := call(acc, secret, "POST", path, body, url.Values{"shared_code": {"1"}, "num": {"1"}, "request_no": {"shared"}})
				if status != 200 {
					t.Fatalf("create: %d %v", status, m)
				}
				if protocol == "dujiao_next" {
					if m["ok"] != true {
						t.Fatalf("create: %v", m)
					}
					ids = append(ids, fmt.Sprint(uint64(m["order_id"].(float64))))
				} else {
					if m["code"] != float64(200) {
						t.Fatalf("create: %v", m)
					}
					d := m["data"].(map[string]any)
					ids = append(ids, d["tradeNo"].(string))
					secrets = append(secrets, d["secret"].(string))
				}
				balance, _ := repo.BalanceOf(context.Background(), acc.ID)
				if balance != 9000 {
					t.Fatalf("account not charged: %d", balance)
				}
			}
			if ids[0] == ids[1] || len(secrets) == 2 && secrets[0] == secrets[1] {
				t.Fatal("cross-account idempotency shared order/cards")
			}
			if protocol == "dujiao_next" {
				paid := paidSecurityOrder(t, repo, b.ID, "compat-paid")
				cancelPath := fmt.Sprintf("/api/v1/upstream/orders/%d/cancel", paid.ID)
				status, result := call(b, "secret-b", "POST", cancelPath, "", nil)
				if status != 200 || result["ok"] != true {
					t.Fatalf("own cancel: %d %v", status, result)
				}
				status, result = call(b, "secret-b", "GET", fmt.Sprintf("/api/v1/upstream/orders/%d", paid.ID), "", nil)
				if status != 200 || result["status"] != "refunded" || result["refunded_amount"] != "6.00" {
					t.Fatalf("refund detail: %d %v", status, result)
				}
				for _, suffix := range []string{"", "/cancel"} {
					method := "GET"
					if suffix != "" {
						method = "POST"
					}
					status, m := call(b, "secret-b", method, "/api/v1/upstream/orders/"+ids[0]+suffix, "", nil)
					if status != 404 {
						t.Fatalf("foreign: %d %v", status, m)
					}
				}
			} else {
				_, m := call(b, "secret-b", "POST", "/shared/commodity/query", "", url.Values{"tradeNo": {ids[0]}})
				if m["code"] == float64(200) {
					t.Fatalf("foreign: %v", m)
				}
				_, m = call(b, "secret-b", "POST", path, "", url.Values{"shared_code": {"1"}, "num": {"4294967297"}, "request_no": {"overflow"}})
				if m["code"] == float64(200) {
					t.Fatalf("quantity truncation: %v", m)
				}
			}
		})
	}
}

func TestSupplyConcurrentSQLiteWAL(t *testing.T) {
	svc, _, _ := newCompatEnv(t)
	d, cleanup, err := data.NewData(&conf.Data{Database: &conf.Data_Database{Driver: "sqlite", Source: "file:" + filepath.Join(t.TempDir(), "concurrent.db"), MaxOpenConns: 12, MaxIdleConns: 12}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := d.Client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.repo = NewSupplierRepoImpl(d, svc.repo.box)
	exerciseConcurrentSupply(t, svc)
}

type callbackCommitProbe struct {
	t     *testing.T
	repo  *SupplierRepoImpl
	calls int
}

func (*callbackCommitProbe) Enabled() bool { return true }
func (p *callbackCommitProbe) Enqueue(ctx context.Context, task queue.Task) error {
	p.t.Helper()
	p.calls++
	if data.Client(ctx, p.repo.data) != p.repo.data.Client {
		p.t.Fatal("callback received transaction context")
	}
	var payload struct {
		ID uint64 `json:"supply_order_id"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		p.t.Fatal(err)
	}
	o, err := p.repo.GetSupplyOrder(ctx, payload.ID)
	if err != nil || o.Status != supplyorder.StatusFulfilled {
		p.t.Fatalf("callback before order commit: %v", err)
	}
	cb, err := p.repo.GetCallbackByOrder(ctx, o.ID)
	if err != nil || cb.AccountID != o.AccountID {
		p.t.Fatalf("callback before registration commit: %v", err)
	}
	return nil
}
func TestSupplyCallbackAfterCommit(t *testing.T) {
	svc, repo, _ := newCompatEnv(t)
	a := seedCompatAccount(t, repo, "zcard", "callback-commit", "secret", 10000)
	probe := &callbackCommitProbe{t: t, repo: repo}
	svc.enq = probe
	if _, err := svc.fulfillOrder(context.Background(), a.ID, 1, 1, "callback-commit", "https://example.com/callback", ""); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 {
		t.Fatalf("callback count: %d", probe.calls)
	}
	if _, err := svc.fulfillOrder(context.Background(), a.ID, 1, 1, "callback-commit", "https://example.com/changed", ""); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 {
		t.Fatal("retry must not replace or enqueue callback")
	}
}
