package migratev1

// P3 卡密域迁移：card_imports → cards 全链（1.x CBC 解密 → 2.0 GCM Seal +
// HMAC hash 全量重算 → 商品内重复跳过 → 状态映射与锁超时释放）→
// order_deliveries 缺失卡密回填（delivery_mode=delete 已物理删的唯一副本）。
// 映射规格《数据迁移工具开发计划》§5.4；order_id 置 0 由 P4 迁完订单后回填。
//
// 性能：cards 走专用批量循环（keyset 分页 + 批级幂等 IN 查询 + CreateBulk），
// 目标 ≥5k 行/s；同商品重复卡密撞唯一索引时该批回退逐行并计 skipped_dup。

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cardimport"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1/laracrypt"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
)

// lockReleaseTTL 1.x 锁卡超时释放口径：locked_at 早于此阈值的 locked 卡按
// available 迁移（对齐 1.x 超时关单释放语义；订单 TTL 为分钟量级，取保守上界）。
const lockReleaseTTL = 30 * time.Minute

// MigrateInventory P3 阶段。
func (m *Migrator) MigrateInventory(ctx context.Context) error {
	if err := m.migrateCardImports(ctx); err != nil {
		return err
	}
	if err := m.migrateCards(ctx); err != nil {
		return err
	}
	return m.backfillOrderDeliveries(ctx)
}

// migrateCardImports 1.x card_imports → card_imports（导入批次历史）。
func (m *Migrator) migrateCardImports(ctx context.Context) error {
	var (
		id, productID, operatorID int64
		total, success, failed    int64
		skipped                   sql.NullInt64
		source                    sql.NullString
		status                    string
		createdAt, updatedAt      sql.NullString
	)
	return m.scanTable(ctx, "card_imports",
		[]string{"id", "product_id", "operator_id", "source", "total",
			"success_count", "failed_count", "skipped_count", "status", "created_at", "updated_at"},
		func() []any {
			return []any{&id, &productID, &operatorID, &source, &total,
				&success, &failed, &skipped, &status, &createdAt, &updatedAt}
		},
		func(int64) error {
			if _, ok := m.IDs.Get(ctx, "card_imports", uint64(id)); ok {
				m.st.Record("card_imports", "skip")
				return nil
			}
			newPID, ok := m.IDs.Get(ctx, "products", uint64(productID))
			if !ok {
				m.st.Record("card_imports", "skip") // 分站商品：P6 补
				return nil
			}
			ca, caOK, err := mustTime(nullStr(createdAt), m.TZ)
			if err != nil {
				return err
			}
			ua, _, err := mustTime(nullStr(updatedAt), m.TZ)
			if err != nil {
				return err
			}
			b := m.Client.CardImport.Create().
				SetProductID(newPID).
				SetOperatorID(0). // 1.x operator 是普通用户表；2.0 语义为管理员，不直迁
				SetFilename(orString(nullStr(source), "v1-import-"+fmt.Sprint(id))).
				SetTotal(int32(total)).
				SetImported(int32(success)).
				SetFailed(int32(failed)).
				SetSkipped(int32(nullInt(skipped))).
				SetStatus(cardimport.Status(mapCardImportStatus(status)))
			if caOK {
				b.SetCreatedAt(ca)
			}
			b.SetUpdatedAt(ua)
			ci, err := b.Save(ctx)
			if err != nil {
				return err
			}
			if _, err := m.IDs.Put(ctx, m.Client, "card_imports", uint64(id), ci.ID); err != nil {
				return err
			}
			m.st.Record("card_imports", "migrated")
			return nil
		},
	)
}

func mapCardImportStatus(s string) string {
	switch s {
	case "running":
		return "processing"
	case "failed":
		return "failed"
	default: // completed
		return "done"
	}
}

// cardRow 扫描中间结构（cards 专用批量循环）。
type cardRow struct {
	id                      int64
	productID, importID     sql.NullInt64
	orderID                 sql.NullInt64
	content, contentHash    string
	status                  string
	lockedAt, usedAt        sql.NullString
	createdAt, updatedAt    sql.NullString
	note, cardType          sql.NullString
	ownerID                 sql.NullInt64
	draftPremium, draftCost int64
	price                   sql.NullInt64
	numberHash              sql.NullString
}

