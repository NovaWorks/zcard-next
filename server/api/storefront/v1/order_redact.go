package storefrontv1

import "fmt"

// Never log shipping addresses, buyer contact, form answers or query passwords.
func (x *CreateOrderRequest) Redact() string {
	return fmt.Sprintf("checkout items=%d", len(x.GetItems()))
}
func (x *GetOrderRequest) Redact() string { return fmt.Sprintf("order=%s", x.GetOrderNo()) }
func (x *ReceiveShipmentRequest) Redact() string {
	return fmt.Sprintf("order=%s shipment=%d", x.GetOrderNo(), x.GetShipmentId())
}
