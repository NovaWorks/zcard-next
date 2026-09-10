package coupon

import (
	"github.com/go-kratos/kratos/v3/errors"
	"math"
)

// Validate writes so malformed/unknown scopes cannot silently match the whole store.
func validatePromotionScope(scope map[string]any) error {
	if scope == nil {
		return errors.BadRequest("coupon.SCOPE_INVALID", "范围必须是 JSON 对象；全场请填写 {}")
	}
	for key, value := range scope {
		if key != "product_ids" && key != "category_ids" {
			return errors.BadRequest("coupon.SCOPE_INVALID", "范围仅支持 product_ids（商品）和 category_ids（分类）")
		}
		ids, ok := value.([]any)
		if !ok || len(ids) == 0 {
			return errors.BadRequest("coupon.SCOPE_INVALID", "编号列表不能为空；全场请填写 {}")
		}
		for _, value := range ids {
			id, ok := value.(float64)
			if !ok || id <= 0 || id > 9007199254740991 || math.Trunc(id) != id {
				return errors.BadRequest("coupon.SCOPE_INVALID", "商品和分类编号必须为有效的正整数")
			}
		}
	}
	return nil
}
