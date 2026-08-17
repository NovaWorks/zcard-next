package license

// 许可证引擎单元测试：签发→校验→篡改/到期/绑定不符 fail-closed + 特性清单。

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestLicenseVerify(t *testing.T) {
	pub, priv := newTestKey(t)
	lic := License{
		InstanceID: "inst-1", Domain: "shop.example.com",
		Features: []string{"analytics", "auto_pricing"},
		IssuedAt: "2026-08-17T00:00:00Z", ExpiresAt: "2027-08-17T00:00:00Z",
	}
	raw, err := Sign(priv, lic)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	got, err := Verify(raw, pub, "inst-1", "shop.example.com", now)
	if err != nil {
		t.Fatalf("合法许可证应通过: %v", err)
	}
	if !got.HasFeature("analytics") || !got.HasFeature("auto_pricing") || got.HasFeature("reseller") {
		t.Fatalf("特性清单错误: %+v", got.Features)
	}
	// 通配 *
	wild, _ := Sign(priv, License{InstanceID: "inst-1", Features: []string{"*"}})
	w, err := Verify(wild, pub, "inst-1", "", now)
	if err != nil || !w.HasFeature("anything") {
		t.Fatal("通配特性应全开")
	}
}

func TestLicenseTamper(t *testing.T) {
	pub, priv := newTestKey(t)
	raw, _ := Sign(priv, License{InstanceID: "inst-1", Features: []string{"analytics"}})
	now := time.Now()

	// 篡改内容（改 JSON 字段）→ 拒绝
	tampered := append([]byte{}, raw...)
	// 构造带篡改的许可：修改 features 后重签不行（无私钥）；直接改签名内容应验签失败
	// ——把 raw 里 "analytics" 换成 "reseller" 长度不一致，用等长替换验证
	for i := 0; i+len("analytics") <= len(raw); i++ {
		if string(raw[i:i+9]) == `analytics` {
			copy(tampered[i:i+9], `reseller`)
			break
		}
	}
	if _, err := Verify(tampered, pub, "inst-1", "", now); err == nil {
		t.Fatal("篡改特性应拒绝")
	}
	// 坏签名
	bad, _ := Sign(priv, License{InstanceID: "inst-1", Features: []string{"x"}})
	bad = append(bad, 'x')
	if _, err := Verify(bad, pub, "inst-1", "", now); err == nil {
		t.Fatal("坏签名应拒绝")
	}
}

func TestLicenseBindAndExpiry(t *testing.T) {
	pub, priv := newTestKey(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	raw, _ := Sign(priv, License{
		InstanceID: "inst-1", Domain: "shop.example.com",
		ExpiresAt: "2026-08-18T00:00:00Z",
	})
	// 实例 ID 不匹配 → 拒绝
	if _, err := Verify(raw, pub, "inst-2", "shop.example.com", now); err == nil {
		t.Fatal("实例 ID 不匹配应拒绝")
	}
	// 域名不匹配 → 拒绝
	if _, err := Verify(raw, pub, "inst-1", "other.com", now); err == nil {
		t.Fatal("域名不匹配应拒绝")
	}
	// 到期 → 拒绝
	later := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	if _, err := Verify(raw, pub, "inst-1", "shop.example.com", later); err == nil {
		t.Fatal("过期许可证应拒绝")
	}
	// 空域名不校验绑定（测试/离线环境）
	if _, err := Verify(raw, pub, "inst-1", "", now); err != nil {
		t.Fatalf("空域名跳过绑定: %v", err)
	}
}
