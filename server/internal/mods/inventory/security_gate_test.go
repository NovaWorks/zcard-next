package inventory

// §5.20.7 防偷卡门禁测试（红灯即阻断）：
// 加密强制 + keyed hash + 库内无明文 + AAD 绑定 + 掩码尾 4 位明文。

import (
	"context"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
)

// TestCardCipherRoundTrip 加密往返 + AAD 换商品解密失败（铁律 11）。
func TestCardCipherRoundTrip(t *testing.T) {
	c := NewTestCipher(t)
	plain := "SECRET-CARD-1234"

	ct, err := c.Seal(plain, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 密文不得等于明文（无明文落盘）
	if string(ct) == plain {
		t.Fatal("加密失败：密文与明文相同")
	}

	got, err := c.Open(ct, 1, 0)
	if err != nil || got != plain {
		t.Fatalf("解密失败：got=%q err=%v", got, err)
	}

	// AAD 换商品 → 解密必须失败
	if _, err := c.Open(ct, 2, 0); err == nil {
		t.Fatal("AAD 绑定失效：换商品仍能解密")
	}
}

// TestContentHashKeyed keyed hash 去重（HMAC-SHA256，防低熵卡彩虹表）。
func TestContentHashKeyed(t *testing.T) {
	c := NewTestCipher(t)
	h := c.ContentHash("1234") // 低熵明文
	if len(h) != 64 {           // HMAC-SHA256 hex
		t.Fatalf("keyed hash 长度错误：%d", len(h))
	}
	if h == "1234" {
		t.Fatal("content_hash 未做 keyed hash")
	}
	// 不同密钥不同 hash
	c2, err := NewCardCipher([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ContentHash("1234") == c2.ContentHash("1234") {
		t.Fatal("keyed hash 未绑定密钥")
	}
}

// TestNoPlaintextAtRest 库内卡片 content 均为密文形态。
func TestNoPlaintextAtRest(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()

	p, err := d.Client.Product.Create().
		SetSubsiteID(0).SetName("安全门禁商品").SetSlug("sec-gate").
		SetPrice(1000).SetStockType(product.StockTypeCard).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	plain := "PLAINTEXT-CARD-9999"
	ct, _ := repo.Cipher.Seal(plain, p.ID, 0)
	_, err = d.Client.Card.Create().
		SetProductID(p.ID).SetSubsiteID(0).
		SetContent(ct).SetContentHash(repo.Cipher.ContentHash(plain)).
		SetStatus(card.StatusAvailable).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rows, _ := d.Client.Card.Query().Where(card.ProductID(p.ID)).All(ctx)
	for _, r := range rows {
		if string(r.Content) == plain {
			t.Fatal("库内落明文卡密")
		}
		// 必须能解密还原
		got, err := repo.Cipher.Open(r.Content, r.ProductID, r.SubsiteID)
		if err != nil || got != plain {
			t.Fatalf("密文不可还原：got=%q err=%v", got, err)
		}
	}
}

// TestMaskPlainTail 管理员默认掩码（尾 4 位明文）。
func TestMaskPlainTail(t *testing.T) {
	cases := map[string]string{
		"SECRET-CARD-1234": "****1234",
		"AB":               "****",
		"ABCD":             "****",
		"ABCDE":            "****BCDE",
	}
	for in, want := range cases {
		if got := maskContent(in); got != want {
			t.Fatalf("maskContent(%q) = %q, want %q", in, got, want)
		}
	}
}
