package cardpurchase

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const RejectionSnapshotKey = "card_purchase_rejection"
const RejectionSealPrefix = "card_purchase_rejection.v1:"

// RejectionProof contains no customer form values or selected card contents.
// Seal authenticates the complete receipt against the frozen purchase cipher.
type RejectionProof struct {
	Kind              string `json:"kind"`
	QuoteID           string `json:"quote_id"`
	ReceiptID         string `json:"receipt_id"`
	Currency          string `json:"currency"`
	Status            string `json:"status"`
	ErrorCode         string `json:"error_code"`
	AmountCents       int64  `json:"amount_cents"`
	Charged           bool   `json:"charged"`
	ProductID         uint64 `json:"product_id"`
	SKUID             uint64 `json:"sku_id"`
	ConnectionID      uint64 `json:"connection_id"`
	Quantity          int32  `json:"quantity"`
	ProductCode       string `json:"product_code"`
	UpstreamSKU       string `json:"upstream_sku"`
	NativeCapCents    int64  `json:"native_cap_cents"`
	ConfigurationHash string `json:"configuration_hash"`
	FrozenCipherHash  string `json:"frozen_cipher_hash"`
	Seal              string `json:"proof_seal,omitempty"`
}

func (p RejectionProof) SealPayload() ([]byte, error) { p.Seal = ""; return json.Marshal(p) }
func (p RejectionProof) ValidateReceipt(driver string) error {
	invalid := fmt.Errorf("卡密拒绝凭证不完整")
	if p.ProductID == 0 || p.ConnectionID == 0 || p.Quantity <= 0 || p.ProductCode == "" || p.NativeCapCents <= 0 || p.ConfigurationHash == "" || p.FrozenCipherHash == "" {
		return invalid
	}
	if p.Kind == "preflight" {
		if driver != "acg_faka" && driver != "zcard" || p.Status != "rejected" || p.AmountCents != 0 || p.Charged || p.ReceiptID != "" || p.ErrorCode != "card_quote_changed" {
			return invalid
		}
		return nil
	}
	if p.Kind != "receipt" || driver != "zcard" || p.QuoteID == "" || p.Currency != "CNY" {
		return invalid
	}
	id, e := strconv.ParseUint(p.ReceiptID, 10, 64)
	if e != nil || id == 0 || strconv.FormatUint(id, 10) != p.ReceiptID {
		return invalid
	}
	if p.Status == "refunded" {
		if !p.Charged || p.AmountCents != p.NativeCapCents || p.ErrorCode != "card_quote_changed" && p.ErrorCode != "no_stock" {
			return invalid
		}
		return nil
	}
	if p.Status != "rejected" || p.Charged || p.AmountCents != 0 {
		return invalid
	}
	switch p.ErrorCode {
	case "card_quote_expired", "product_unavailable", "product_changed", "SUPPLY_RESTRICTED", "price_changed", "connection_unavailable", "connection_changed", "insufficient_balance", "no_stock":
		return nil
	}
	return invalid
}
