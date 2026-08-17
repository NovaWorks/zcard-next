package schema

// 所有权：mods/catalog（M1；M0 先落表结构支撑 storefront 列表链路）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Product 商品主表（《数据库架构设计.md》§4.1，字段精确对齐）。
type Product struct {
	ent.Schema
}

func (Product) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("category_id").Optional().Comment("分类（软外键，仅索引）"),
		field.String("name").MaxLen(150),
		field.String("slug").MaxLen(150).Comment("唯一标识"),
		field.Text("description").Optional().Comment("详情（服务端 sanitize 后存储）"),
		field.String("cover").MaxLen(255).Optional(),
		field.JSON("images", []string{}).Optional().Comment("详情图集"),
		// 金额一律 int64 分（铁律 1）
		field.Int64("price").Default(0).Comment("售价（分）"),
		field.Int64("factory_price").Default(0).Comment("成本价（分，上游/自营成本快照）"),
		field.Int64("draft_premium").Default(0).Comment("预选卡密默认加价（分）"),
		field.JSON("member_price", map[string]int64{}).Optional().Comment("按会员等级价 {level_id: 分}"),
		field.Int64("points_required").Default(0).Comment("积分兑换价（分单位积分；0=不参与积分商城，P3-01）"),
		field.Enum("stock_type").Values("card", "url", "code").Default("card").Comment("卡密/链接/兑换码"),
		field.Bool("stock_visible").Default(true).Comment("是否显示库存"),
		field.Enum("delivery_mode").Values("status", "delete").Default("status").Comment("发货模式：标记/即删"),
		field.JSON("control_config", map[string]any{}).Optional().Comment("自定义控件配置（结构化控件走 product_controls 表，M1）"),
		field.Bool("dedup").Default(true).Comment("导入去重开关"),
		field.Int32("sort").Default(0),
		field.Int8("status").Default(1).Comment("1=上架 0=下架 2=隐藏（游客不可见会员可见）"),
		field.Uint64("upstream_source_id").Optional().Comment("货源连接（NULL=自营；M2）"),
		field.String("upstream_product_code").MaxLen(128).Optional().Comment("上游商品标识（M2）"),
		field.Time("upstream_synced_at").SchemaType(mysqlTime).Optional(),
	}
}

func (Product) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "slug").Unique(),
		index.Fields("subsite_id", "category_id"),
		index.Fields("subsite_id", "status"),
		index.Fields("upstream_source_id"),
	}
}

func (Product) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("skus", ProductSku.Type).Comment("多规格"),
		edge.To("cards", Card.Type).Comment("库存卡密（硬 FK）"),
	}
}

// ProductSku 多规格（《数据库架构设计.md》§4.1）。
type ProductSku struct {
	ent.Schema
}

func (ProductSku) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (ProductSku) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id").Comment("所属商品（硬外键 → products）"),
		field.String("name").MaxLen(100).Comment("规格名（如「月卡」）"),
		field.JSON("spec_values", map[string]string{}).Comment("规格值组合 {规格: 值}"),
		field.Int64("price").Optional().Comment("独立售价（分，NULL=继承商品价）"),
		field.Int64("cost").Optional().Comment("独立成本（分）"),
		field.Int32("stock_offset").Default(0).Comment("独立库存位"),
		field.String("upstream_sku_id").MaxLen(64).Optional().Comment("上游 SKU 标识（M2）"),
	}
}

func (ProductSku) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id"),
		index.Fields("product_id", "name").Unique(),
	}
}

func (ProductSku) Edges() []ent.Edge {
	return []ent.Edge{
		// 硬外键 → products（核心聚合内）
		edge.From("product", Product.Type).
			Ref("skus").
			Field("product_id").
			Required().
			Unique(),
	}
}
