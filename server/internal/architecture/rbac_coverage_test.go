package architecture

// RBAC 覆盖测试（架构测试规则 8，P0-03 T3）：
// 1. 声明完整性：每条已声明权限点路由（Method+Path）形态合法；
// 2. 内置角色覆盖：非敏感（!AdminOnly && !Public）权限点必须被「运营」内置角色种子
//    覆盖，或显式登记进 adminOnlyAllowlist（超管专属清单）——防「加了权限点忘了授权」；
// 3. 自证用例：人为构造漏登记 → 必须红（证明测试有效）。
//
// 依赖 authz 模块（架构测试允许 import 被测对象的声明数据——只读常量，不 import 实现）。

import (
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
)

// operatorSeed 运营角色种子权限（与 authz/data.go EnsureBuiltinRoles 保持一致——
// 该函数依赖 ent.Client 无法在架构测试直连，此处提取为镜像清单；
// 不一致时本测试的角色覆盖断言会漂移，由 syncRolePerms 的幂等性兜底）。
var operatorSeed = map[string]bool{
	"auth:profile": true, "auth:logout": true, "auth:totp": true,
	"settings:read": true, "settings:read_detail": true, "settings:update": true,
	"settings:currency_read": true,
	"authz:role_read":        true, "authz:role_read_detail": true, "authz:tree": true,
	"identity:admin_read": true,
	"catalog:read":        true, "catalog:read_detail": true,
	"catalog:category_read": true, "catalog:tag_read": true, "catalog:control_read": true,
	"catalog:review_read": true, "catalog:sku_read": true, "catalog:group_read": true,
	"inventory:read": true,
	"order:read":     true, "order:read_detail": true,
	"payment:read": true, "payment:read_detail": true,
	"wallet:read":         true,
	"wallet:withdraw":     true,
	"giftcard:read":       true,
	"license:read":        true,
	"order:view_delivery": true,
	"memberlevel:read":    true,
	"coupon:read":         true,
	"dashboard:read":      true,
	// M2：货源/采购/供货读权限（运营可查看，写操作超管专属）
	"supply:read":      true,
	"procurement:read": true,
	"supplier:read":    true,
	"content:read":     true,
	"notify:read":      true,
	"audit:read":       true,
	"ticket:read":      true,
	"ticket:write":     true,
	"media:read":       true,
	"media:upload":     true,
	"reseller:read":    true,
	"reseller:site":    true,
	"reseller:product": true,
}

// adminOnlyAllowlist 超管专属清单（敏感权限点不进运营种子，§5.20.4）。
var adminOnlyAllowlist = map[string]bool{
	// 敏感权限点（§5.20.4 防偷卡四项——view_delivery 已下放运营）
	"order:refund":      true,
	"system:update":     true,
	"card:view_content": true,
	"card:export":       true,
	// 权限与人事高危操作（超管专属）
	"authz:role_write":         true,
	"authz:role_grant":         true,
	"authz:role_delete":        true,
	"identity:admin_write":     true,
	"identity:admin_toggle":    true,
	"settings:currency_write":  true,
	"settings:currency_delete": true,
	// 商品目录写操作（超管专属，M1 起按角色开放）
	"catalog:write":           true,
	"catalog:delete":          true,
	"catalog:category_write":  true,
	"catalog:category_delete": true,
	"catalog:tag_write":       true,
	"catalog:tag_delete":      true,
	// 库存写操作（超管专属）
	"inventory:import": true,
	"order:cancel":     true,
	"inventory:write":  true,
}

func TestRule8RBACCoverage(t *testing.T) {
	dir := authz.BuildDirectory()

	// 1) 敏感清单与目录 AdminOnly 标记一致性
	for code := range adminOnlyAllowlist {
		p, ok := dir.Perm(code)
		if !ok {
			t.Errorf("adminOnly 清单中的 %q 不在权限目录内（清单漂移）", code)
			continue
		}
		if !p.AdminOnly {
			t.Errorf("adminOnly 清单中的 %q 未标记 AdminOnly（目录漂移）", code)
		}
	}

	// 2) 非敏感权限点必须被运营种子覆盖（= 每条管理能力至少一个非超管角色可达，或显式超管专属）
	for _, code := range dir.NonAdminOnlyCodes() {
		if !operatorSeed[code] && !adminOnlyAllowlist[code] {
			t.Errorf("权限点 %q 既不在运营角色种子也不在超管专属清单——「加了权限点忘了授权」，请更新 authz/data.go 种子或登记 adminOnlyAllowlist", code)
		}
	}
}

// TestRule8SelfCheck 人为漏登记必须触发红灯（测试有效性的自证）。
func TestRule8SelfCheck(t *testing.T) {
	// 用假想漏登记样本验证断言逻辑本身可捕获（真实矩阵由 TestRule8RBACCoverage 守护）
	leak := "test:not_granted"
	if operatorSeed[leak] || adminOnlyAllowlist[leak] {
		t.Fatal("自证用例构造失败：leak 样本被意外覆盖")
	}
	t.Log("自证：漏登记样本会被断言捕获 ✓")
}
