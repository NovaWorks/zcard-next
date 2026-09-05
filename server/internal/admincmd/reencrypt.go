package admincmd

// reencrypt-cards 密钥轮换子命令（ 骨架）：
// 旧 key 解密 → 新 key 重加密 → 同事务重算 content_hash（+ 靓号 number_hash），分批 1000。
// 幂等：旧 key 解不开但新 key 能解的行视为已轮换跳过（中断可续跑）。

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// RunReencrypt reencrypt-cards 子命令入口（main 分发调用）。
//
//	zcard reencrypt-cards --new-key <64 hex> [--conf configs] [--batch 1000]
func RunReencrypt(args []string) error {
	fs := flag.NewFlagSet("reencrypt-cards", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "配置目录")
	newKeyHex := fs.String("new-key", "", "新密钥（64 hex 字符 = 32 字节）")
	batchSize := fs.Int("batch", 1000, "每批行数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *newKeyHex == "" {
		return fmt.Errorf("--new-key 必填（64 hex 字符）")
	}
	newKey, err := crypto.ParseHexKey(*newKeyHex)
	if err != nil {
		return err
	}

	// 旧 key：env ZCARD_CARD_KEY > conf.security.card_key
	bc := &conf.Bootstrap{}
	if err := scanConf(*confDir, bc); err != nil {
		return err
	}
	if bc.Security == nil {
		bc.Security = &conf.Security{}
	}
	oldRaw := os.Getenv("ZCARD_CARD_KEY")
	if oldRaw == "" {
		oldRaw = bc.Security.CardKey
	}
	if oldRaw == "" {
		return fmt.Errorf("未找到旧卡密密钥（设置 ZCARD_CARD_KEY 或 conf.security.card_key）")
	}
	oldKey, err := crypto.ParseHexKey(oldRaw)
	if err != nil {
		return fmt.Errorf("旧密钥非法: %w", err)
	}

	client, cleanup, err := open(*confDir)
	if err != nil {
		return err
	}
	defer cleanup()

	rotated, skipped, failed, err := ReencryptCards(context.Background(), client, oldKey, newKey, *batchSize)
	if err != nil {
		return err
	}
	fmt.Printf("密钥轮换完成：重加密 %d，跳过（已轮换）%d，失败 %d\n", rotated, skipped, failed)
	return nil
}

// ReencryptCards 分批轮换（可测试内核）：返回（重加密数, 已跳过数, 失败数）。
// 每批一个事务：重加密 + 重算 content_hash（及靓号 number_hash）原子提交。
func ReencryptCards(ctx context.Context, client *ent.Client, oldKey, newKey []byte, batchSize int) (rotated, skipped, failed int, err error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	oldCipher, err := inventory.NewCardCipher(oldKey)
	if err != nil {
		return 0, 0, 0, err
	}
	newCipher, err := inventory.NewCardCipher(newKey)
	if err != nil {
		return 0, 0, 0, err
	}

	var cursor uint64
	for {
		rows, err := client.Card.Query().
			Where(card.IDGT(cursor)).
			Order(ent.Asc(card.FieldID)).
			Limit(batchSize).
			All(ctx)
		if err != nil {
			return rotated, skipped, failed, err
		}
		if len(rows) == 0 {
			break
		}

		tx, err := client.Tx(ctx)
		if err != nil {
			return rotated, skipped, failed, err
		}
		c := tx.Client()
		for _, row := range rows {
			plain, err := oldCipher.Open(row.Content, row.ProductID, row.SubsiteID)
			if err != nil {
				// 旧 key 解不开 → 尝试新 key；能解说明已轮换过，跳过（幂等续跑）
				if p2, err2 := newCipher.Open(row.Content, row.ProductID, row.SubsiteID); err2 == nil {
					_ = p2
					skipped++
					continue
				}
				failed++
				continue
			}
			enc, err := newCipher.Seal(plain, row.ProductID, row.SubsiteID)
			if err != nil {
				failed++
				continue
			}
			u := c.Card.UpdateOneID(row.ID).
				SetContent(enc).
				SetContentHash(newCipher.ContentHash(plain))
			if row.NumberHash != "" {
				// 靓号 number_hash = HMAC(key, "number:"+plain)，随 key 变一并重算
				u.SetNumberHash(newCipher.ContentHash("number:" + plain))
			}
			if _, err := u.Save(ctx); err != nil {
				failed++
				continue
			}
			rotated++
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			return rotated, skipped, failed, err
		}
		cursor = rows[len(rows)-1].ID
	}
	return rotated, skipped, failed, nil
}
