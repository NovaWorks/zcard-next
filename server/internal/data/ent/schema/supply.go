package schema

// 所有权：mods/supply（P2-01）/ mods/procurement（P2-02）
// 字段对齐《数据库架构设计.md》§4.7/§5。

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SupplyConnection 上游连接（凭据 AES-GCM；SSRF 校验；定价三参数；健康缓存）。
type SupplyConnection struct {
	ent.Schema
}

func (SupplyConnection) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplyConnection) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(100),
		field.String("driver").MaxLen(30).Comment("zcard | dujiao_next | acg_faka"),
		field.String("base_url").MaxLen(255).Comment("上游地址（httpx SSRF 校验）"),
		field.Bytes("credentials").Comment("AES-256-GCM 加密凭据（结构随 driver）"),
		field.Enum("status").Values("active", "disabled").Default("active"),
		field.String("callback_url").MaxLen(500).Optional().Comment("本站作下游时的回调登记"),
		field.Int32("retry_max").Default(5),
		field.String("retry_intervals").MaxLen(200).Default("[30,60,300]").Comment("重试间隔数组（秒，JSON 字符串）"),
		field.Float("exchange_rate").
			SchemaType(map[string]string{dialect.MySQL: "decimal(20,8)", dialect.Postgres: "numeric(20,8)", dialect.SQLite: "real"}).
			Default(1).Comment("汇率（上游价 × 汇率 = 本地价）"),
		field.Float("price_markup_percent").
			SchemaType(map[string]string{dialect.MySQL: "decimal(20,8)", dialect.Postgres: "numeric(20,8)", dialect.SQLite: "real"}).
			Default(0).Comment("加价百分比（100=翻倍；与固定加价可组合）"),
		field.Int64("price_markup_amount").Default(0).Comment("固定加价（分；本地价 = 上游价×汇率×(1+%) + 固定额 → 取整）"),
		field.Enum("price_rounding_mode").Values("none", "ceil_int", "ceil_tenth").Default("none"),
		field.Bool("auto_sync_price").Default(true).Comment("同步时自动更新本地价（manual 改价保护）"),
		field.Enum("stock_mode").Values("real", "plenty").Default("real"),
		field.JSON("settings", map[string]any{}).Optional().Comment("其余开关（发卡模式/失败策略等）"),
		field.Time("last_ping_at").SchemaType(mysqlTime).Optional(),
		field.Bool("last_ping_ok").Default(false),
		field.Time("last_synced_at").SchemaType(mysqlTime).Optional(),
		field.Text("last_error").Optional(),
		field.Int64("balance_cache").Default(0).Comment("上游余额缓存（分）"),
		// P2-10 S2/S3：定时调度锚点（三类 scope 各自的下次执行判据）
		field.Time("last_collect_at").SchemaType(mysqlTime).Optional(),
		field.Time("last_price_sync_at").SchemaType(mysqlTime).Optional(),
		field.Time("last_status_sync_at").SchemaType(mysqlTime).Optional(),
		// P2-10 S2：自适应节奏器（AIMD）状态与渠道熔断冷却
		field.JSON("rate_state", map[string]any{}).Optional().Comment("节奏器持久状态（当前间隔/连续成功/封锁计数/冷却时长）"),
		field.Time("rate_limit_until").SchemaType(mysqlTime).Optional().Comment("熔断冷却截止（非空且未到=调度与出站跳过）"),
	}
}

func (SupplyConnection) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "driver"),
		index.Fields("last_synced_at"),
	}
}

// SupplyMapping 商品映射（上游分类/商品/SKU ↔ 本地）。
type SupplyMapping struct {
	ent.Schema
}

func (SupplyMapping) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplyMapping) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("connection_id"),
		field.String("upstream_category").MaxLen(128).Optional(),
		field.Uint64("local_category_id").Optional(),
		field.String("upstream_product").MaxLen(128),
		field.Uint64("local_product_id").Optional(),
		field.String("upstream_sku").MaxLen(64).Default(""),
		field.Uint64("local_sku_id").Optional(),
		field.Int32("up_stock").Default(0).Comment("库存缓存（-1=无限）"),
		field.JSON("pricing_override", map[string]any{}).Optional(),
	}
}

