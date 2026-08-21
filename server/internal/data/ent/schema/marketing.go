package schema

// 所有权：mods/memberlevel（P3-01）/ mods/coupon（P3-02）/ mods/affiliate（P3-03）
//          / mods/reseller（P3-04）/ mods/ticket（P3-05）/ mods/media（P3-06）

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MemberLevel 会员等级（升级条件充值/消费双条件 AND|OR；只升不降可配）。
type MemberLevel struct {
	ent.Schema
}

func (MemberLevel) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (MemberLevel) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60),
		field.String("logo").MaxLen(255).Optional(),
		field.String("badge_color").MaxLen(20).Optional(),
		field.Enum("threshold_type").Values("recharge", "consume", "both_and", "both_or").Default("recharge"),
		field.Int64("threshold_recharge").Default(0).Comment("累计充值阈值（分；countAsRecharge 口径防刷）"),
		field.Int64("threshold_consume").Default(0).Comment("累计消费阈值（分）"),
		field.Int32("discount").Default(0).Comment("等级折扣（万分比）"),
		field.JSON("points_rule", map[string]any{}).Optional().Comment("积分产生规则（消费 X 元产 Y 分）"),
		field.Int32("sort").Default(0),
		field.Bool("enabled").Default(true),
	}
}

// Coupon 优惠券（一行一码：批量生成 N 行同前缀唯一码；1.x 模式）。
type Coupon struct {
	ent.Schema
}

func (Coupon) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Coupon) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("batch_id").MaxLen(40).Default("").Comment("批次标识（按批作废）"),
		field.String("name").MaxLen(100),
		field.Enum("type").Values("fixed", "percent"),
		field.Int64("value").Comment("fixed=分；percent=万分比"),
		field.String("code").MaxLen(40).Unique(),
		field.JSON("scope", map[string]any{}).Optional().Comment("全场/商品IDs/分类IDs/等级IDs"),
		field.Time("expire_at").SchemaType(mysqlTime).Optional(),
		field.Int32("per_user_limit").Default(1).Comment("单码次数（多次码放开）"),
		field.Enum("status").Values("unused", "used", "disabled").Default("unused"),
		field.Uint64("user_id").Optional().Comment("领取人（兑换码/后台赠送回填）"),
		field.Time("used_at").SchemaType(mysqlTime).Optional(),
		field.Uint64("used_order_id").Optional(),
	}
}

func (Coupon) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("batch_id"),
		index.Fields("user_id", "status"),
	}
}

// FlashSale 限时秒杀（限量与卡密同一把锁防超卖；窗口判定无状态）。
type FlashSale struct {
	ent.Schema
}

func (FlashSale) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (FlashSale) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("product_id"),
		field.Uint64("sku_id").Default(0).Comment("0=商品级（哨兵）"),
		field.Int64("flash_price").Comment("秒杀价（分）"),
		field.Time("start_at").SchemaType(mysqlTime),
		field.Time("end_at").SchemaType(mysqlTime),
		field.Int32("limit_qty").Comment("总量（与库存同锁扣减）"),
		field.Int32("sold_qty").Default(0).Comment("已售（CAS 扣减）"),
		field.Int32("per_user_limit").Default(1),
	}
}

func (FlashSale) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("product_id", "sku_id"),
		index.Fields("end_at"),
	}
}

// Promotion 通用促销（门槛满减/折扣/会员特价；范围关联商品/分类）。
type Promotion struct {
	ent.Schema
}

func (Promotion) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Promotion) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(100),
		field.JSON("scope", map[string]any{}),
		field.Enum("type").Values("fixed", "percent", "special_price"),
		field.Int64("threshold").Default(0).Comment("满 X（分）"),
		field.Int64("discount").Default(0).Comment("fixed=分；percent=万分比"),
		field.Int64("special_price").Default(0).Comment("special_price=分"),
		field.Time("start_at").SchemaType(mysqlTime),
		field.Time("end_at").SchemaType(mysqlTime),
		field.Bool("enabled").Default(true),
	}
}

// AffiliateCommission 三级佣金（UNIQUE(order_id, tier) 幂等；冻结在自身状态机，不占 wallet.locked）。
type AffiliateCommission struct {
	ent.Schema
}

