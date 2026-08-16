package inventory

// T2 导入管线（P1-02 任务书 T2）：预览 → 确认 → 分片写入 → 批次追踪。
// 1.x CardImportService 模式平移：块内+库内双重去重、>5000 转队列、批次撤销。

import (
	"context"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cardimport"
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

// ParseLines 解析原始行（靓号三段式检测 + 去重 + 预览统计）。
func (r *CardRepoImpl) ParseLines(ctx context.Context, in ImportInput) (*ImportPreview, error) {
	p := &ImportPreview{Sample: make([]string, 0, 50)}
	seen := map[string]bool{}
	client := data.Client(ctx, r.data)

	for i, line := range in.Lines {
		line = strings.TrimSpace(line)
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

		// 库内去重（content_hash 比对）
		hash := r.Cipher.ContentHash(line)
		exists, err := client.Card.Query().
			Where(card.ProductID(in.ProductID), card.ContentHash(hash)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if exists {
			p.DupInDB++
			continue
		}

		if len(p.Sample) < 50 {
			if len(parts) >= 2 {
				p.Sample = append(p.Sample, strings.TrimSpace(parts[0])+"---"+strings.Join(parts[1:], "---"))
			} else {
				p.Sample = append(p.Sample, line)
			}
		}
		_ = i
	}
	return p, nil
}

// ImportConfirm 确认导入（创建批次 + 分片写入）。
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

	// 分片写入（1000/批）
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
		for _, line := range chunk {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// 靓号解析
			var number string
			parts := strings.SplitN(line, "---", 3)
			if len(parts) >= 2 {
				number = strings.TrimSpace(parts[0])
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

			// 检查是否已存在（批次间去重）
			exists, _ := client.Card.Query().
				Where(card.ProductID(in.ProductID), card.ContentHash(hash)).Exist(ctx)
			if exists {
				skipped++
				continue
			}

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

		if len(creates) > 0 {
			if err := client.Card.CreateBulk(creates...).Exec(ctx); err != nil {
				// 部分冲突（并发导入同内容）——跳过冲突行
				for _, c := range creates {
					if _, err := c.Save(ctx); err == nil {
						imported++
					} else {
						skipped++
					}
				}
			} else {
				imported += int32(len(creates))
			}
		}
	}

	// 更新批次状态
	_, err = client.CardImport.UpdateOne(imp).
		SetImported(imported).
		SetSkipped(skipped).
		SetFailed(failed).
		SetStatus(cardimport.StatusDone).
		Save(ctx)
	return imp, err
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

// ── 导出（T3）────────────────────────────────────────────────

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

// 保持 time 引用
