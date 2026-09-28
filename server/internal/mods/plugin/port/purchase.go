package port

import "context"

// Begin must precede every SQL product lock. Check runs inside PurchaseTx;
// Release runs after commit/rollback. No admin permission is involved.
type PurchaseGate interface {
	Begin(context.Context) (PurchaseSession, error)
}
type PurchaseSession interface {
	Check(context.Context, PurchaseInput) ([]map[string]any, error)
	Release()
}
type PurchaseItem struct {
	ProductID, SKUID uint64
	Quantity         int32
}
type PurchaseInput struct {
	SubsiteID, UserID, LevelID uint64
	Channel                    string
	Items                      []PurchaseItem
}
