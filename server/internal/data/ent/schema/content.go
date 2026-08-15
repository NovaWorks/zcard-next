package schema

// 所有权：mods/content（P2-04）/ mods/notify（P2-05）/ mods/dashboard（P3-07）
// 多语言字段用 JSON（*_json），按请求语言回落 zh_CN。

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Banner 横幅/轮播（生效时间窗；link_type=ad 承载第三方广告位）。
type Banner struct {
	ent.Schema
}

func (Banner) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Banner) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(100),
		field.Enum("position").Values("top", "middle", "bottom").Default("top"),
		field.JSON("title_json", map[string]string{}).Optional().Comment("多语言标题"),
		field.String("image").MaxLen(255),
		field.String("mobile_image").MaxLen(255).Optional().Comment("缺省回落 PC 图"),
		field.Enum("link_type").Values("product", "category", "url", "ad").Default("url"),
		field.String("link_value").MaxLen(500).Optional(),
		field.Bool("is_active").Default(true),
		field.Time("start_at").SchemaType(mysqlTime).Optional(),
		field.Time("end_at").SchemaType(mysqlTime).Optional(),
		field.Int32("sort").Default(0),
	}
}

func (Banner) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "position", "is_active"),
	}
}

// PostCategory 文章分类。
type PostCategory struct {
	ent.Schema
}

func (PostCategory) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (PostCategory) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("name").MaxLen(60),
		field.String("slug").MaxLen(80).Unique(),
		field.Int32("sort").Default(0),
	}
}

// Post 文章/公告（type=notice 公告；content 经 sanitize）。
type Post struct {
	ent.Schema
}

func (Post) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}, TenantMixin{}} }

func (Post) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("slug").MaxLen(120),
		field.Enum("type").Values("blog", "notice").Default("blog"),
		field.JSON("title_json", map[string]string{}),
		field.JSON("summary_json", map[string]string{}).Optional(),
		field.Text("content_json").Comment("多语言内容（JSON，sanitize 后）"),
		field.String("thumbnail").MaxLen(255).Optional(),
		field.Uint64("category_id").Optional(),
		field.Bool("is_published").Default(false),
		field.Time("published_at").SchemaType(mysqlTime).Optional(),
	}
}

func (Post) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subsite_id", "slug").Unique(),
		index.Fields("type", "is_published"),
	}
}

// Notification 站内信（用户铃铛）。
type Notification struct {
	ent.Schema
}

func (Notification) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.String("title").MaxLen(200),
		field.Text("content"),
		field.Time("read_at").SchemaType(mysqlTime).Optional(),
		field.String("source_type").MaxLen(40).Optional().Comment("来源（order/ticket/broadcast）"),
		field.Uint64("source_id").Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (Notification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "read_at", "created_at"),
	}
}

// NotifyTemplate 事件驱动模板（事件 × 通道 × 语言；占位符白名单渲染）。
type NotifyTemplate struct {
	ent.Schema
}

func (NotifyTemplate) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (NotifyTemplate) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("event_type").MaxLen(64).Comment("order.paid / ticket.replied ..."),
		field.Enum("channel").Values("email", "sms", "inbox", "telegram"),
		field.String("locale").MaxLen(10).Default("zh_CN"),
		field.String("subject_tpl").MaxLen(255),
		field.Text("body_tpl"),
		field.Bool("enabled").Default(true),
	}
}

func (NotifyTemplate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_type", "channel", "locale").Unique(),
	}
}

// NotificationLog 发送日志（status=skipped 为降级标记：通道未配置不报错雪崩）。
type NotificationLog struct {
	ent.Schema
}

func (NotificationLog) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("event_type").MaxLen(64),
		field.String("biz_type").MaxLen(40).Optional(),
		field.Uint64("biz_id").Optional(),
		field.Enum("channel").Values("email", "sms", "inbox", "telegram"),
		field.String("recipient").MaxLen(255),
		field.String("locale").MaxLen(10).Default("zh_CN"),
		field.String("subject").MaxLen(255).Optional(),
		field.Text("body").Optional(),
		field.Enum("status").Values("pending", "sent", "failed", "skipped").Default("pending"),
		field.Text("error_message").Optional(),
		field.JSON("variables", map[string]any{}).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (NotificationLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "created_at"),
		index.Fields("event_type", "created_at"),
	}
}

// DailyStat 日结指标（报表只扫此表不扫大表；今日实时走 report 层）。
type DailyStat struct {
	ent.Schema
}

func (DailyStat) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("subsite_id").Default(0),
		field.String("stat_date").MaxLen(8).Comment("yyyymmdd"),
		field.String("metric").MaxLen(64).Comment("orders/amount/profit/pay_success_rate..."),
		field.String("dimension_key").MaxLen(128).Default("").Comment("维度（商品ID 等；空=总量）"),
		field.Int64("value").Default(0),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
		field.Time("updated_at").SchemaType(mysqlTime).Default(nowUTC).UpdateDefault(nowUTC),
	}
}

func (DailyStat) Indexes() []ent.Index {
	return []ent.Index{
		// 日结幂等（重跑覆盖同维度行）
		index.Fields("subsite_id", "stat_date", "metric", "dimension_key").Unique(),
	}
}

// ReconciliationJob 对账任务（时间窗内 procurement vs 上游订单）。
type ReconciliationJob struct {
	ent.Schema
}

func (ReconciliationJob) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ReconciliationJob) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("connection_id"),
		field.String("type").MaxLen(40).Default("orders"),
		field.Enum("status").Values("pending", "processing", "done", "failed").Default("pending"),
		field.Time("time_range_start").SchemaType(mysqlTime),
		field.Time("time_range_end").SchemaType(mysqlTime),
		field.Int32("total_count").Default(0),
		field.Int32("matched_count").Default(0),
		field.Int32("mismatched_count").Default(0),
		field.JSON("result_json", map[string]any{}).Optional(),
	}
}

// ReconciliationItem 对账明细（四态：matched/mismatched/local_only/upstream_only）。
type ReconciliationItem struct {
	ent.Schema
}

func (ReconciliationItem) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("job_id"),
		field.Uint64("procurement_order_id").Optional(),
		field.String("local_order_no").MaxLen(64).Optional(),
		field.String("upstream_order_no").MaxLen(80).Optional(),
		field.Enum("status").Values("matched", "mismatched", "local_only", "upstream_only"),
		field.JSON("diff_json", map[string]any{}).Optional().Comment("金额/状态差异"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (ReconciliationItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("job_id", "status"),
	}
}

// V1IDMap 1.x→2.0 迁移 ID 映射（migrate-from-v1 专用，业务不读）。
type V1IDMap struct {
	ent.Schema
}

func (V1IDMap) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("table_name").MaxLen(64).Comment("1.x 表名（列名 table 为保留字，取 table_name）"),
		field.Uint64("old_id"),
		field.Uint64("new_id"),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (V1IDMap) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("table_name", "old_id").Unique(),
	}
}
