package adminv1

import "fmt"

// Redact keeps saleable card contents out of Kratos request logs. These methods
// live outside generated code so regeneration preserves the logging boundary.
func (x *ImportPreviewRequest) Redact() string {
	return fmt.Sprintf("product_id:%d sku_id:%d line_count:%d dedup:%t", x.GetProductId(), x.GetSkuId(), len(x.GetLines()), x.GetDedup())
}

func (x *ImportConfirmRequest) Redact() string {
	return fmt.Sprintf("product_id:%d sku_id:%d line_count:%d dedup:%t expected_revision:%d", x.GetProductId(), x.GetSkuId(), len(x.GetLines()), x.GetDedup(), x.GetExpectedRevision())
}

func (x *AssignCardsRequest) Redact() string {
	// request_key is caller-controlled and may itself contain a credential.
	return fmt.Sprintf("product_id:%d sku_id:%d card_ids:%v expected_revision:%d", x.GetProductId(), x.GetSkuId(), x.GetCardIds(), x.GetExpectedRevision())
}
