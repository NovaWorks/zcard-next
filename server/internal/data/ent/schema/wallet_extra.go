package schema

// 所有权：mods/wallet（P1-05 充值/积分/提现/礼品卡；账务纪律与 wallet 主表同构）

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PointAccount 积分账户（与 WalletAccount 同构纪律：行锁+非负+流水重算）。
type PointAccount struct {
	ent.Schema
}

func (PointAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id").Unique(),
		field.Int64("balance").Default(0).Comment("积分余额（非负）"),
		field.Int32("version").Default(0),
		field.Time("updated_at").SchemaType(mysqlTime).Default(nowUTC).UpdateDefault(nowUTC),
	}
}

// PointTransaction 积分流水（reference 幂等键同钱包规范）。
type PointTransaction struct {
	ent.Schema
}

func (PointTransaction) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		// string(8)：in/out（ent.Enum 的 in 值会生成 DirectionIn 常量与 IN 谓词撞名，同 wallet 方案）
		field.String("direction").MaxLen(8).Comment("方向：in | out"),
		field.String("type").MaxLen(40).Comment("earn_consume/earn_recharge/redeem/adjust..."),
		field.Int64("amount").Comment("积分（正数）"),
		field.Int64("balance_before"),
		field.Int64("balance_after"),
		field.String("reference").MaxLen(120).Unique().Comment("幂等键（points:<orderID> 等）"),
		field.Uint64("order_id").Optional(),
		field.String("remark").MaxLen(255).Optional(),
		field.Time("created_at").SchemaType(mysqlTime).Immutable().Default(nowUTC),
	}
}

func (PointTransaction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
	}
}

// RechargeOrder 充值单（复用支付管线；预设赠送充 X 送 Y 余额 Z 积分）。
type RechargeOrder struct {
	ent.Schema
}

func (RechargeOrder) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (RechargeOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.Int64("amount").Comment("充值金额（分）"),
		field.Int64("gift_amount").Default(0).Comment("赠送余额（分）"),
		field.Int32("gift_points").Default(0).Comment("赠送积分"),
		field.Enum("target").Values("balance", "supply").Default("balance").Comment("入账方向（supply=供货商预存，M2）"),
		field.Uint64("supplier_account_id").Optional().Comment("target=supply 时的入账目标对接账户"),
		field.Enum("status").Values("pending", "success", "failed", "expired").Default("pending"),
		field.Uint64("payment_id").Optional(),
		field.Time("paid_at").SchemaType(mysqlTime).Optional(),
	}
}

func (RechargeOrder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "created_at"),
		index.Fields("status"),
	}
}

// Withdrawal 提现（申请即锁定 available→locked；审核通过打款/驳回解锁）。
type Withdrawal struct {
	ent.Schema
}

func (Withdrawal) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Withdrawal) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("user_id"),
		field.Int64("amount").Comment("提现金额（分）"),
		field.Int64("fee").Default(0).Comment("手续费（分）"),
		field.JSON("method", map[string]any{}).Comment("收款方式快照（渠道/账号/姓名）"),
		field.Enum("status").Values("pending", "approved", "paid", "rejected").Default("pending"),
		field.Uint64("reviewed_by").Optional(),
		field.String("reject_reason").MaxLen(255).Optional(),
		field.Time("paid_at").SchemaType(mysqlTime).Optional(),
		field.Time("reviewed_at").SchemaType(mysqlTime).Optional(),
		// 打款回执（交易流水号/凭证备注；打款时填写，客户记录展示）
		field.String("receipt").MaxLen(255).Optional(),
	}
}

func (Withdrawal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "status"),
		index.Fields("status", "created_at"),
	}
}

// GiftcardBatch 礼品卡批次。
type GiftcardBatch struct {
	ent.Schema
}

func (GiftcardBatch) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (GiftcardBatch) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.String("batch_no").MaxLen(40).Unique(),
		field.String("name").MaxLen(100),
		field.Int64("amount").Comment("面额（分）"),
		field.Int32("quantity").Comment("发行数量"),
		field.Uint64("operator_id").Optional(),
	}
}

// Giftcard 礼品卡（code 同卡密纪律：AES-256-GCM 密文 + keyed hash 去重，永不落明文）。
type Giftcard struct {
	ent.Schema
}

func (Giftcard) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Giftcard) Fields() []ent.Field {
	return []ent.Field{
		field.Uint64("id"),
		field.Uint64("batch_id"),
		field.Bytes("code").Comment("AES-256-GCM 密文（ZCARD_CARD_KEY）"),
		field.String("code_hash").MaxLen(64).Unique().Comment("keyed hash（兑换检索，防爆破限流配套）"),
		field.Int64("amount").Comment("面额（分）"),
		field.String("currency").MaxLen(3).Default("CNY"),
		field.Enum("status").Values("unused", "used", "disabled").Default("unused"),
		field.Uint64("used_by").Optional().Comment("兑换用户"),
		field.Time("used_at").SchemaType(mysqlTime).Optional(),
		field.Time("expires_at").SchemaType(mysqlTime).Optional(),
	}
}

func (Giftcard) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("batch_id"),
		index.Fields("status"),
	}
}
