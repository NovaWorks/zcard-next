package procurement

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/wallet"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

func cardRejectionFixture(t *testing.T, driver string) (*ProcureService, *data.Data, *payment.PaymentRepoImpl, *ent.ProcurementOrder, *supplyport.CardPurchaseRejection) {
	t.Helper()
	s, d, _, iid := submitFixture(t)
	ctx := t.Context()
	d.Client.User.Create().SetID(1).SetUsername("rejected-card-buyer").SaveX(ctx)
	it := d.Client.OrderItem.GetX(ctx, iid)
	d.Client.Order.UpdateOneID(it.OrderID).SetBaseCurrency("CNY").SetTotalAmount(501).SetStatus("fulfilling").ExecX(ctx)
	conn := d.Client.SupplyConnection.Create().SetName("card supplier").SetDriver(driver).SetBaseURL("https://supplier.invalid").SetCredentials([]byte("sealed")).SaveX(ctx)
	quoteID := "owned-wholesale-quote"
	if driver == "acg_faka" {
		quoteID = ""
	}
	frozen := struct {
		Request supplyport.CardPurchaseRequest `json:"request"`
		Quote   supplyport.CardPurchaseQuote   `json:"quote"`
	}{
		Request: supplyport.CardPurchaseRequest{ProductID: it.ProductID, SKUID: it.SkuID, ConnectionID: conn.ID, ProductCode: "P1", Quantity: 2, ManualFormData: map[string]any{"account": "sensitive-input"}},
		Quote:   supplyport.CardPurchaseQuote{AmountCents: 501, CostCents: 250, ProviderQuoteID: quoteID, ProviderAmountCents: 250, ProviderCurrency: "CNY", ConfigurationHash: "frozen-configuration", CostRate: "1"},
	}
	raw, _ := json.Marshal(frozen)
	sealed, e := s.cipher.Seal(string(raw), it.ID, it.SubsiteID)
	if e != nil {
		t.Fatal(e)
	}
	d.Client.OrderItem.UpdateOneID(it.ID).SetAmount(501).SetUnitPrice(250).SetCost(125).SetProfitSnapshot(map[string]any{"card_cost_total_cents": int64(250)}).SetUpstreamCardCipher(sealed).ExecX(ctx)
	po, e := s.repo.CreatePending(ctx, it.ID, conn.ID, "P1", 2, "auto_refund", "fixture")
	if e != nil {
		t.Fatal(e)
	}
	wr := wallet.NewWalletRepoImpl(d)
	wp := wallet.ProvidePortWallet(wr)
	if e = data.Tx(ctx, d, func(ctx context.Context) error {
		if e := wp.CreditInTx(ctx, walletport.Entry{UserID: 1, Direction: "in", Type: "adjust", Amount: 1000, Reference: "buyer-funds"}); e != nil {
			return e
		}
		return wp.DebitInTx(ctx, walletport.Entry{UserID: 1, Direction: "out", Type: "order_pay", Amount: 501, Reference: "buyer-payment", OrderID: it.OrderID})
	}); e != nil {
		t.Fatal(e)
	}
	d.Client.Payment.Create().SetOrderID(it.OrderID).SetChannel("wallet").SetDriverSnapshot("wallet").SetAmount(501).SetChargedAmount(501).SetStatus("success").SaveX(ctx)
	box, _ := crypto.NewBox(make([]byte, 32))
	pay := payment.NewPaymentRepoImpl(d, box, payment.NewRegistry(), nil, wp, wallet.ProvidePortPoints(wr), data.NewOutboxWriter(d), nil, nil, nil)
	pay.SetCardPurchaseCipher(s.cipher)
	s.refund = pay
	rejection := &supplyport.CardPurchaseRejection{QuoteID: quoteID, ReceiptID: "77", Currency: "CNY", Status: "rejected", ErrorCode: "card_quote_expired", Cause: supplyport.ErrUpstreamUnavailable}
	return s, d, pay, po, rejection
}

func TestCardPurchaseRejectionRefundsBuyerActualWalletAmountOnce(t *testing.T) {
	for _, kind := range []string{"zero_charge", "full_supply_refund", "direct_acg_preflight", "zcard_preflight"} {
		t.Run(kind, func(t *testing.T) {
			driver := "zcard"
			if kind == "direct_acg_preflight" {
				driver = "acg_faka"
			}
			s, d, pay, po, receipt := cardRejectionFixture(t, driver)
			var failure error = receipt
			if kind == "full_supply_refund" {
				receipt.Status = "refunded"
				receipt.Charged = true
				receipt.AmountCents = 250
				receipt.ErrorCode = "card_quote_changed"
			}
			if strings.HasSuffix(kind, "preflight") {
				failure = supplyport.ErrCardQuoteChanged
			}
			if e := s.handleSubmitError(t.Context(), po.ID, failure); e != nil {
				t.Fatal(e)
			}
			if e := pay.RefundRejectedCard(t.Context(), po.ID); e != nil {
				t.Fatal("idempotent refund", e)
			}
			current := d.Client.ProcurementOrder.GetX(t.Context(), po.ID)
			if current.Status != "refunded" || current.LastError != supplyport.ErrCardPurchaseRejected.Error() {
				t.Fatal("typed rejection was not settled", current.Status, current.LastError)
			}
			if balance := d.Client.WalletAccount.Query().OnlyX(t.Context()).Available; balance != 1000 {
				t.Fatal("buyer was refunded supplier price instead of actual paid 501", balance)
			}
			refund := d.Client.RefundOrder.Query().OnlyX(t.Context())
			if refund.Amount != 501 || d.Client.WalletTransaction.Query().CountX(t.Context()) != 3 {
				t.Fatal("refund amount or duplicate ledger", refund.Amount)
			}
			proof, _ := json.Marshal(current.UpstreamSourceSnapshot[cardpurchase.RejectionSnapshotKey])
			if strings.Contains(string(proof), "sensitive-input") || strings.Contains(string(proof), "account") {
				t.Fatal("private form leaked in rejection proof", string(proof))
			}
		})
	}
}

