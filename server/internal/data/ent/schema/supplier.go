package schema

// 所有权：mods/supplier（P2-03 对外供货：本站作上游）
// 字段对齐《数据库架构设计.md》§4.7/§5；HMAC 四头协议见主文档 §5.8。

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SupplierAccount 下游账户（申请→审核→发 key；secret 只显示一次）。
type SupplierAccount struct {
	ent.Schema
}

func (SupplierAccount) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplierAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(100),
		field.String("api_key").MaxLen(64).Unique().Comment("下游识别 key（可索引明文）"),
		field.Bytes("api_secret").Comment("AES-256-GCM 加密 secret"),
		field.String("contact").MaxLen(255).Optional().Comment("联系方式（申请审核用）"),
		field.Enum("status").Values("applying", "approved", "rejected", "disabled").Default("applying"),
		field.Int64("balance_cache").Default(0).Comment("供货余额缓存（可由流水重算）"),
		field.String("notify_url").MaxLen(500).Optional().Comment("交付回调地址（HTTPS 强制）"),
		field.Time("reviewed_at").SchemaType(mysqlTime).Optional(),
		// P2-10 B/C：该账号对外说话用的协议（决定兼容层凭据形态与回调签名器）。
		// dujiao_next：api_key=Api-Key、secret=签名密钥；acg_faka：api_key=数字
		// app_id、secret=32 位 app_key（MD5 验签时解密参与）。
		field.Enum("protocol").Values("zcard", "dujiao_next", "acg_faka").Default("zcard"),
		field.String("display_name").MaxLen(100).Optional().Comment("connect/ping 回显的店铺名（shopName/site_name）"),
		// 前台对接申请（P3-xx）：owner=前台用户 id（admin 手动建号恒 0）；
		// apply_reason 申请理由、review_note 审核意见（驳回时必填）。
		field.Uint64("owner_user_id").Optional().Default(0).Comment("申请归属前台用户（0=admin 建号）"),
		field.String("apply_reason").MaxLen(500).Optional().Comment("申请理由（审核用）"),
		field.String("review_note").MaxLen(500).Optional().Comment("审核意见/驳回理由"),
		// IP 白名单（申请通过后可配置）：空数组 = 所有 IP 放行；非空 = 仅命中（精确 IP
		// 或 CIDR 网段）的请求可调供货/兼容接口。鉴权层（三协议统一）强制。
		field.JSON("ip_whitelist", []string{}).Optional().Comment("IP 白名单（空=不限；支持精确 IP 与 CIDR）"),
	}
}

func (SupplierAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_user_id"),
	}
}

// SupplierLedgerEntry 供货余额账本（幂等键 supply_order:<downID>；append-only）。
type SupplierLedgerEntry struct {
	ent.Schema
}

func (SupplierLedgerEntry) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("account_id"),
		field.Uint64("supply_order_id").Optional(),
		field.String("type").MaxLen(40).Comment("recharge/supply_pay/supply_refund/adjust"),
		field.Int64("amount").Comment("有符号（分）"),
		field.String("currency").MaxLen(3).Default("CNY"),
		field.String("reference").MaxLen(120).Unique().Comment("幂等键"),
		field.String("remark").MaxLen(255).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (SupplierLedgerEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "created_at"),
	}
}

// SupplyOrder 下游订单（downstream_order_no 幂等；复用 inventory 锁卡）。
type SupplyOrder struct {
	ent.Schema
}

func (SupplyOrder) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplyOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("account_id"),
		field.String("downstream_order_no").MaxLen(64).Unique().Comment("下游单号（幂等锚点）"),
		field.JSON("items", []map[string]any{}).Comment("商品行快照（上游商品/SKU/数量/单价）"),
		field.Int64("amount").Comment("应付（分，供货价口径）"),
		field.Enum("status").
			Values("pending", "paid", "fulfilling", "fulfilled", "rejected", "refunded").
			Default("pending"),
		field.Uint64("local_order_id").Optional().Comment("转本地订单（复用交付出口时回填）"),
		field.Time("paid_at").SchemaType(mysqlTime).Optional(),
		field.Time("fulfilled_at").SchemaType(mysqlTime).Optional(),
	}
}

func (SupplyOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "created_at"),
		index.Fields("status"),
	}
}

// SupplyNonce 供货 API nonce 防重放（Redis 优先，DB 兜底；过期清理 cron）。
type SupplyNonce struct {
	ent.Schema
}

func (SupplyNonce) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("key").MaxLen(64).Comment("api_key（命名空间隔离）"),
		field.String("nonce").MaxLen(64),
		field.Time("expires_at").SchemaType(mysqlTime),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (SupplyNonce) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key", "nonce").Unique(),
		index.Fields("expires_at"),
	}
}

// SupplierProductPrice 对外供货定价（本站→下游差异化定价；区别于 supply_mappings 的上游→本地）。
type SupplierProductPrice struct {
	ent.Schema
}

func (SupplierProductPrice) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (SupplierProductPrice) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("supplier_account_id"),
		field.Uint64("product_id"),
		field.Uint64("sku_id").Default(0).Comment("0=商品级（哨兵，参与唯一索引）"),
		field.Int64("price").Comment("供货价（分）"),
	}
}

func (SupplierProductPrice) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("supplier_account_id", "product_id", "sku_id").Unique(),
	}
}

// DownstreamCallback 下游回调转发重试（交付完成 → POST notify_url；死信可手动重发）。
type DownstreamCallback struct {
	ent.Schema
}

func (DownstreamCallback) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (DownstreamCallback) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("supply_order_id").Unique(),
		field.Uint64("account_id"),
		field.String("downstream_order_no").MaxLen(64),
		field.String("callback_url").MaxLen(500),
		field.String("trace_id").MaxLen(64).Optional(),
		field.Enum("callback_status").Values("pending", "success", "failed").Default("pending"),
		field.Int32("retry_count").Default(0),
		field.Time("last_callback_at").SchemaType(mysqlTime).Optional(),
		field.Text("last_error").Optional(),
	}
}

func (DownstreamCallback) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("callback_status", "retry_count"),
	}
}