func (SupplyMapping) Indexes() []ent.Index {
	return []ent.Index{
		// sku 用空串哨兵参与唯一索引（NULL 可重复问题同 cart_items）
		index.Fields("connection_id", "upstream_product", "upstream_sku").Unique(),
		index.Fields("local_product_id"),
	}
}

// SupplySyncTask 同步任务追踪（进度/统计/心跳/取消；1.x SupplySyncTask 对齐）。
type SupplySyncTask struct {
	ent.Schema
}

func (SupplySyncTask) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplySyncTask) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("connection_id"),
		field.String("mode").MaxLen(20).Comment("full | incremental"),
		field.String("scope").MaxLen(60).Optional(),
		field.Bool("force_reprice").Default(false),
		field.Enum("status").Values("pending", "processing", "done", "failed", "canceled").Default("pending"),
		field.Int32("total_count").Default(0),
		field.Int32("processed_count").Default(0),
		field.Int32("created_count").Default(0),
		field.Int32("updated_count").Default(0),
		field.Int32("price_updated_count").Default(0),
		field.Int32("manual_skipped_count").Default(0),
		field.Int32("hidden_count").Default(0),
		field.Int32("deleted_count").Default(0),
		field.String("error_code").MaxLen(64).Optional(),
		field.Text("error_context").Optional(),
		field.Time("started_at").SchemaType(mysqlTime).Optional(),
		field.Time("heartbeat_at").SchemaType(mysqlTime).Optional().Comment("心跳（卡死检测）"),
		field.String("current_stage").MaxLen(60).Optional(),
		field.Int32("current_page").Default(0),
		field.Time("cancel_requested_at").SchemaType(mysqlTime).Optional(),
		field.String("worker_version").MaxLen(40).Optional(),
		field.Time("finished_at").SchemaType(mysqlTime).Optional(),
	}
}

func (SupplySyncTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("connection_id", "created_at"),
		index.Fields("status"),
	}
}

// ProcurementOrder 采购单（状态机 §5.7.2；三通道结果汇聚；dedupe 幂等）。
type ProcurementOrder struct {
	ent.Schema
}

func (ProcurementOrder) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ProcurementOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_item_id").Comment("触发采购的订单子项"),
		field.Uint64("connection_id"),
		field.String("upstream_order_id").MaxLen(80).Optional(),
		field.Enum("status").
			Values("pending", "submitted", "polling", "fulfilled", "rejected", "refunding", "refunded", "manual", "failed").
			Default("pending"),
		field.Enum("fail_strategy").Values("auto_refund", "manual").Default("auto_refund"),
		field.Int32("retry_count").Default(0),
		field.Time("next_retry_at").SchemaType(mysqlTime).Optional().Comment("退避重试调度"),
		field.Time("last_poll_at").SchemaType(mysqlTime).Optional().Comment("巡检扫描锚点"),
		field.String("dedupe_key").MaxLen(120).Unique().Comment("幂等键（order_item 派生）"),
		field.String("trace_id").MaxLen(64).Optional(),
		field.Text("last_error").Optional().Comment("最近失败原因（重试/审计）"),
		field.String("upstream_refund_id").MaxLen(80).Optional().Comment("上游退款单号（退款传导回填）"),
	}
}

func (ProcurementOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("connection_id", "upstream_order_id"),
		index.Fields("order_item_id"),
		index.Fields("status", "last_poll_at"),
	}
}

// ProcurementItem 采购明细（显式多卡密/多 SKU；到手即加密）。
type ProcurementItem struct {
	ent.Schema
}

func (ProcurementItem) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ProcurementItem) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("procurement_id"),
		field.String("upstream_sku").MaxLen(64).Default(""),
		field.Int32("quantity"),
		field.Int64("unit_cost").Default(0).Comment("单位成本（分）"),
		field.JSON("received_content", []string{}).Optional().Comment("到手卡密密文（base64 行，AES-GCM，零明文落盘）"),
	}
}

func (ProcurementItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("procurement_id"),
	}
}