func TestCardRejectionProofTamperingCannotCreditBuyer(t *testing.T) {
	for _, field := range []string{"receipt_id", "quote_id", "amount_cents", "currency", "frozen_cipher_hash", "native_cap_cents", "proof_seal", "cost_snapshot", "frozen_cipher", "missing_cipher_injection", "delivered", "non_wallet", "multiple_items"} {
		t.Run(field, func(t *testing.T) {
			s, d, pay, po, receipt := cardRejectionFixture(t, "zcard")
			ctx := t.Context()
			if e := s.recordCardRejection(ctx, po.ID, receipt); e != nil {
				t.Fatal(e)
			}
			po = d.Client.ProcurementOrder.GetX(ctx, po.ID)
			proof := po.UpstreamSourceSnapshot[cardpurchase.RejectionSnapshotKey].(map[string]any)
			it := d.Client.OrderItem.GetX(ctx, po.OrderItemID)
			switch field {
			case "receipt_id":
				proof[field] = "88" // Another well-formed ID still fails the seal.
			case "quote_id":
				proof[field] = "another-quote"
			case "amount_cents":
				proof[field] = 1
			case "currency":
				proof[field] = "USD"
			case "native_cap_cents":
				proof[field] = 251
			case "proof_seal", "frozen_cipher_hash":
				proof[field] = "tampered"
			case "cost_snapshot":
				d.Client.OrderItem.UpdateOneID(it.ID).SetProfitSnapshot(map[string]any{"card_cost_total_cents": 251}).ExecX(ctx)
			case "frozen_cipher":
				bad := append([]byte(nil), it.UpstreamCardCipher...)
				bad[0] ^= 1
				d.Client.OrderItem.UpdateOneID(it.ID).SetUpstreamCardCipher(bad).ExecX(ctx)
			case "missing_cipher_injection":
				pay.SetCardPurchaseCipher(nil)
			case "delivered":
				d.Client.OrderItem.UpdateOneID(it.ID).SetFulfillmentStatus("delivered").ExecX(ctx)
			case "non_wallet":
				paid := d.Client.Payment.Query().OnlyX(ctx)
				d.Client.Payment.UpdateOneID(paid.ID).SetDriverSnapshot("external").ExecX(ctx)
			case "multiple_items":
				d.Client.OrderItem.Create().SetOrderID(it.OrderID).SetProductID(20).SetQuantity(1).SetAmount(100).SetUnitPrice(100).SetFulfillmentType("upstream").SaveX(ctx)
			}
			po.UpstreamSourceSnapshot[cardpurchase.RejectionSnapshotKey] = proof
			d.Client.ProcurementOrder.UpdateOneID(po.ID).SetStatus("refunding").SetLastError(supplyport.ErrCardPurchaseRejected.Error()).SetUpstreamSourceSnapshot(po.UpstreamSourceSnapshot).ExecX(ctx)
			if e := pay.RefundRejectedCard(ctx, po.ID); e == nil {
				t.Fatal("unverified or tampered receipt refunded")
			}
			if b := d.Client.WalletAccount.Query().OnlyX(ctx).Available; b != 499 || d.Client.RefundOrder.Query().CountX(ctx) != 0 || d.Client.WalletTransaction.Query().CountX(ctx) != 2 {
				t.Fatal("rejected proof changed buyer money", b)
			}
		})
	}
}

func TestCardRejectionMismatchAndUnknownStayManualWithoutProof(t *testing.T) {
	for _, kind := range []string{"quote", "amount", "currency", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s, d, _, po, receipt := cardRejectionFixture(t, "zcard")
			var failure error = receipt
			switch kind {
			case "quote":
				receipt.QuoteID = "foreign"
			case "amount":
				receipt.AmountCents = 250
			case "currency":
				receipt.Currency = "USD"
			case "unknown":
				failure = supplyport.ErrUpstreamUncertain
			}
			if e := s.handleSubmitError(t.Context(), po.ID, failure); e != nil {
				t.Fatal(e)
			}
			current := d.Client.ProcurementOrder.GetX(t.Context(), po.ID)
			if current.Status != "manual" || current.UpstreamSourceSnapshot[cardpurchase.RejectionSnapshotKey] != nil || d.Client.RefundOrder.Query().CountX(t.Context()) != 0 {
				t.Fatal(fmt.Sprintf("invalid rejection %s became refund authority", kind))
			}
		})
	}
}
