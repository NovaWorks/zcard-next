package inventory

// 导入管线（ ）：预览 → 确认 → 分片批量写入 → 批次追踪。
// 性能纪律（PG/MySQL 大数量优化）：去重一律 content_hash 分组 IN 批查
// （预览全量一次、确认每分片一次，杜绝逐行 Exist 的 N+1）；写入走
// CreateBulk + ON CONFLICT DO NOTHING（唯一索引 (subsite_id, product_id,
// content_hash) 兜底，并发导入窗口的重复行静默跳过）。

import (
	"context"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cardimport"

	entsql "entgo.io/ent/dialect/sql"
)

// ImportPreview 预览结果（确认前端步展示）。
type ImportPreview struct {
	Total     int      `json:"total"`
	DupInFile int      `json:"dup_in_file"` // 文件内重复
	DupInDB   int      `json:"dup_in_db"`   // 与库内重复（同商品）
	Invalid   int      `json:"invalid"`     // 格式非法行
	Sample    []string `json:"sample"`      // 前 50 行预览
	IsPremium bool     `json:"is_premium"`  // 是否靓号格式（含 ---分隔）
}

// ImportInput 导入参数。
type ImportInput struct {
	ProductID uint64
	SkuID     uint64
	Lines     []string // 原始行（每行一条卡密；靓号「卡密---价格---备注」三段）
	Dedup     bool     // 去重开关（false=重复行报错）
	Operator  uint64
}

// dedupChunk IN 批查单批上限（500：IN 列表与唯一索引扫描的平衡点）。
const dedupChunk = 500

// existingHashes 批查库内已存在的 content_hash 集合（分组 IN，替代逐行 Exist）。
func (r *CardRepoImpl) existingHashes(ctx context.Context, client *ent.Client, productID uint64, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	for i := 0; i < len(hashes); i += dedupChunk {
		end := i + dedupChunk
		if end > len(hashes) {
			end = len(hashes)
		}
		rows, err := client.Card.Query().
			Where(card.ProductID(productID), card.ContentHashIn(hashes[i:end]...)).
			Select(card.FieldContentHash).
			Strings(ctx)
		if err != nil {
			return nil, err
		}
		for _, h := range rows {
			out[h] = true
		}
	}
	return out, nil
}

// ParseLines 解析原始行（靓号三段式检测 + 去重 + 预览统计）。
func (r *CardRepoImpl) ParseLines(ctx context.Context, in ImportInput) (*ImportPreview, error) {
	p := &ImportPreview{Sample: make([]string, 0, 50)}
	seen := map[string]bool{}
	client := data.Client(ctx, r.data)

	// 第一遍：文件内去重 + 收集待查 hash
	type pending struct {
		hash  string
		line  string
		parts []string
	}
	uniq := make([]pending, 0, len(in.Lines))
	hashes := make([]string, 0, len(in.Lines))
	for _, raw := range in.Lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		p.Total++

		// 靓号检测
		parts := strings.SplitN(line, "---", 3)
		if len(parts) >= 2 {
			p.IsPremium = true
			line = strings.TrimSpace(parts[0])
		}

		// 文件内去重
		if seen[line] {
			p.DupInFile++
			continue
		}
		seen[line] = true

		hash := r.Cipher.ContentHash(line)
		hashes = append(hashes, hash)
		uniq = append(uniq, pending{hash: hash, line: line, parts: parts})
	}

	// 第二遍：库内去重（一次批查全部 hash）
	exists, err := r.existingHashes(ctx, client, in.ProductID, hashes)
	if err != nil {
		return nil, err
	}
	for _, u := range uniq {
		if exists[u.hash] {
			p.DupInDB++
			continue
		}
		if len(p.Sample) < 50 {
			if len(u.parts) >= 2 {
				p.Sample = append(p.Sample, u.line+"---"+strings.Join(u.parts[1:], "---"))
			} else {
				p.Sample = append(p.Sample, u.line)
			}
		}
	}
	return p, nil
}