func (AffiliateCommission) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (AffiliateCommission) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("order_id"),
		field.Uint64("buyer_id"),
		field.Uint64("referrer_id").Comment("收佣人"),
		field.Int8("tier").Comment("层级 1/2/3"),
		field.Float("rate").
			SchemaType(map[string]string{dialect.MySQL: "decimal(20,8)", dialect.Postgres: "numeric(20,8)", dialect.SQLite: "real"}).
			Comment("费率快照（万分比）"),
		field.Int64("base_amount").Comment("计算基数（分：订单金额或毛利，settings 口径）"),
		field.Int64("amount").Comment("佣金（分）"),
		field.Enum("status").Values("pending_confirm", "available", "withdrawn", "reversed").Default("pending_confirm"),
		field.Time("available_at").SchemaType(mysqlTime).Optional().Comment("冻结到期"),
	}
}

func (AffiliateCommission) Indexes() []ent.Index {
	return []ent.Index{
		// 幂等防重发佣
		index.Fields("order_id", "tier").Unique(),
		index.Fields("referrer_id", "status"),
		index.Fields("available_at"),
	}
}

// ResellerProfile 分站站长资格（申请/审核；加价率上下限管控；等级权限位）。
type ResellerProfile struct {
	ent.Schema
}

func (ResellerProfile) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResellerProfile) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id").Unique(),
		field.Enum("status").Values("applying", "approved", "rejected").Default("applying"),
		field.Text("apply_reason").Optional(),
		field.String("reject_reason").MaxLen(255).Optional(),
		field.Int8("level").Default(1).Comment("分站等级（域名/供货/自助上架权限位）"),
		field.Float("default_markup_percent").
			SchemaType(map[string]string{dialect.MySQL: "decimal(20,8)", dialect.Postgres: "numeric(20,8)", dialect.SQLite: "real"}).
			Default(0).Comment("默认加价率"),
		field.Float("max_markup_percent").
			SchemaType(map[string]string{dialect.MySQL: "decimal(20,8)", dialect.Postgres: "numeric(20,8)", dialect.SQLite: "real"}).
			Default(100).Comment("加价率上限（管控）"),
		field.Int32("confirm_days").Default(7).Comment("利润冻结天数（库文档 settlement_status 的落地语义）"),
		field.Uint64("reviewed_by").Optional(),
		field.Time("reviewed_at").SchemaType(mysqlTime).Optional(),
	}
}

// ResellerSite 分站域名 + 白标（DNS TXT / HTTP well-known 双验证，钉死公网 IP 防 rebinding）。
type ResellerSite struct {
	ent.Schema
}

func (ResellerSite) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResellerSite) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("profile_id"),
		field.String("domain").MaxLen(255).Unique(),
		field.Enum("type").Values("main", "custom").Default("custom"),
		field.String("verification_token").MaxLen(64),
		field.Enum("verification_status").Values("pending", "verified", "failed").Default("pending"),
		field.Bool("is_primary").Default(false),
		field.String("site_name").MaxLen(100).Optional(),
		field.String("logo").MaxLen(255).Optional(),
		field.String("favicon").MaxLen(255).Optional(),
		field.JSON("announcement_json", map[string]any{}).Optional(),
		field.JSON("support_json", map[string]any{}).Optional().Comment("客服配置"),
		field.Enum("status").Values("active", "disabled").Default("active"),
	}
}

// ResellerPricing 分站定价（4 模式；优先级 SKU > 商品 > 分站默认；分站价不低于基础价）。
type ResellerPricing struct {
	ent.Schema
}

func (ResellerPricing) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResellerPricing) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("subsite_id"),
		field.Uint64("product_id"),
		field.Uint64("sku_id").Default(0).Comment("0=商品级（哨兵，参与唯一索引）"),
		field.Enum("mode").Values("inherit", "markup_percent", "fixed_markup", "fixed_price").Default("inherit"),
		field.Int64("value").Default(0).Comment("markup_percent=万分比；fixed_*=分"),
	}
}

func (ResellerPricing) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "product_id", "sku_id").Unique(),
	}
}

// ResellerLedgerEntry 分站利润账本（幂等键 order_profit:<orderID>；冻结在自身状态机）。
type ResellerLedgerEntry struct {
	ent.Schema
}

func (ResellerLedgerEntry) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("subsite_id"),
		field.Uint64("order_id").Optional(),
		field.Enum("type").Values("order_profit", "refund_deduct", "withdraw_lock", "withdraw_paid", "manual_adjust"),
		field.Int64("amount").Comment("有符号（分）"),
		field.String("currency").MaxLen(3).Default("CNY"),
		field.Enum("status").Values("pending", "available", "locked", "withdrawn").Default("pending"),
		field.Time("available_at").SchemaType(mysqlTime).Optional(),
		field.String("idempotency_key").MaxLen(120).Unique(),
		field.JSON("metadata_json", map[string]any{}).Optional(),
		field.String("remark").MaxLen(255).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (ResellerLedgerEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "status"),
		index.Fields("available_at"),
	}
}

