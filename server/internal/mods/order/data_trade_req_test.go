package order

// 交易设置下单校验测试：查询密码强制 + 游客联系方式（settings.trade）。

import (
	"context"
	"testing"
)

// TestTradeRequirements 交易设置校验：
// 1) 无查询密码 → QUERY_PASSWORD_REQUIRED；2) 游客无联系方式 → CONTACT_REQUIRED；
// 3) 游客带合法邮箱 → 通过；4) 登录用户无联系方式 → 通过（账户可追溯）。
func TestTradeRequirements(t *testing.T) {
	d, uc, _ := newIdemEnv(t)
	_ = d
	ctx := context.Background()

	// 建上架商品（复用 idem 环境的商品 1）

	// 1) 无查询密码
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		UserID: 1, Items: []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err == nil || !contains2(err.Error(), "QUERY_PASSWORD_REQUIRED") {
		t.Fatalf("无查询密码应拒绝: %v", err)
	}

	// 2) 游客无联系方式（默认 any 模式）
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		QueryPassword: "test1234",
		Items:         []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err == nil || !contains2(err.Error(), "CONTACT_REQUIRED") {
		t.Fatalf("游客无联系方式应拒绝: %v", err)
	}

	// 3) 游客带邮箱 → 通过（下单成功即通过校验）
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		QueryPassword: "test1234",
		Contact:       "guest@example.com",
		Items:         []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatalf("游客带邮箱应通过: %v", err)
	}

	// 4) 游客带手机号 → 通过（any 模式）
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		QueryPassword: "test1234",
		Contact:       "13800138000",
		Items:         []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatalf("游客带手机号应通过: %v", err)
	}

	// 5) 登录用户无联系方式 → 通过
	if _, err := uc.CreateOrder(ctx, CreateOrderInput{
		QueryPassword: "test1234",
		UserID:        1,
		Items:         []OrderItemInput{{ProductID: 1, Quantity: 1}},
	}); err != nil {
		t.Fatalf("登录用户应通过: %v", err)
	}
}

// TestContactMatchesMode 联系方式格式校验单元。
func TestContactMatchesMode(t *testing.T) {
	cases := []struct {
		contact, mode string
		want          bool
	}{
		{"a@b.com", "email", true},
		{"a@b.com", "phone", false},
		{"13800138000", "phone", true},
		{"13800138000", "qq", true}, // 纯数字 11 位在 QQ 长度区间
		{"12345", "qq", true},
		{"abc", "qq", false},
		{"a@b.com", "any", true},
		{"13800138000", "any", true},
		{"12345", "any", true},
		{"hello", "any", false},
	}
	for _, c := range cases {
		if got := contactMatchesMode(c.contact, c.mode); got != c.want {
			t.Errorf("contactMatchesMode(%q, %q) = %v, want %v", c.contact, c.mode, got, c.want)
		}
	}
}