// migrateCards 卡密全链批量迁移。
func (m *Migrator) migrateCards(ctx context.Context) error {
	t := m.st.table("cards")
	if m.dry {
		var n int64
		if err := m.Src.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `cards`").Scan(&n); err != nil {
			return err
		}
		t.Planned = n
		return nil
	}
	cipher, err := inventory.NewCardCipher(m.NewCardKey)
	if err != nil {
		return fmt.Errorf("ZCARD_CARD_KEY 不可用（卡密重加密必需）: %w", err)
	}
	cols := "`id`, `product_id`, `import_id`, `order_id`, `content`, `content_hash`, `status`, " +
		"`locked_at`, `used_at`, `created_at`, `updated_at`, `note`, `card_type`, `owner_id`, " +
		"`draft_premium`, `draft_cost`, `price`, `number_hash`"
	query := fmt.Sprintf("SELECT %s FROM `cards` WHERE `id` > ? ORDER BY `id` LIMIT ?", cols)

	var cursor int64
	for {
		rows, err := m.Src.DB.QueryContext(ctx, query, cursor, m.Opts.Batch)
		if err != nil {
			return fmt.Errorf("扫描 cards 失败: %w", err)
		}
		var batch []cardRow
		for rows.Next() {
			var r cardRow
			if err := rows.Scan(&r.id, &r.productID, &r.importID, &r.orderID, &r.content,
				&r.contentHash, &r.status, &r.lockedAt, &r.usedAt, &r.createdAt, &r.updatedAt,
				&r.note, &r.cardType, &r.ownerID, &r.draftPremium, &r.draftCost, &r.price,
				&r.numberHash); err != nil {
				rows.Close()
				return fmt.Errorf("扫描 cards 行失败: %w", err)
			}
			batch = append(batch, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(batch) == 0 {
			return nil
		}
		cursor = batch[len(batch)-1].id

		if err := m.migrateCardBatch(ctx, cipher, batch); err != nil {
			return err
		}
		if len(batch) < m.Opts.Batch {
			return nil
		}
	}
}

// migrateCardBatch 单批：批级幂等 + 转换 + CreateBulk（唯一冲突回退逐行）。
func (m *Migrator) migrateCardBatch(ctx context.Context, cipher *inventory.CardCipher, batch []cardRow) error {
	t := m.st.table("cards")

	oldIDs := make([]uint64, len(batch))
	for i, r := range batch {
		oldIDs[i] = uint64(r.id)
	}
	existing := m.IDs.GetBatch(ctx, "cards", oldIDs)

	var (
		preparedRows []preparedCard
		seen         = map[string]bool{} // 批内 (product,hash) 去重
	)
	for _, r := range batch {
		if _, ok := existing[uint64(r.id)]; ok {
			t.SkippedExists++
			continue
		}
		newPID, ok := m.IDs.Get(ctx, "products", uint64(nullInt(r.productID)))
		if !ok {
			t.SkippedExists++ // 分站商品：P6 补
			continue
		}
		plain, wasEnc, err := openV1Card(r.content, m.CardKey)
		if err != nil {
			if m.Opts.OnError == "abort" {
				return fmt.Errorf("卡密 %d 解密失败（abort）: %w", r.id, err)
			}
			t.Failed++
			m.RW.AddError("cards", uint64(r.id), "解密失败: "+err.Error())
			continue
		}
		// 1.x content_hash 校验（裸 sha256）：漂移仅告警不阻断（老库存在不可信 hash）
		if sum := sha256.Sum256([]byte(plain)); hex.EncodeToString(sum[:]) != r.contentHash {
			m.RW.AddError("cards", uint64(r.id), "content_hash 与明文不一致（按明文重算迁移）")
		}
		hash := cipher.ContentHash(plain)
		sealed, err := cipher.Seal(plain, newPID, m.productSubsite(ctx, newPID))
		if err != nil {
			t.Failed++
			m.RW.AddError("cards", uint64(r.id), "重加密失败: "+err.Error())
			continue
		}
		dedupKey := fmt.Sprintf("%d:%s", newPID, hash)
		if seen[dedupKey] {
			t.SkippedExists++ // 同商品重复卡密（1.x dedup_hash 可空导致）：跳过
			continue
		}
		seen[dedupKey] = true
		_ = wasEnc
		preparedRows = append(preparedRows, preparedCard{row: r, newPID: newPID, content: sealed, hash: hash})
	}
	if len(preparedRows) == 0 {
		return nil
	}

	builders := make([]*ent.CardCreate, 0, len(preparedRows))
	for _, p := range preparedRows {
		builders = append(builders, m.cardBuilder(ctx, cipher, p.row, p.newPID, p.content, p.hash))
	}
	// 性能关键路径：bulk 插入前取 MAX(id) 基线，插入后一次回查区间连续 ID，
	// 再批量写 v1id_maps（3 次往返替代原先的 2N 次）；回查异常回退逐行保正确性。
	base, err := m.Client.Card.Query().
		Aggregate(ent.Max(card.FieldID)).
		Int(ctx)
	if err != nil {
		base = 0
	}
	if _, err := m.Client.Card.CreateBulk(builders...).Save(ctx); err != nil {
		// 唯一冲突（目标库已有同商品同 hash）→ 回退逐行，冲突行计跳过
		for _, p := range preparedRows {
			c, err := m.cardBuilder(ctx, cipher, p.row, p.newPID, p.content, p.hash).Save(ctx)
			if err != nil {
				if isUniqueViolation(err) {
					t.SkippedExists++
					continue
				}
				if m.Opts.OnError == "abort" {
					return err
				}
				t.Failed++
				m.RW.AddError("cards", uint64(p.row.id), err.Error())
				continue
			}
			t.Migrated++
			if _, err := m.IDs.Put(ctx, m.Client, "cards", uint64(p.row.id), c.ID); err != nil {
				return err
			}
		}
		return nil
	}
	newIDs, err := m.Client.Card.Query().
		Where(card.IDGT(uint64(base))).
		Order(ent.Asc(card.FieldID)).
		IDs(ctx)
	if err != nil || len(newIDs) != len(preparedRows) {
		// 回查异常（行数不符=自增不连续假设被打破）：回退逐行回查保正确性
		t.Migrated += int64(len(preparedRows))
		return m.recordBulkIDs(ctx, preparedRows)
	}
	pairs := make([][2]uint64, len(preparedRows))
	for i, p := range preparedRows {
		pairs[i] = [2]uint64{uint64(p.row.id), newIDs[i]}
	}
	if err := m.IDs.PutBatch(ctx, m.Client, "cards", pairs); err != nil {
		return err
	}
	t.Migrated += int64(len(preparedRows))
	return nil
}

// preparedCard 已完成解密+重加密的待写行。
type preparedCard struct {
	row     cardRow
	newPID  uint64
	content []byte
	hash    string
}

// recordBulkIDs bulk 插入后按内容回查真实新 ID 写 idmap（bulk 不返回逐行 ID；
// 以 (subsite=0, product, content_hash) 定位，唯一索引保证至多一行）。
func (m *Migrator) recordBulkIDs(ctx context.Context, prepared []preparedCard) error {
	for _, p := range prepared {
		row, err := m.Client.Card.Query().
			Where(
				card.ProductID(p.newPID),
				card.ContentHash(p.hash),
			).Only(ctx)
		if err != nil {
			return fmt.Errorf("bulk 卡密回查失败（product=%d hash=%s）: %w", p.newPID, p.hash, err)
		}
		if _, err := m.IDs.Put(ctx, m.Client, "cards", uint64(p.row.id), row.ID); err != nil {
			return err
		}
	}
	return nil
}

// openV1Card 1.x content 解密（形态识别直通明文；等价 CardCipher::decrypt 非 strict）。
func openV1Card(content string, cardKey []byte) (string, bool, error) {
	if !laracrypt.LooksEncrypted(content) {
		return content, false, nil
	}
	if len(cardKey) == 0 {
		return "", true, fmt.Errorf("密文卡但旧卡密钥匙不可用")
	}
	c, err := laracrypt.New(cardKey)
	if err != nil {
		return "", true, err
	}
	plain, err := c.OpenString(content)
	if err != nil {
		return "", true, err
	}
	return plain, true, nil
}

// cardBuilder 单行构建。
func (m *Migrator) cardBuilder(ctx context.Context, cipher *inventory.CardCipher, r cardRow,
	newPID uint64, sealed []byte, hash string) *ent.CardCreate {

	b := m.Client.Card.Create().
		SetProductID(newPID).
		SetContent(sealed).
		SetContentHash(hash).
		SetStatus(card.Status(mapCardStatus(r.status, nullStr(r.lockedAt), m.TZ))).
		SetDraftPremium(r.draftPremium).
		SetDraftCost(r.draftCost)
	if imp := nullInt(r.importID); imp > 0 {
		if newImp, ok := m.IDs.Get(ctx, "card_imports", uint64(imp)); ok {
			b.SetImportID(newImp)
		}
	}
	if o := nullInt(r.ownerID); o > 0 {
		if newU, ok := m.IDs.Get(ctx, "users", uint64(o)); ok {
			b.SetOwnerID(newU)
		}
	}
	if n := nullStr(r.note); n != "" {
		b.SetNote(n)
	}
	if ct := nullStr(r.cardType); ct != "" {
		b.SetCardType(ct)
	}
	if nh := nullStr(r.numberHash); nh != "" {
		b.SetNumberHash(nh)
	}
	if pr := nullInt(r.price); pr > 0 {
		b.SetPrice(pr)
	}
	if t, ok, err := mustTime(nullStr(r.lockedAt), m.TZ); err == nil && ok {
		b.SetLockedAt(t)
	}
	if t, ok, err := mustTime(nullStr(r.usedAt), m.TZ); err == nil && ok {
		b.SetUsedAt(t)
	}
	if t, ok, err := mustTime(nullStr(r.createdAt), m.TZ); err == nil && ok {
		b.SetCreatedAt(t)
	}
	if t, ok, err := mustTime(nullStr(r.updatedAt), m.TZ); err == nil && ok {
		b.SetUpdatedAt(t)
	}
	// order_id：1.x 订单 P4 才迁——置 0，P4 迁完订单后按 v1id_maps(cards) 回填
	return b
}

func mapCardStatus(status, lockedAt string, tz *time.Location) string {
	switch status {
	case "used":
		return "used"
	case "disabled":
		return "disabled"
	case "locked":
		if t, ok := parseNaiveTime(lockedAt, tz); ok && time.Since(t) > lockReleaseTTL {
			return "available" // 锁超时释放（1.x 超时关单语义）
		}
		return "reserved"
	default: // unused
		return "available"
	}
}

// backfillOrderDeliveries delivery_mode=delete 的订单已物理删卡，
// order_deliveries.card_content 是唯一明文副本 → 重建 used 卡（order_id 置 0，
// v1id_maps("order_deliveries") 记 old delivery id → new card id 供 P4 关联）。
func (m *Migrator) backfillOrderDeliveries(ctx context.Context) error {
	t := m.st.table("order_deliveries_backfill")
	if m.dry {
		return nil
	}
	cipher, err := inventory.NewCardCipher(m.NewCardKey)
	if err != nil {
		return err
	}
	rows, err := m.Src.DB.QueryContext(ctx,
		"SELECT `id`, `order_id`, `product_id`, `card_content`, `delivered_at` FROM `order_deliveries` ORDER BY `id`")
	if err != nil {
		return fmt.Errorf("扫描 order_deliveries 失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, orderID, productID sql.NullInt64
			content                string
			deliveredAt            sql.NullString
		)
		if err := rows.Scan(&id, &orderID, &productID, &content, &deliveredAt); err != nil {
			return err
		}
		if _, ok := m.IDs.Get(ctx, "order_deliveries", uint64(nullInt(id))); ok {
			t.SkippedExists++
			continue
		}
		newPID, ok := m.IDs.Get(ctx, "products", uint64(nullInt(productID)))
		if !ok {
			t.SkippedExists++ // 分站商品：P6 补
			continue
		}
		// 源库 cards 仍有该卡（status 模式未物理删）→ 无需重建，仅记 delivery 映射占位
		sum := sha256.Sum256([]byte(content))
		var exists int
		if err := m.Src.DB.QueryRowContext(ctx,
			"SELECT 1 FROM `cards` WHERE `product_id` = ? AND `content_hash` = ? LIMIT 1",
			nullInt(productID), hex.EncodeToString(sum[:])).Scan(&exists); err == nil {
			t.SkippedExists++
			continue
		} else if err != sql.ErrNoRows {
			return err
		}
		hash := cipher.ContentHash(content)
		sealed, err := cipher.Seal(content, newPID, m.productSubsite(ctx, newPID))
		if err != nil {
			t.Failed++
			continue
		}
		b := m.Client.Card.Create().
			SetProductID(newPID).
			SetContent(sealed).
			SetContentHash(hash).
			SetStatus(card.StatusUsed)
		if dt, ok, err := mustTime(nullStr(deliveredAt), m.TZ); err == nil && ok {
			b.SetUsedAt(dt)
		}
		c, err := b.Save(ctx)
		if err != nil {
			if isUniqueViolation(err) { // 与已迁卡密同明文（回填与主迁移重叠）
				t.SkippedExists++
				continue
			}
			t.Failed++
			m.RW.AddError("order_deliveries", uint64(nullInt(id)), err.Error())
			continue
		}
		if _, err := m.IDs.Put(ctx, m.Client, "order_deliveries", uint64(nullInt(id)), c.ID); err != nil {
			return err
		}
		t.Migrated++
	}
	return rows.Err()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "unique constraint")
}

func orString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
