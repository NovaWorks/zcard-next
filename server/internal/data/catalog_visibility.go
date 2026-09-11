package data

import (
	"context"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/predicate"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

// HiddenCategoryIDs includes descendants without overwriting their own visibility.
// Read failures propagate: an unavailable category policy must never expose products.
func HiddenCategoryIDs(ctx context.Context, client *ent.Client, subsiteID uint64) ([]uint64, error) {
	rows, err := client.Category.Query().Where(category.SubsiteID(subsiteID)).All(ctx)
	if err != nil {
		return nil, err
	}
	children := map[uint64][]uint64{}
	hidden := map[uint64]bool{}
	var queue []uint64
	for _, c := range rows {
		children[c.ParentID] = append(children[c.ParentID], c.ID)
		visible := len(c.VisibleSubsites) == 0
		for _, id := range c.VisibleSubsites {
			if id == subsiteID {
				visible = true
			}
		}
		if c.Hide || !visible {
			hidden[c.ID] = true
			queue = append(queue, c.ID)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, id := range children[queue[i]] {
			if !hidden[id] {
				hidden[id] = true
				queue = append(queue, id)
			}
		}
	}
	return queue, nil
}

func VisibleProductCategory(ids []uint64) predicate.Product {
	return product.Or(product.CategoryIDIsNil(), product.CategoryIDNotIn(ids...))
}

func CategoryHidden(ids []uint64, id uint64) bool {
	for _, hidden := range ids {
		if hidden == id {
			return true
		}
	}
	return false
}
