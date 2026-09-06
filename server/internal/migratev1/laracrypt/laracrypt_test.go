package laracrypt

// golden vector 测试：testdata/v1_crypto_fixtures.json 由 1.x 仓库
// docs/重构/generate-v1-crypto-fixtures.php（真实 Illuminate Encrypter）生成。
// 重新生成命令见该脚本头部注释；密钥为确定性测试派生，禁止用于生产。

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// hexAA "a"*32 字节的 hex 编码（64 字符）。
var hexAA = hex.EncodeToString([]byte(strings.Repeat("a", 32)))

type fixtureVector struct {
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Payload     string  `json:"payload"`
	Expect      *string `json:"expect"`
	ExpectError string  `json:"expect_error"`
}

type fixtures struct {
	Cipher  string          `json:"cipher"`
	AppKey  string          `json:"app_key"`
	CardKey string          `json:"card_key"`
	Vectors []fixtureVector `json:"vectors"`
}

func loadFixtures(t *testing.T) *fixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/v1_crypto_fixtures.json")
	if err != nil {
		t.Fatalf("读取 fixtures 失败（先运行 PHP 生成脚本）: %v", err)
	}
	f := &fixtures{}
	if err := json.Unmarshal(raw, f); err != nil {
		t.Fatalf("解析 fixtures 失败: %v", err)
	}
	return f
}

func TestGoldenVectors(t *testing.T) {
	f := loadFixtures(t)
	appKey, err := ParseKey(f.AppKey)
	if err != nil {
		t.Fatalf("解析 APP_KEY 失败: %v", err)
	}
	cardKey, err := ParseKey(f.CardKey)
	if err != nil {
		t.Fatalf("解析 CARD_KEY 失败: %v", err)
	}

	for _, v := range f.Vectors {
		t.Run(v.Name+"/"+v.Kind, func(t *testing.T) {
			switch v.Kind {
			case "crypt_string", "crypt_serialized":
				c, err := New(appKey)
				if err != nil {
					t.Fatal(err)
				}
				got, err := c.OpenString(v.Payload)
				assertVector(t, v, got, err)
			case "card", "plaintext_card":
				got, wasEnc, err := OpenCard(v.Payload, cardKey, true)
				if v.Kind == "plaintext_card" && wasEnc {
					t.Fatalf("明文卡密被误判为密文形态")
				}
				assertVector(t, v, got, err)
			case "payment_config":
				got, _, err := OpenPaymentConfig(v.Payload, appKey, true)
				assertVector(t, v, got, err)
			case "setting_secret":
				got, _, err := OpenSettingSecret(v.Payload, appKey, true)
				assertVector(t, v, got, err)
			case "setting_card_key":
				key, _, err := CardKeyFromSetting(v.Payload, appKey, true)
				if err != nil {
					t.Fatalf("解析卡密钥匙失败: %v", err)
				}
				if string(key) != string(cardKey) {
					t.Fatalf("钥匙解析结果与期望不一致: %x ≠ %x", key, cardKey)
				}
			default:
				t.Fatalf("未知向量类型 %q", v.Kind)
			}
		})
	}
}

func assertVector(t *testing.T, v fixtureVector, got string, err error) {
	t.Helper()
	switch {
	case v.ExpectError == "mac":
		if !errors.Is(err, ErrBadMac) {
			t.Fatalf("期望 MAC 拒收，实际 err=%v got=%q", err, got)
		}
	case v.ExpectError != "":
		if err == nil {
			t.Fatalf("期望错误 %q，实际成功 got=%q", v.ExpectError, got)
		}
	case err != nil:
		t.Fatalf("解密失败: %v", err)
	case got != *v.Expect:
		t.Fatalf("明文不一致:\n got=%q\nwant=%q", got, *v.Expect)
	}
}

func TestParseKey(t *testing.T) {
	if _, err := ParseKey(""); err == nil {
		t.Fatal("空密钥应报错")
	}
	if _, err := ParseKey("base64:###"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if _, err := ParseKey("base64:c2hvcnQ="); err == nil {
		t.Fatal("非 32 字节 base64 应报错")
	}
	if _, err := ParseKey("zz"); err == nil {
		t.Fatal("非密钥形态应报错")
	}
	// base64: / 64hex / 32 字节原文 三形态等价
	a, err := ParseKey("base64:QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=") // "A"*32
	if err != nil || string(a) != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("base64: 形态解析异常: %v %q", err, a)
	}
	b, err := ParseKey("6161") // 过短，非法
	if err == nil {
		_ = b
		t.Fatal("过短输入应报错")
	}
	c, err := ParseKey(hexAA) // "a"*32 的 hex（程序化生成，避免手数位数）
	if err != nil || string(c) != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("hex 形态解析异常: %v %q", err, c)
	}
}

func TestLooksEncrypted(t *testing.T) {
	neg := []string{
		"",
		"PLAIN-CARD-0001",
		"aGVsbG8geg==", // base64("hello z")，非 JSON
		"eyJhIjoxfQ==", // base64({"a":1})，缺三键
		"!!not-base64!!",
	}
	for _, s := range neg {
		if LooksEncrypted(s) {
			t.Fatalf("%q 不应被识别为密文", s)
		}
	}
}

func TestCrossKeyRejected(t *testing.T) {
	f := loadFixtures(t)
	cardKey, _ := ParseKey(f.CardKey)
	// 用卡密钥匙解 APP_KEY 密文：形态合法但 MAC 不匹配 → 必须拒收（密钥错配检测）
	for _, v := range f.Vectors {
		if v.Kind != "crypt_string" || v.ExpectError != "" {
			continue
		}
		c, err := New(cardKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.OpenString(v.Payload); !errors.Is(err, ErrBadMac) {
			t.Fatalf("跨密钥解密应报 ErrBadMac，实际: %v", err)
		}
		return
	}
	t.Fatal("fixtures 中缺少可用的 crypt_string 向量")
}
