package crypto

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func TestBoxSealOpen(t *testing.T) {
	box, err := NewBox(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("CARD-ABCD-1234-EFGH")
	aad := []byte("product:42:subsite:0")

	ct, err := box.Seal(plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("密文中包含明文")
	}
	// 同一明文两次加密，密文不同（随机 nonce）
	ct2, _ := box.Seal(plain, aad)
	if bytes.Equal(ct, ct2) {
		t.Fatal("两次加密密文相同：nonce 未随机化")
	}

	got, err := box.Open(ct, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("解密结果不一致: %q", got)
	}

	// AAD 不匹配必须解密失败（防密文挪用到其他商品）
	if _, err := box.Open(ct, []byte("product:43:subsite:0")); err == nil {
		t.Fatal("AAD 不匹配时解密应失败")
	}
	// 错误密钥必须解密失败
	other := testKey(t)
	other[0] ^= 0xFF
	box2, _ := NewBox(other)
	if _, err := box2.Open(ct, aad); err == nil {
		t.Fatal("错误密钥解密应失败")
	}
}

func TestKeyedHash(t *testing.T) {
	k1 := testKey(t)
	h1 := KeyedHash(k1, []byte("low-entropy-code"))
	h2 := KeyedHash(k1, []byte("low-entropy-code"))
	if h1 != h2 {
		t.Fatal("同 key 同明文 hash 应一致")
	}
	k2 := testKey(t)
	k2[5] ^= 0x01
	if KeyedHash(k2, []byte("low-entropy-code")) == h1 {
		t.Fatal("不同 key 的 hash 应不同（keyed hash 语义）")
	}
	if len(h1) != 64 || strings.ToLower(h1) != h1 {
		t.Fatalf("hash 应为 64 位小写 hex: %q", h1)
	}
}

func TestParseHexKey(t *testing.T) {
	if _, err := ParseHexKey(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("合法 32 字节 hex 密钥应通过: %v", err)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("ab", 31), strings.Repeat("zz", 32)} {
		if _, err := ParseHexKey(bad); err == nil {
			t.Fatalf("非法密钥 %q 应报错", bad)
		}
	}
	_ = hex.EncodeToString
}
