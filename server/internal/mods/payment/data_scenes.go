package payment

import (
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

const (
	scenePurchase       = "purchase"
	sceneMemberRecharge = "member_recharge"
	sceneSupplyRecharge = "supply_recharge"
)

// Usage switches are optional on writes so older clients cannot reset a policy.
// Omitted method switches inherit the channel policy (legacy compatibility).
type ChannelUsage struct {
	AllowPurchase       *bool `json:"allow_purchase,omitempty"`
	AllowMemberRecharge *bool `json:"allow_member_recharge,omitempty"`
	AllowSupplyRecharge *bool `json:"allow_supply_recharge,omitempty"`
}

func (u ChannelUsage) allows(scene string) bool {
	var flag *bool
	switch scene {
	case scenePurchase:
		flag = u.AllowPurchase
	case sceneMemberRecharge:
		flag = u.AllowMemberRecharge
	case sceneSupplyRecharge:
		flag = u.AllowSupplyRecharge
	default:
		return false
	}
	return flag == nil || *flag
}

func channelAllows(ch *ent.PaymentChannel, scene string) bool {
	if !ch.Enabled {
		return false
	}
	if ch.Driver == "wallet" && scene != scenePurchase {
		return false
	}
	return (ChannelUsage{&ch.AllowPurchase, &ch.AllowMemberRecharge, &ch.AllowSupplyRecharge}).allows(scene)
}

func checkPaymentUsage(ch *ent.PaymentChannel, method, scene string) error {
	if !channelAllows(ch, scene) {
		return fmt.Errorf("payment.SCENE_DISABLED: 该支付渠道未开放此用途，请更换支付方式")
	}
	if ms := parseMethods(ch); len(ms) > 0 {
		for _, m := range ms {
			if m.Code == method && m.Enabled {
				if !m.ChannelUsage.allows(scene) {
					return fmt.Errorf("payment.SCENE_DISABLED: 该支付方式未开放此用途，请更换支付方式")
				}
				return nil
			}
		}
		return fmt.Errorf("payment.METHOD_INVALID: 请选择该渠道支持的支付方式")
	}
	return nil
}
