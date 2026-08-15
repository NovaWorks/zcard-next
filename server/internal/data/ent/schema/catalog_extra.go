package schema

// 所有权：mods/catalog（P1-01）/ mods/inventory（P1-02）/ mods/order（P1-03 购物车）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Category 分类树（parent_id 树形；分站可见性白名单）。
type Category struct {
	ent.Schema
}

func (Category) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Category) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("parent_id").Optional().Comment("父分类（NULL=根）"),
		field.String("name").MaxLen(60),
		field.String("icon").MaxLen(255).Optional(),
		field.Bool("hide").Default(false),
		field.Int32("sort").Default(0),
		field.JSON("visible_subsites", []uint64{}).Optional().Comment("分站可见性白名单（空=全部可见）"),
	}
}

func (Category) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "parent_id"),
	}
}

// Tag 商品标签（position：右上方/标题前/右下方）。
type Tag struct {
	ent.Schema
}

func (Tag) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Tag) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60),
		field.String("slug").MaxLen(80).Unique(),
		field.String("icon").MaxLen(255).Optional(),
		field.String("color").MaxLen(20).Optional(),
		field.Enum("position").Values("top_right", "before_title", "bottom_right").Default("top_right"),
		field.Bool("hide").Default(false),
	}
}

// ProductControl 自定义控件（下单收集：文本/密码/下拉/数字/多选/单选）。
type ProductControl struct {
	ent.Schema
}

func (ProductControl) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (ProductControl) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id"),
		field.String("name").MaxLen(60),
		field.Enum("type").Values("text", "password", "select", "number", "checkbox", "radio"),
		field.Bool("required").Default(false),
		field.JSON("options", []string{}).Optional().Comment("选项（select/checkbox/radio）"),
		field.Int32("sort").Default(0),
	}
}

func (ProductControl) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id"),
	}
}

// MemberProductGroup 会员商品组（组名/商品集/折扣/叠加规则）。
type MemberProductGroup struct {
	ent.Schema
}

func (MemberProductGroup) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (MemberProductGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60),
		field.JSON("product_ids", []uint64{}),
		field.Int32("discount").Default(0).Comment("折扣（万分比）"),
		field.Bool("stack_member").Default(false).Comment("与会员等级折扣叠加"),
		field.Bool("stack_coupon").Default(false).Comment("与优惠券叠加"),
		field.String("badge_style").MaxLen(60).Optional().Comment("标签样式"),
	}
}

// Review 真实评价（已付订单一单一评；审核流）。
type Review struct {
	ent.Schema
}

func (Review) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Review) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id"),
		field.Uint64("user_id"),
		field.Uint64("order_id"),
		field.Int8("rating").Comment("1-5"),
		field.Text("content").Comment("入库前 sanitize"),
		field.Enum("status").Values("pending", "approved", "rejected").Default("pending"),
	}
}

func (Review) Indexes() []ent.Index {
	return []ent.Index{
		// 一单一评
		index.Fields("order_id").Unique(),
		index.Fields("product_id", "status"),
	}
}

// VirtualReview 虚拟评论（与真实评价合并展示）。
type VirtualReview struct {
	ent.Schema
}

func (VirtualReview) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id"),
		field.String("nickname").MaxLen(60),
		field.Text("content"),
		field.Int8("rating").Default(5),
		field.Int32("sort").Default(0),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (VirtualReview) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id"),
	}
}

// CardImport 导入批次（模板/预览/分片/撤销；P1-02 任务书 T2）。
type CardImport struct {
	ent.Schema
}

func (CardImport) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (CardImport) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id"),
		field.String("filename").MaxLen(255),
		field.Int32("total").Default(0).Comment("文件总行数"),
		field.Int32("imported").Default(0).Comment("已导入（进度回填）"),
		field.Int32("skipped").Default(0).Comment("重复跳过"),
		field.Int32("failed").Default(0),
		field.Enum("status").Values("pending", "processing", "done", "failed", "canceled").Default("pending"),
		field.Uint64("operator_id").Optional(),
	}
}

func (CardImport) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id", "created_at"),
	}
}

// CartItem 购物车（UNIQUE(user_id, product_id, sku_id)——sku_id 用 0 哨兵参与唯一索引，
// 规避三方言「唯一索引含 NULL 列可重复」语义差异）。
type CartItem struct {
	ent.Schema
}

func (CartItem) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (CartItem) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.Uint64("product_id"),
		field.Uint64("sku_id").Default(0).Comment("0=商品级（哨兵，参与唯一索引）"),
		field.Int32("quantity").Default(1),
		field.Enum("fulfillment_type").Values("auto", "manual", "upstream").Default("auto"),
	}
}

func (CartItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "product_id", "sku_id").Unique(),
	}
}
