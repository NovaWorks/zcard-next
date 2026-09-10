package order

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"sort"
	"strconv"
)

// ID is a string so JSON round trips never truncate a 64-bit campaign ID.
type flashReservation struct {
	ID       string `json:"id"`
	Quantity int32  `json:"quantity"`
}

func newFlashReservation(id uint64, qty int32) flashReservation {
	return flashReservation{ID: strconv.FormatUint(id, 10), Quantity: qty}
}

func (uc *OrderUsecase) settleFlashReservations(ctx context.Context, o *ent.Order, paid bool) error {
	raw, exists := o.Extra["flash_reservations"]
	if !exists {
		return nil
	} // Legacy orders already consumed quota at creation.
	if uc.Flash == nil {
		return fmt.Errorf("order.FLASH_UNAVAILABLE")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var reservations []flashReservation
	if err = json.Unmarshal(encoded, &reservations); err != nil {
		return fmt.Errorf("order.FLASH_SNAPSHOT_INVALID: %w", err)
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].ID < reservations[j].ID })
	for _, r := range reservations {
		id, err := strconv.ParseUint(r.ID, 10, 64)
		if err != nil || id == 0 || r.Quantity <= 0 {
			return fmt.Errorf("order.FLASH_SNAPSHOT_INVALID")
		}
		if paid {
			err = uc.Flash.Confirm(ctx, id, r.Quantity)
		} else {
			err = uc.Flash.Release(ctx, id, r.Quantity)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
