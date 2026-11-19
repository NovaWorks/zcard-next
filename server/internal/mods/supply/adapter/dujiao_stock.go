package adapter

import "fmt"

func (p dujiaoProduct) stock(skuCode string) (int32, error) {
	seen := map[string]bool{}
	var total int64
	unlimited := false
	for _, sk := range p.SKUs {
		code := idString(sk.ID)
		if code == "" || seen[code] {
			return -2, fmt.Errorf("adapter.dujiao: invalid SKU identity")
		}
		seen[code] = true
		if skuCode != "" && skuCode != code {
			continue
		}
		if !sk.IsActive {
			if skuCode != "" {
				return 0, ErrProductUnavailable
			}
			continue
		}
		if sk.StockQuantity == nil || *sk.StockQuantity < -1 {
			return -2, fmt.Errorf("adapter.dujiao: SKU stock is unknown")
		}
		if skuCode != "" {
			return *sk.StockQuantity, nil
		}
		if *sk.StockQuantity == -1 {
			unlimited = true
		} else {
			total += int64(*sk.StockQuantity)
		}
		if total > 2147483647 {
			return -2, fmt.Errorf("adapter.dujiao: SKU stock overflows")
		}
	}
	if skuCode != "" {
		return 0, ErrProductUnavailable
	}
	if p.SKUs == nil && p.StockQuantity != nil {
		if *p.StockQuantity < -1 {
			return -2, fmt.Errorf("adapter.dujiao: product stock is unknown")
		}
		return *p.StockQuantity, nil
	}
	if p.SKUs == nil {
		return -2, fmt.Errorf("adapter.dujiao: missing SKU stock")
	}
	if unlimited {
		return -1, nil
	}
	return int32(total), nil
}

func dujiaoOrderStopped(status string) bool {
	switch status {
	case "refunded", "partially_refunded", "refunding", "canceled", "cancelled", "failed", "rejected", "expired":
		return true
	}
	return false
}
