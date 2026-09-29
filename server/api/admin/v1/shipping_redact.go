package adminv1

import "fmt"

// Detailed logistics and address changes are retained only in authorized order records.
func (x *UpdateShippingRequest) Redact() string {
	return fmt.Sprintf("order=%s shipment=%d received=%t", x.GetOrderNo(), x.GetShipmentId(), x.GetReceived())
}
func (x *ShipOrderRequest) Redact() string {
	return fmt.Sprintf("order=%s items=%d", x.GetOrderNo(), len(x.GetItemIds()))
}