// ImportConfirm 确认导入（创建批次 + 分片批量写入）。
func (r *CardRepoImpl) ImportConfirm(ctx context.Context, in ImportInput) (*ent.CardImport, error) {
	client := data.Client(ctx, r.data)

	// 创建批次
	imp, err := client.CardImport.Create().
		SetProductID(in.ProductID).
		SetFilename("api-import").
		SetTotal(int32(len(in.Lines))).
		SetStatus(cardimport.StatusProcessing).
		SetOperatorID(in.Operator).
		Save(ctx)
	if err != nil {
		return nil, err
	}

	// 分片写入（1000/批；每片一次 IN 批查去重 + 一次批量 INSERT）
	var imported, skipped, failed int32
	seen := map[string]bool{}
	batchSize := 1000
	for i := 0; i < len(in.Lines); i += batchSize {
		end := i + batchSize
		if end > len(in.Lines) {
			end = len(in.Lines)
		}
		chunk := in.Lines[i:end]

		creates := make([]*ent.CardCreate, 0, len(chunk))
		pendingHashes := make([]string, 0, len(chunk))
		for _, line := range chunk {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// 靓号解析（非靓号 number=整行，防文件内去重误判）
			var number string
			parts := strings.SplitN(line, "---", 3)
			if len(parts) >= 2 {
				number = strings.TrimSpace(parts[0])
			} else {
				number = strings.TrimSpace(line)
			}

			// 文件内去重
			if seen[number] {
				skipped++
				continue
			}
			seen[number] = true

			// 加密 + keyed hash
			enc, err := r.Cipher.Seal(number, in.ProductID, 0)
			if err != nil {
				failed++
				continue
			}
			hash := r.Cipher.ContentHash(number)
			pendingHashes = append(pendingHashes, hash)

			create := client.Card.Create().
				SetProductID(in.ProductID).
				SetContent(enc).
				SetContentHash(hash).
				SetStatus(card.StatusAvailable)
			if in.SkuID > 0 {
				create.SetSkuID(in.SkuID)
			}
			if imp.ID > 0 {
				create.SetImportID(imp.ID)
			}
			// 靓号字段
			if len(parts) >= 2 {
				nh := r.Cipher.ContentHash("number:" + number)
				create.SetNumberHash(nh)
			}
			creates = append(creates, create)
		}

		// 分片内批查去重（批次间/与库内重复；并发窗口漏网的由 ON CONFLICT 兜底）
		if len(pendingHashes) > 0 {
			exists, err := r.existingHashes(ctx, client, in.ProductID, pendingHashes)
			if err != nil {
				return nil, err
			}
			kept := creates[:0]
			for idx, c := range creates {
				if exists[pendingHashes[idx]] {
					skipped++
					continue
				}
				kept = append(kept, c)
			}
			creates = kept
		}

		// 批量写入：冲突（并发导入竞态）静默跳过，不退化为逐行
		if len(creates) > 0 {
			if err := client.Card.CreateBulk(creates...).
				OnConflict(entsql.DoNothing()).
				Exec(ctx); err != nil {
				return nil, err
			}
			imported += int32(len(creates))
		}
	}

	// 更新批次状态（UpdateOne 返回值不入内存对象——重新读取保证
	// imported/skipped/status 反映真实结果，否则响应恒为创建态 0/processing）
	if _, err = client.CardImport.UpdateOne(imp).
		SetImported(imported).
		SetSkipped(skipped).
		SetFailed(failed).
		SetStatus(cardimport.StatusDone).
		Save(ctx); err != nil {
		return nil, err
	}
	return client.CardImport.Get(ctx, imp.ID)
}

// ListImports 导入批次列表。
func (r *CardRepoImpl) ListImports(ctx context.Context, productID uint64) ([]*ent.CardImport, error) {
	q := data.Client(ctx, r.data).CardImport.Query().
		Order(ent.Desc(cardimport.FieldCreatedAt)).
		Limit(50)
	if productID > 0 {
		q = q.Where(cardimport.ProductID(productID))
	}
	return q.All(ctx)
}

// CancelImport 撤销批次（删除本批 available 卡）。
func (r *CardRepoImpl) CancelImport(ctx context.Context, importID uint64) error {
	client := data.Client(ctx, r.data)
	n, err := client.Card.Delete().
		Where(card.ImportID(importID), card.StatusEQ(card.StatusAvailable)).
		Exec(ctx)
	if err != nil {
		return err
	}
	_, err = client.CardImport.UpdateOneID(importID).
		SetStatus(cardimport.StatusCanceled).
		Save(ctx)
	_ = n
	return err
}

// ── 导出（）────────────────────────────────────────────────

// CsvSafe 防 Excel 注入（= + - @ 开头前缀 '）。
func CsvSafe(s string) string {
	if len(s) > 0 && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		return "'" + s
	}
	return s
}

// ExportCards 导出 available 卡密（返回解密明文行；需 card:export 权限 + 审计）。
func (r *CardRepoImpl) ExportCards(ctx context.Context, productID uint64) ([]string, error) {
	rows, err := data.Client(ctx, r.data).Card.Query().
		Where(card.ProductID(productID), card.StatusEQ(card.StatusAvailable)).
		Order(ent.Asc(card.FieldID)).
		Limit(10000). // 导出上限
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		plain, err := r.Cipher.Open(row.Content, row.ProductID, row.SubsiteID)
		if err != nil {
			continue // 解密失败降级跳过（绝不 500）
		}
		out = append(out, CsvSafe(plain))
	}
	return out, nil
}
