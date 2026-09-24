package schema

// 所有权：mods/catalog。分类内运营配置与商品所属分类、首页推荐独立。
import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type CategoryProductPlacement struct{ ent.Schema }

func (CategoryProductPlacement) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }
func (CategoryProductPlacement) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"), field.Uint64("category_id"), field.Uint64("product_id"),
		field.Bool("is_pinned").Default(false), field.Bool("is_recommended").Default(false), field.Int32("position").Default(0),
	}
}
func (CategoryProductPlacement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "category_id", "product_id").Unique(), index.Fields("subsite_id", "product_id"),
	}
}
