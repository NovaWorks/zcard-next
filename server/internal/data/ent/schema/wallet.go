package schema

// 所有权：mods/wallet（M1 充值 / M3 提现；表结构 M0 落地——账务纪律核心）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WalletAccount 账户（冻结分离，超越友商单一 Balance，《数据库架构设计.md》§4.5）。
//
// 不变量：total = available + locked 恒真；一切余额变动必须经 wallet.InTx：
// 行锁账户 → 非负校验 → 更新（乐观锁 version 兜底）→ 写流水（reference 幂等重入）。
type WalletAccount struct {
	ent.Schema
}

func (WalletAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id").Unique(),
		field.String("currency").MaxLen(3).Default("CNY").Comment("基础货币"),
		field.Int64("available").Default(0).Comment("可用余额（分）"),
		field.Int64("locked").Default(0).Comment("冻结余额（分）：提现冻结/佣金冻结期"),
		field.Int32("version").Default(0).Comment("乐观锁（并发扣款/冻结，与行锁双保险）"),
		field.Time("updated_at").SchemaType(mysqlTime).Default(nowUTC).UpdateDefault(nowUTC),
	}
}

// WalletTransaction 流水（只追加不更新；余额永可由流水重算）。
// reference 即幂等键（铁律 §5.6）：order_pay:<orderID> / recharge:<payID> / commission:<id> / adjust:<auditID>。
type WalletTransaction struct {
	ent.Schema
}

func (WalletTransaction) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		// string(8)：in/out（取值约束由 wallet 模块账务纪律保证；不用 ent.Enum
		// 是因枚举值 in 会生成 DirectionIn 常量、与 IN 查询谓词撞名）
		field.String("direction").MaxLen(8).Comment("方向：in | out"),
		field.String("type").MaxLen(40).Comment("order_pay/order_refund/recharge/commission/adjust..."),
		field.Int64("amount").Comment("金额（分，正数）"),
		field.Int64("balance_before").Comment("变动前快照"),
		field.Int64("balance_after").Comment("变动后快照"),
		field.String("currency").MaxLen(3).Default("CNY"),
		field.String("reference").MaxLen(120).Unique().Comment("幂等键（重复入账直接返回成功）"),
		field.Uint64("order_id").Optional(),
		field.Uint64("operator_id").Optional().Comment("手动调账操作管理员"),
		field.String("remark").MaxLen(255).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (WalletTransaction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
		index.Fields("type", "created_at"),
	}
}
