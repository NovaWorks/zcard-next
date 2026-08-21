package supplier

// P2-03 必测项：签名 golden vectors（双口径）、时间窗、nonce 重放、账本幂等、
// 下单幂等、余额不足拒绝、回调记录生命周期。

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newSupplierTestData(t *testing.T) (*SupplierRepoImpl, *data.Data) {
	t.Helper()
	handle, err := db.SQLite.Open("file:suppliertest?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	box, _ := crypto.NewBox(make([]byte, 32))
	return NewSupplierRepoImpl(d, box), d
}

// seedAccount 建账户（approved + 余额 10000 分）。
func seedAccount(t *testing.T, r *SupplierRepoImpl) uint64 {
	t.Helper()
	ctx := context.Background()
	acc, err := r.CreateAccount(ctx, "下游A", "key-001", "supplier-secret-abc123", "contact@example.com", "zcard", "")
	if err != nil {
		t.Fatal(err)
	}
	acc, err = r.ReviewAccount(ctx, acc.ID, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Recharge(ctx, acc.ID, 10000, "recharge:test:1", "测试充值"); err != nil {
		t.Fatal(err)
	}
	return acc.ID
}

// TestGoldenSignatures 签名向量（Python 独立计算固化）。
func TestGoldenSignatures(t *testing.T) {
	const secret = "supplier-secret-abc123"
	body := []byte(`{"product_id":"1","quantity":2}`)

	t.Run("旧口径", func(t *testing.T) {
		got := hmacSha256Hex(secret, signStringOld("POST", "/api/supply/orders", "1700000000", "n1", body))
		want := "d1ef1f4fb549cc3c89473745c360d28aff3a9d26fb0033705846792f010b9bc4"
		if got != want {
			t.Fatalf("旧口径向量漂移: got %s", got)
		}
	})
	t.Run("新口径", func(t *testing.T) {
		got := supplySign(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", body)
		want := "4b183c7205b11badc37a88f066d26d013cc8dc2c886fff0c7d04c5c113b5ffbe"
		if got != want {
			t.Fatalf("新口径向量漂移: got %s", got)
		}
	})
	t.Run("双口径兼容验签", func(t *testing.T) {
		old := hmacSha256Hex(secret, signStringOld("POST", "/api/supply/orders", "1700000000", "n1", body))
		new := supplySign(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", body)
		if !verifyDual(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", body, old) {
			t.Fatal("旧口径签名应通过双口径验签")
		}
		if !verifyDual(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", body, new) {
			t.Fatal("新口径签名应通过双口径验签")
		}
		if verifyDual(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", body, "deadbeef") {
			t.Fatal("错误签名必须拒绝")
		}
		// body 篡改拒绝（哈希字节 === 发出字节 不变式）
		if verifyDual(secret, "POST", "/api/supply/orders", "page=1&page_size=50", "1700000000", "n1", []byte(`{"product_id":"2"}`), new) {
			t.Fatal("body 篡改必须拒绝")
		}
	})
}

// TestNonceReplay 同 nonce 二次消费拒绝（UNIQUE 约束）。
func TestNonceReplay(t *testing.T) {
	r, _ := newSupplierTestData(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	if err := r.ConsumeNonce(ctx, "key-001", "nonce-1", exp); err != nil {
		t.Fatal(err)
	}
	if err := r.ConsumeNonce(ctx, "key-001", "nonce-1", exp); err == nil {
		t.Fatal("nonce 重放必须拒绝")
	}
	// 不同 key 同 nonce 允许（命名空间隔离）
	if err := r.ConsumeNonce(ctx, "key-002", "nonce-1", exp); err != nil {
		t.Fatalf("跨 key 同 nonce 应允许: %v", err)
	}
}

// TestLedgerIdempotentAndBalance 账本：幂等键重放、余额不足拒绝且不产生流水。
func TestLedgerIdempotentAndBalance(t *testing.T) {
	r, d := newSupplierTestData(t)
	ctx := context.Background()
	accID := seedAccount(t, r)

	// 扣款 3000（余额 10000 → 7000）
	if err := r.LedgerEntry(ctx, accID, 1, "supply_pay", -3000, "supply_order:DOWN-1", "扣款"); err != nil {
		t.Fatal(err)
	}
	// 幂等键重放：同 reference 再次入账 → ErrDuplicateLedger，余额不变
	err := r.LedgerEntry(ctx, accID, 1, "supply_pay", -3000, "supply_order:DOWN-1", "扣款重放")
	if !errors.Is(err, ErrDuplicateLedger) {
		t.Fatalf("幂等键重放应拒绝: %v", err)
	}
	bal, _ := r.BalanceOf(ctx, accID)
	if bal != 7000 {
		t.Fatalf("重放后余额不应变化: %d", bal)
	}
	// 余额不足：8000 > 7000 → 拒绝且不产生流水
	err = r.LedgerEntry(ctx, accID, 2, "supply_pay", -8000, "supply_order:DOWN-2", "超扣")
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("余额不足应拒绝: %v", err)
	}
	bal, _ = r.BalanceOf(ctx, accID)
	if bal != 7000 {
		t.Fatalf("拒绝后余额不应变化: %d", bal)
	}
	_, total, _ := r.ListLedger(ctx, accID, 1, 10)
	if total != 2 { // recharge + supply_pay（重放与超扣均无流水）
		t.Fatalf("流水数错误: %d", total)
	}
	_ = d
}

// TestCreateSupplyOrderIdempotent 同 downstream_order_no 重复 → 返回首单。
func TestCreateSupplyOrderIdempotent(t *testing.T) {
	r, _ := newSupplierTestData(t)
	ctx := context.Background()
	accID := seedAccount(t, r)

	o1, err := r.CreateSupplyOrder(ctx, accID, "DOWN-100", []map[string]any{{"product_id": uint64(1)}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CreateSupplyOrder(ctx, accID, "DOWN-100", []map[string]any{{"product_id": uint64(1)}}, 1000)
	if err == nil {
		t.Fatal("重复 downstream_order_no 必须拒绝（UNIQUE）")
	}
	got, err := r.GetSupplyOrderByNo(ctx, "DOWN-100")
	if err != nil || got.ID != o1.ID {
		t.Fatalf("按单号查不到首单: %v", err)
	}
}

// TestAccountSecretEncrypted 库内 secret 为密文；解密往返正确。
func TestAccountSecretEncrypted(t *testing.T) {
	r, _ := newSupplierTestData(t)
	ctx := context.Background()
	acc, err := r.CreateAccount(ctx, "密文测试", "key-sec", "plain-secret-xyz", "", "zcard", "")
	if err != nil {
		t.Fatal(err)
	}
	row, _ := r.GetAccount(ctx, acc.ID)
	if string(row.APISecret) == "plain-secret-xyz" {
		t.Fatal("api_secret 列出现明文")
	}
	_, secret, err := r.AccountByKey(ctx, "key-sec")
	if err != nil {
		t.Fatal(err)
	}
	if secret != "plain-secret-xyz" {
		t.Fatalf("解密错误: %q", secret)
	}
}

// TestCallbackLifecycle 回调记录：创建 → 结果标记 → 手动重发。
func TestCallbackLifecycle(t *testing.T) {
	r, _ := newSupplierTestData(t)
	ctx := context.Background()
	accID := seedAccount(t, r)
	o, err := r.CreateSupplyOrder(ctx, accID, "DOWN-CB", []map[string]any{{"product_id": uint64(1)}}, 500)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := r.CreateCallback(ctx, o.ID, accID, "DOWN-CB", "https://down.example.com/notify", "trace-1")
	if err != nil {
		t.Fatal(err)
	}
	if cb.CallbackStatus.String() != "pending" {
		t.Fatalf("初始应 pending: %s", cb.CallbackStatus)
	}
	if err := r.MarkCallbackResult(ctx, cb.ID, false, "下游 500"); err != nil {
		t.Fatal(err)
	}
	cb, _ = r.GetCallbackByOrder(ctx, o.ID)
	if cb.RetryCount != 1 || cb.CallbackStatus.String() != "failed" {
		t.Fatalf("失败标记错误: %+v", cb)
	}
	// 手动重发
	cb, err = r.ResetCallback(ctx, cb.ID)
	if err != nil || cb.CallbackStatus.String() != "pending" || cb.RetryCount != 0 {
		t.Fatalf("重置错误: %+v", cb)
	}
}

// TestPriceOverride 差异化定价 upsert + 读取。
func TestPriceOverride(t *testing.T) {
	r, _ := newSupplierTestData(t)
	ctx := context.Background()
	accID := seedAccount(t, r)
	if _, err := r.UpsertPrice(ctx, accID, 7, 0, 888); err != nil {
		t.Fatal(err)
	}
	price, err := r.PriceOf(ctx, accID, 7, 0)
	if err != nil || price != 888 {
		t.Fatalf("定价读取错误: %d %v", price, err)
	}
	// 无覆盖返回 0
	price, _ = r.PriceOf(ctx, accID, 8, 0)
	if price != 0 {
		t.Fatalf("无覆盖应返回 0: %d", price)
	}
}
