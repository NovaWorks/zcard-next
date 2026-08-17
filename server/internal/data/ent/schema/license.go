package schema

// 所有权：mods/license（P3-08 在线购买：专业套餐订阅购买单）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LicenseOrder 专业套餐购买单（发行侧在线签发记录；钱包扣款同事务落账）。
type LicenseOrder struct {
	ent.Schema
}

func (LicenseOrder) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (LicenseOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Enum("plan").Values("monthly", "yearly").Comment("套餐档位（3U/月、30U/年）"),
		field.Int64("amount").Comment("实付（分）"),
		field.Enum("status").Values("pending", "success", "canceled").Default("pending"),
		field.Uint64("payer_user_id").Comment("购买人（发行侧站点用户；钱包扣款对象）"),
		field.String("instance_id").MaxLen(64).Comment("目标实例 ID（许可证绑定；空=本实例）"),
		field.String("domain").MaxLen(255).Optional().Comment("目标域名（许可证绑定；空=不限）"),
		field.JSON("features", []string{}).Optional().Comment("授权特性快照"),
		field.Time("expires_at").SchemaType(mysqlTime).Comment("订阅到期（许可证签发口径）"),
		field.Text("license_file").Optional().Comment("签发的许可证文件（购买人下载后安装）"),
		field.Time("paid_at").SchemaType(mysqlTime).Optional(),
	}
}

func (LicenseOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("payer_user_id"),
		index.Fields("instance_id"),
	}
}
