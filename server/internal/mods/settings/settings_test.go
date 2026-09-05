package settings

// P0-04 验收测试：SECRET 泄漏快照（双保险断言）、键校验、白名单、默认值回落。

import (
	"testing"
)

// TestPublicKeysLeakSnapshot SECRET 键绝不出现在任何分组的 PublicKeys 中——
// 白名单与敏感清单交集必须为空（快照式断言，新增分组自动纳入）。
func TestPublicKeysLeakSnapshot(t *testing.T) {
	for _, gname := range GroupsSorted() {
		g, _ := Group(gname)
		for k := range g.PublicKeys {
			if g.SecretKeys[k] {
				t.Errorf("SECRET 键 %s.%s 同时出现在 PublicKeys——前台将泄漏", gname, k)
			}
		}
	}
}

// TestSecretKeyCatalog SECRET 清单快照（notify 三键 + license 签发私钥；新增需同步更新测试）。
func TestSecretKeyCatalog(t *testing.T) {
	want := map[string]bool{
		"notify.smtp_password":     true,
		"notify.sms_key":           true,
		"notify.sms_secret":        true,
		"license.purchase_privkey": true,
	}
	got := map[string]bool{}
	for _, gname := range GroupsSorted() {
		g, _ := Group(gname)
		for k := range g.SecretKeys {
			got[gname+"."+k] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("SECRET 清单漂移：got %v want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("SECRET 键缺失：%s", k)
		}
	}
}

// TestValidateKey 键校验（未知组/未知键拒绝）。
func TestValidateKey(t *testing.T) {
	if err := ValidateKey("site", "name"); err != nil {
		t.Errorf("合法键被拒：%v", err)
	}
	if err := ValidateKey("nope", "name"); err == nil {
		t.Error("未知分组应拒绝")
	}
	if err := ValidateKey("site", "hack_key"); err == nil {
		t.Error("未知键应拒绝")
	}
}

// TestGroupCatalog 分组目录快照（18 组；P0-04 验收「全部 12+ 分组」；P3-08 加 license；客服加 service；供货充值加 supplier_recharge；在线更新加 system）。
func TestGroupCatalog(t *testing.T) {
	got := GroupsSorted()
	want := []string{"affiliate", "footer", "i18n", "license", "notify", "ops", "points", "promo", "recharge", "security", "service", "site", "supplier_recharge", "supply", "system", "template", "trade", "withdraw"}
	if len(got) != len(want) {
		t.Fatalf("分组数漂移：got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("分组顺序漂移：got[%d]=%s want[%d]=%s", i, got[i], i, want[i])
		}
	}
}

// TestIsPublic 白名单判定。
func TestIsPublic(t *testing.T) {
	if !IsPublic("site", "name") {
		t.Error("site.name 应为公开键")
	}
	if IsPublic("notify", "smtp_password") {
		t.Error("smtp_password 不得公开")
	}
	if !IsPublic("security", "register_enabled") || !IsPublic("security", "register_method") {
		t.Error("security 注册键应公开（注册页动态表单消费）")
	}
	if !IsPublic("security", "captcha_login") {
		t.Error("captcha_login 应公开（前端条件渲染）")
	}
}

// TestDefaultJSON 默认值序列化。
func TestDefaultJSON(t *testing.T) {
	g, _ := Group("trade")
	v, ok := g.DefaultJSON("order_ttl_minutes")
	if !ok || v != "30" {
		t.Errorf("默认值错误：%q %v", v, ok)
	}
}
