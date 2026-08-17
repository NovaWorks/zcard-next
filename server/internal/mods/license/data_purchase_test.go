package license

// P3-08 M3 专业套餐在线购买测试：报价（服务端裁决）/ 购买（扣款+签发+自购安装
// 同事务）/ 余额不足回滚 / 未配置签发密钥拒绝。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/licenseorder"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/license"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	_ "modernc.org/sqlite"
)

// purchaseWalletAdapter 真实钱包扣款适配（与 wallet providers 同构）。
type purchaseWalletAdapter struct{ repo *wallet.WalletRepoImpl }

func (a purchaseWalletAdapter) CreditInTx(ctx context.Context, e walletport.Entry) error {
	return a.repo.CreditInTx(ctx, wallet.Entry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type,
		Amount: int64(e.Amount), Reference: e.Reference, OrderID: e.OrderID,
		OperatorID: e.Operator, Remark: e.Remark,
	})
}
func (a purchaseWalletAdapter) DebitInTx(ctx context.Context, e walletport.Entry) error {
	return a.repo.DebitInTx(ctx, wallet.Entry{
		UserID: e.UserID, Direction: e.Direction, Type: e.Type,
		Amount: int64(e.Amount), Reference: e.Reference, OrderID: e.OrderID,
		OperatorID: e.Operator, Remark: e.Remark,
	})
}
func (a purchaseWalletAdapter) Lock(ctx context.Context, userID uint64, amount money.Cents, availableAt int64) error {
	return a.repo.Lock(ctx, userID, int64(amount), availableAt)
}
func (a purchaseWalletAdapter) Unlock(ctx context.Context, userID uint64, amount money.Cents) error {
	return a.repo.Unlock(ctx, userID, int64(amount))
}

// newPurchaseEnv 测试环境：密钥对配置（pubkey + 签发私钥）+ 购买人余额 1000。
func newPurchaseEnv(t *testing.T) (*PurchaseRepo, *LicenseRepo, *data.Data, *wallet.WalletRepoImpl) {
	t.Helper()
	handle, err := db.SQLite.Open(fmt.Sprintf("file:licpurchase%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	ctx := context.Background()

	pub, priv, err := license.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeSettingsStore{m: map[string]json.RawMessage{
		"license/pubkey":         json.RawMessage(`"` + base64.StdEncoding.EncodeToString(pub) + `"`),
		"license/purchase_privkey": json.RawMessage(`"` + base64.StdEncoding.EncodeToString(priv) + `"`),
	}}
	licRepo := NewLicenseRepo(store)
	wrepo := wallet.NewWalletRepoImpl(d)
	// 购买人 user 3 余额 1000 分
	if err := wrepo.CreditInTx(ctx, wallet.Entry{
		UserID: 3, Direction: "in", Type: "recharge", Amount: 1000, Reference: "seed:3",
	}); err != nil {
		t.Fatal(err)
	}
	repo := NewPurchaseRepo(d, licRepo, purchaseWalletAdapter{repo: wrepo})
	return repo, licRepo, d, wrepo
}

// TestLicensePurchaseFlow 自购全流程：扣款 → 签发（绑定本实例）→ 安装生效 → 记录可查。
func TestLicensePurchaseFlow(t *testing.T) {
	repo, licRepo, _, wrepo := newPurchaseEnv(t)
	ctx := context.Background()

	offer, err := repo.Offer(ctx)
	if err != nil || !offer.Purchasable || offer.MonthlyCents != 300 || offer.YearlyCents != 3000 {
		t.Fatalf("报价错误: %+v err=%v", offer, err)
	}

	row, err := repo.Purchase(ctx, 3, "monthly", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != licenseorder.StatusSuccess || row.Amount != 300 {
		t.Fatalf("购买单错误: %+v", row)
	}
	// 余额 1000 − 300 = 700
	avail, _, _ := wrepo.GetBalance(ctx, 3)
	if avail != 700 {
		t.Fatalf("扣款错误: %d (want 700)", avail)
	}
	// 自购（instance 空=本实例）：许可证已安装且实时有效、特性=专业套餐清单
	st := licRepo.Status(ctx)
	if !st.Installed || !st.Valid {
		t.Fatalf("自购未激活: %+v", st)
	}
	featureSet := map[string]bool{}
	for _, f := range st.Features {
		featureSet[f] = true
	}
	for _, f := range professionalFeatures {
		if !featureSet[f] {
			t.Fatalf("特性缺失: %s（%+v）", f, st.Features)
		}
	}
	// 购买记录可查
	rows, total, _ := repo.ListPurchases(ctx, 3, 1, 10)
	if total != 1 || len(rows) != 1 || rows[0].LicenseFile == "" {
		t.Fatalf("购买记录错误: total=%d", total)
	}
}

// TestLicensePurchaseInsufficient 余额不足：整事务回滚（零残留）。
func TestLicensePurchaseInsufficient(t *testing.T) {
	repo, _, d, wrepo := newPurchaseEnv(t)
	ctx := context.Background()
	// user 4 无余额
	if _, err := repo.Purchase(ctx, 4, "yearly", "", ""); err == nil {
		t.Fatal("无余额应失败")
	}
	n, _ := d.Client.LicenseOrder.Query().Count(ctx)
	if n != 0 {
		t.Fatalf("失败购买单残留: %d", n)
	}
	_, locked, _ := wrepo.GetBalance(ctx, 4)
	if locked != 0 {
		t.Fatalf("失败后异常锁余额: %d", locked)
	}
}

// TestLicensePurchaseIssuerUnconfigured 未配置签发密钥：明确拒绝（fail-closed）。
func TestLicensePurchaseIssuerUnconfigured(t *testing.T) {
	handle, err := db.SQLite.Open(fmt.Sprintf("file:licpurchase2%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := &data.Data{Client: client, DB: handle, Dialect: db.SQLite}
	store := &fakeSettingsStore{m: map[string]json.RawMessage{}}
	licRepo := NewLicenseRepo(store)
	wrepo := wallet.NewWalletRepoImpl(d)
	repo := NewPurchaseRepo(d, licRepo, purchaseWalletAdapter{repo: wrepo})

	if _, err := repo.Purchase(context.Background(), 3, "monthly", "", ""); err == nil {
		t.Fatal("未配置签发密钥应拒绝")
	}
	offer, _ := repo.Offer(context.Background())
	if offer.Purchasable {
		t.Fatal("未配置密钥 offer 不应可购")
	}
}