// ResellerBalanceAccount 分站余额缓存（可由流水重算；last_entry_id 水位）。
type ResellerBalanceAccount struct {
	ent.Schema
}

func (ResellerBalanceAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("subsite_id").Unique(),
		field.String("currency").MaxLen(3).Default("CNY"),
		field.Int64("available").Default(0),
		field.Int64("locked").Default(0),
		field.Int64("negative").Default(0).Comment("负债（退款逆向不足扣）"),
		field.Uint64("last_entry_id").Default(0).Comment("账本水位（增量重算）"),
		field.Time("updated_at").SchemaType(mysqlTime).Default(nowUTC).UpdateDefault(nowUTC),
	}
}

// ResellerRelatedAccount 分站关联账户（自买风控数据源）。
type ResellerRelatedAccount struct {
	ent.Schema
}

func (ResellerRelatedAccount) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ResellerRelatedAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("reseller_id").Comment("分站 profile user_id"),
		field.Uint64("user_id").Comment("关联用户"),
		field.String("relation_type").MaxLen(40).Comment("family/same_device/sub_account..."),
		field.String("source").MaxLen(120).Optional().Comment("判定依据"),
		field.Enum("status").Values("active", "ignored").Default("active"),
	}
}

func (ResellerRelatedAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("reseller_id", "user_id", "relation_type").Unique(),
	}
}

// Ticket 工单（付费加急 urgent_paid 置顶高亮；SLA 预留）。
type Ticket struct {
	ent.Schema
}

func (Ticket) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Ticket) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("ticket_no").MaxLen(40).Unique().Comment("雪花 T 前缀，不可枚举"),
		field.Uint64("user_id").Optional().Comment("NULL=游客"),
		field.String("guest_contact").MaxLen(150).Optional(),
		field.Enum("type").Values("presale", "aftersale", "withdraw"),
		field.Enum("priority").Values("low", "normal", "high", "urgent_paid").Default("normal"),
		field.Uint64("order_id").Optional(),
		field.Uint64("product_id").Optional(),
		field.Enum("status").Values("open", "processing", "resolved", "closed").Default("open"),
		field.Time("first_reply_at").SchemaType(mysqlTime).Optional(),
		field.Time("sla_due_at").SchemaType(mysqlTime).Optional().Comment("SLA 预留（M4 告警）"),
		field.Int8("satisfaction").Optional().Comment("1-5 评价"),
	}
}

func (Ticket) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "priority", "created_at"),
		index.Fields("user_id", "created_at"),
	}
}

// TicketMessage 工单消息（会话式；is_internal 内部备注用户侧双过滤）。
type TicketMessage struct {
	ent.Schema
}

func (TicketMessage) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("ticket_id"),
		field.Enum("sender_type").Values("user", "admin", "system"),
		field.Uint64("sender_id").Optional(),
		field.Text("content").Comment("sanitize 后"),
		field.JSON("attachments", []uint64{}).Optional().Comment("media ids"),
		field.Bool("is_internal").Default(false).Comment("内部备注（用户不可见）"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (TicketMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id", "created_at"),
	}
}

// MediaCategory 素材分类树。
type MediaCategory struct {
	ent.Schema
}

func (MediaCategory) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (MediaCategory) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("parent_id").Optional(),
		field.String("name").MaxLen(60),
		field.Int32("sort").Default(0),
	}
}

// Media 素材（类型白名单 + 重编码防图片马；ref_count 引用计数防误删）。
type Media struct {
	ent.Schema
}

func (Media) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Media) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("category_id").Optional(),
		field.String("path").MaxLen(500).Comment("相对存储根（uploads/YYYY/MM/随机名）"),
		field.String("name").MaxLen(255),
		field.String("mime").MaxLen(60),
		field.Int64("size"),
		field.Int32("width").Optional(),
		field.Int32("height").Optional(),
		field.String("sha256").MaxLen(64).Optional().Comment("秒传去重"),
		field.Enum("storage").Values("local", "s3").Default("local"),
		field.Int32("ref_count").Default(0).Comment("引用计数（删除需 confirm）"),
		field.Uint64("uploader_id").Optional(),
	}
}

func (Media) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("category_id"),
		index.Fields("sha256"),
	}
}
