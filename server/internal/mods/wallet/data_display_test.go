package wallet

import (
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"testing"
)

func TestLedgerBusinessReferencesPreserveAccountingAndOwnership(t *testing.T) {
	svc, repo := newStoreWalletService(t, nil)
	ctx := userCtx(1)
	c := repo.data.Client
	o := c.Order.Create().SetOrderNo("O-20260910-123").SetUserID(1).SetTotalAmount(100).SetCost(0).SaveX(ctx)
	other := c.Order.Create().SetOrderNo("OTHER-PRIVATE").SetUserID(2).SetTotalAmount(100).SetCost(0).SaveX(ctx)
	refund := c.RefundOrder.Create().SetOrderID(o.ID).SetAmount(100).SetChannel("wallet").SaveX(ctx)
	cases := []struct{ kind, ref, want string }{
		{"order_pay", fmt.Sprintf("order_pay:%d", o.ID), o.OrderNo},
		{"refund", fmt.Sprintf("order_refund:%d", refund.ID), o.OrderNo},
		{"ticket_urgent", "ticket_urgent:T-123", "工单 T-123"},
		{"order_pay", fmt.Sprintf("order_pay:%d", other.ID), fmt.Sprintf("订单 #%d", other.ID)},
		{"order_pay", "order_pay:999999", "订单 #999999"},
		{"adjust", "adjust:55", "调账记录 #55"},
	}
	want := map[string]string{}
	for _, tt := range cases {
		c.WalletTransaction.Create().SetUserID(1).SetDirection("out").SetType(tt.kind).SetAmount(100).SetBalanceBefore(200).SetBalanceAfter(100).SetReference(tt.ref).SaveX(ctx)
		want[tt.ref] = tt.want
	}
	got, err := svc.ListTransactions(ctx, &storefrontv1.ListTxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Transactions) != len(cases) {
		t.Fatal(got)
	}
	for _, tx := range got.Transactions {
		if tx.DisplayReference != want[tx.Reference] || tx.AmountCents != 100 || tx.Direction != "out" {
			t.Fatalf("unexpected display/accounting: %+v", tx)
		}
		stored := c.WalletTransaction.GetX(ctx, tx.Id)
		if stored.Reference != tx.Reference || stored.Amount != tx.AmountCents {
			t.Fatal("stored ledger changed")
		}
	}
	admin := NewAdminWalletService(repo, repo.data, nil)
	all, err := admin.ListTransactions(ctx, &adminv1.ListWalletTxRequest{UserId: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range all.Transactions {
		if tx.DisplayReference != want[tx.Reference] {
			t.Fatal(tx)
		}
	}
}
