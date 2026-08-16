package admincmd

// reencrypt-cards 内核单测：旧 key 加密 → 轮换 → 新 key 可解、hash 更新、幂等跳过。

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	_ "modernc.org/sqlite"
)

func newReencryptClient(t *testing.T) *ent.Client {
	t.Helper()
	handle, err := db.SQLite.Open("file:reencrypt?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, handle)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client
}

func TestReencryptCardsRotatesKeyAndHash(t *testing.T) {
	ctx := context.Background()
	client := newReencryptClient(t)

	oldKey := []byte("01234567890123456789012345678901") // 32 字节
	newKey := []byte("abcdefghijklmnopqrstuvwxyz123456") // 32 字节
	oldCipher, err := inventory.NewCardCipher(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	newCipher, err := inventory.NewCardCipher(newKey)
	if err != nil {
		t.Fatal(err)
	}

	// 建商品（cards.product_id 硬外键 → products）
	p, err := client.Product.Create().
		SetSubsiteID(0).SetName("测试商品").SetSlug("test-reencrypt").
		SetPrice(1000).SetStockType("card").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 建 2 张卡（1 张普通 + 1 张靓号）
	enc, _ := oldCipher.Seal("CARD-0001", p.ID, 0)
	_, err = client.Card.Create().
		SetProductID(p.ID).SetSubsiteID(0).
		SetContent(enc).SetContentHash(oldCipher.ContentHash("CARD-0001")).
		SetStatus(card.StatusAvailable).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := oldCipher.Seal("88888888", p.ID, 0)
	_, err = client.Card.Create().
		SetProductID(p.ID).SetSubsiteID(0).
		SetContent(enc2).SetContentHash(oldCipher.ContentHash("88888888")).
		SetNumberHash(oldCipher.ContentHash("number:88888888")).
		SetStatus(card.StatusAvailable).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rotated, skipped, failed, err := ReencryptCards(ctx, client, oldKey, newKey, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if rotated != 2 || skipped != 0 || failed != 0 {
		t.Fatalf("期望 rotated=2 skipped=0 failed=0，得 %d/%d/%d", rotated, skipped, failed)
	}

	rows, err := client.Card.Query().Order(ent.Asc(card.FieldID)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，得 %d", len(rows))
	}
	// 新 key 可解、hash 已更新；旧 key 解不开
	for i, row := range rows {
		plain, err := newCipher.Open(row.Content, row.ProductID, row.SubsiteID)
		if err != nil {
			t.Fatalf("卡 %d 新 key 解密失败: %v", i, err)
		}
		if row.ContentHash != newCipher.ContentHash(plain) {
			t.Fatalf("卡 %d content_hash 未随新 key 更新", i)
		}
		if _, err := oldCipher.Open(row.Content, row.ProductID, row.SubsiteID); err == nil {
			t.Fatalf("卡 %d 旧 key 仍可解密（未轮换）", i)
		}
		if row.NumberHash != "" && row.NumberHash != newCipher.ContentHash("number:"+plain) {
			t.Fatalf("靓号卡 number_hash 未随新 key 更新")
		}
	}

	// 幂等：再次轮换应全部跳过
	rotated, skipped, failed, err = ReencryptCards(ctx, client, oldKey, newKey, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if rotated != 0 || skipped != 2 || failed != 0 {
		t.Fatalf("幂等重跑期望 0/2/0，得 %d/%d/%d", rotated, skipped, failed)
	}
}
