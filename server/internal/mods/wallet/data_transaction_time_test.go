package wallet

import (
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"testing"
	"time"
)

func TestTransactionTimeAndPagination(t *testing.T) {
	svc, repo := newStoreWalletService(t, nil)
	ctx := userCtx(1)
	stamp := time.Date(2026, 9, 10, 12, 34, 56, 0, time.UTC)
	for i := 0; i < 3; i++ {
		repo.data.Client.WalletTransaction.Create().SetUserID(1).SetDirection("in").SetType("adjust").SetAmount(100).SetBalanceBefore(int64(i * 100)).SetBalanceAfter(int64((i + 1) * 100)).SetReference(stamp.Add(time.Duration(i) * time.Second).String()).SetCreatedAt(stamp.Add(time.Duration(i) * time.Second)).SaveX(ctx)
	}
	repo.data.Client.WalletTransaction.Create().SetUserID(2).SetDirection("in").SetType("adjust").SetAmount(999).SetBalanceBefore(0).SetBalanceAfter(999).SetReference("other-user").SaveX(ctx)
	page, err := svc.ListTransactions(ctx, &storefrontv1.ListTxRequest{Page: 2, PageSize: 2})
	if err != nil || page.Total != 3 || len(page.Transactions) != 1 || page.Transactions[0].CreatedAt != stamp.Unix() {
		t.Fatalf("store page: %+v %v", page, err)
	}
	admin := NewAdminWalletService(repo, repo.data, nil)
	got, err := admin.ListTransactions(ctx, &adminv1.ListWalletTxRequest{UserId: 1, Page: 1, PageSize: 2})
	if err != nil || got.Total != 3 || len(got.Transactions) != 2 || got.Transactions[0].CreatedAt != stamp.Add(2*time.Second).Unix() {
		t.Fatalf("admin page: %+v %v", got, err)
	}
}
