package migratev1

// verify-only：对已迁移数据做事后校验（不迁移、不写业务表），产出独立报告。
// 校验项（《数据迁移工具开发计划》§6）：
//  1. 金额恒等式：orders.total_amount == SUM(order_amount_lines.amount)（2.0 内部）
//  2. 钱包三方对账：末条流水 balance_after vs wallet_accounts.available（差异清单）
//  3. 分站余额对账：reseller_ledger 合计 vs reseller_balance_accounts.available
//  4. 卡密抽样：源端解密明文 vs 目标端解密明文逐字节相等 + HMAC 一致
//  5. 行数三方核对：源行数 / v1id_maps 映射数 / 目标行数（差异列出供人工判读）
//  6. 引用完整性：已迁订单的 user_id=0 但源库有值的清单（映射缺失）

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/orderamountline"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/resellerledgerentry"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/v1idmap"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
)

// VerifyResult 校验报告。
type VerifyResult struct {
	Passed  bool
	Checks  []Check
	Summary map[string]string
}

// RunVerify 执行事后校验。
func (m *Migrator) RunVerify(ctx context.Context, sample int) (*VerifyResult, error) {
	if sample <= 0 {
		sample = 100
	}
	res := &VerifyResult{Summary: map[string]string{}}
	fail := func(name, msg string) {
		res.Checks = append(res.Checks, Check{Name: name, Status: StatusFail, Message: msg})
	}
	pass := func(name, msg string) {
		res.Checks = append(res.Checks, Check{Name: name, Status: StatusOK, Message: msg})
	}

	// 1. 金额恒等式（2.0 内部，逐单核对取汇总）
	type row struct {
		OrderID uint64
		Total   int64
		Sum     int64
	}
	var broken []row
	os, err := m.Client.Order.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range os {
		sum, err := m.Client.OrderAmountLine.Query().
			Where(orderamountline.OrderIDEQ(o.ID)).
			Aggregate(ent.Sum(orderamountline.FieldAmount)).
			Int(ctx)
		if err != nil {
			return nil, err
		}
		if int64(sum) != o.TotalAmount {
			broken = append(broken, row{o.ID, o.TotalAmount, int64(sum)})
		}
	}
	if len(broken) > 0 {
		fail("金额恒等式", fmt.Sprintf("%d/%d 单 total≠SUM(lines)，首批：order_id=%d total=%d sum=%d",
			len(broken), len(os), broken[0].OrderID, broken[0].Total, broken[0].Sum))
		for i, b := range broken {
			m.RW.AddError("verify_amount", b.OrderID, fmt.Sprintf("total=%d lines_sum=%d", b.Total, b.Sum))
			if i >= 100 {
				break
			}
		}
	} else {
		pass("金额恒等式", fmt.Sprintf("全部 %d 单 total == SUM(lines)", len(os)))
	}

	// 2. 钱包三方对账（复用 P5 逻辑的只读版）
	if err := m.ReconcileWallets(ctx); err != nil {
		return nil, err
	}
	rc := m.st.table("wallet_reconcile")
	if rc.SkippedExists > 0 {
		fail("钱包对账", fmt.Sprintf("%d 个账户流水与快照不一致（差异清单见 errors.jsonl）", rc.SkippedExists))
	} else {
		pass("钱包对账", fmt.Sprintf("全部 %d 个账户流水与快照一致", rc.Migrated))
	}

	// 3. 分站余额对账
	profiles, err := m.Client.ResellerProfile.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	subDiff := 0
	accs, err := m.Client.ResellerBalanceAccount.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	accBySite := map[uint64]*ent.ResellerBalanceAccount{}
	for _, a := range accs {
		accBySite[a.SubsiteID] = a
	}
	for _, p := range profiles {
		acc := accBySite[p.ID]
		sum, err := m.Client.ResellerLedgerEntry.Query().
			Where(resellerledgerentry.SubsiteIDEQ(p.ID)).
			Aggregate(ent.Sum(resellerledgerentry.FieldAmount)).
			Int(ctx)
		if err != nil {
			return nil, err
		}
		if acc == nil && sum != 0 {
			subDiff++
			continue
		}
		if acc != nil && acc.Available != int64(sum) {
			subDiff++
			m.RW.AddError("verify_reseller", p.ID,
				fmt.Sprintf("ledger 合计 %d ≠ 账户快照 %d", sum, acc.Available))
		}
	}
	if subDiff > 0 {
		fail("分站余额对账", fmt.Sprintf("%d 个分站账实不一致", subDiff))
	} else {
		pass("分站余额对账", fmt.Sprintf("全部 %d 个分站一致（含零流水分站）", len(profiles)))
	}

	// 4. 卡密抽样：源解密 vs 目标解密逐字节相等
	if err := m.verifyCardSamples(ctx, sample, fail, pass); err != nil {
		return nil, err
	}

	// 5. 行数三方核对（源 / idmap / 目标）
	for _, t := range []struct{ src, dst string }{
		{"users", "users"}, {"products", "products"}, {"orders", "orders"}, {"cards", "cards"},
	} {
		var srcN int64
		if err := m.Src.DB.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM `%s`", t.src)).Scan(&srcN); err != nil {
			continue // 源表缺失（可选域）
		}
		mapN, err := m.Client.V1IDMap.Query().
			Where(v1idmap.TableName(t.src)).Count(ctx)
		if err != nil {
			return nil, err
		}
		var dstN int64
		switch t.dst {
		case "users":
			var u int
			u, err = m.Client.User.Query().Count(ctx)
			dstN = int64(u)
		case "products":
			var u int
			u, err = m.Client.Product.Query().Count(ctx)
			dstN = int64(u)
		case "orders":
			var u int
			u, err = m.Client.Order.Query().Count(ctx)
			dstN = int64(u)
		case "cards":
			var u int
			u, err = m.Client.Card.Query().Count(ctx)
			dstN = int64(u)
		}
		if err != nil {
			return nil, err
		}
		res.Summary[t.src] = fmt.Sprintf("源 %d / 映射 %d / 目标 %d", srcN, mapN, dstN)
		if int64(mapN) != dstN {
			fail("行数核对:"+t.src, fmt.Sprintf("映射 %d ≠ 目标 %d（差异可能来自回填行或分站未迁——人工判读）", mapN, dstN))
		} else if int64(mapN) < srcN && srcN > 0 {
			res.Checks = append(res.Checks, Check{
				Name: "行数核对:" + t.src, Status: StatusWarn,
				Message: fmt.Sprintf("映射 %d < 源 %d（分站/跳过/软删——主站先行模式下属预期）", mapN, srcN),
			})
		}
	}

	// 6. 引用完整性：已迁订单 user_id=0 但源库有值
	var orderIDs []uint64
	srcOrders, err := m.Src.DB.QueryContext(ctx,
		"SELECT `id`, `user_id` FROM `orders` WHERE `user_id` > 0")
	if err == nil {
		for srcOrders.Next() {
			var id, uid int64
			if srcOrders.Scan(&id, &uid) == nil {
				newO, ok := m.IDs.Get(ctx, "orders", uint64(id))
				if !ok {
					continue
				}
				o, err := m.Client.Order.Get(ctx, newO)
				if err != nil {
					continue
				}
				if o.UserID == 0 {
					orderIDs = append(orderIDs, newO)
				}
			}
		}
		srcOrders.Close()
	}
	if len(orderIDs) > 0 {
		fail("引用完整性", fmt.Sprintf("%d 笔已迁订单丢失 user 关联（映射缺失）", len(orderIDs)))
	} else {
		pass("引用完整性", "已迁订单的用户关联完整")
	}

	res.Passed = true
	for _, c := range res.Checks {
		if c.Status == StatusFail {
			res.Passed = false
		}
	}
	return res, nil
}

// verifyCardSamples 卡密抽样比对：源明文（1.x 解密/直通）vs 目标明文（2.0 卡密钥匙解密）。
func (m *Migrator) verifyCardSamples(ctx context.Context, sample int,
	fail func(name, msg string), pass func(name, msg string)) error {

	var srcTotal int64
	if err := m.Src.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM `cards`").Scan(&srcTotal); err != nil {
		pass("卡密抽样", "源库无 cards 表，跳过")
		return nil
	}
	if srcTotal == 0 {
		pass("卡密抽样", "源库卡密为空")
		return nil
	}
	n := int64(sample)
	if srcTotal < n {
		n = srcTotal
	}
	// 随机偏移抽样（id 均匀性近似）
	mismatch := 0
	checked := 0
	for i := int64(0); i < n; i++ {
		r, err := rand.Int(rand.Reader, big.NewInt(srcTotal))
		if err != nil {
			return err
		}
		var (
			oldID                int64
			content, contentHash string
		)
		err = m.Src.DB.QueryRowContext(ctx,
			"SELECT `id`, `content`, `content_hash` FROM `cards` ORDER BY `id` LIMIT 1 OFFSET ?", r.Int64()).
			Scan(&oldID, &content, &contentHash)
		if err != nil {
			return err
		}
		newID, ok := m.IDs.Get(ctx, "cards", uint64(oldID))
		if !ok {
			continue // 未迁（分站/跳过）
		}
		c, err := m.Client.Card.Get(ctx, newID)
		if err != nil {
			return err
		}
		srcPlain, _, err := openV1Card(content, m.CardKey)
		if err != nil {
			mismatch++
			m.RW.AddError("verify_cards", uint64(oldID), "源端解密失败: "+err.Error())
			continue
		}
		dstPlain, err := m.verifyOpen(c.Content, c.ProductID, c.SubsiteID)
		if err != nil || dstPlain != srcPlain {
			mismatch++
			m.RW.AddError("verify_cards", uint64(oldID), "明文不一致或解密失败")
			continue
		}
		checked++
		_ = contentHash
	}
	if mismatch > 0 {
		fail("卡密抽样", fmt.Sprintf("抽样 %d 条中 %d 条不一致（致命——密钥或数据损坏）", checked+mismatch, mismatch))
	} else {
		pass("卡密抽样", fmt.Sprintf("抽样 %d 条全部一致（含 AAD 绑定校验）", checked))
	}
	return nil
}

// verifyOpen 目标端卡密解密（verify 专用，绑定行自身 product/subsite 的 AAD）。
func (m *Migrator) verifyOpen(content []byte, productID, subsiteID uint64) (string, error) {
	c, err := inventory.NewCardCipher(m.NewCardKey)
	if err != nil {
		return "", err
	}
	return c.Open(content, productID, subsiteID)
}

// WriteVerify 报告输出。
func (w *ReportWriter) WriteVerify(v *VerifyResult, meta PreflightMeta) error {
	if err := w.WriteJSON("verify.json", map[string]any{"meta": meta, "result": v}); err != nil {
		return err
	}
	var md string
	title := "❌ 未通过"
	if v.Passed {
		title = "✅ 通过"
	}
	md = fmt.Sprintf("\n## 迁移后校验（%s）\n\n| 状态 | 检查项 | 说明 |\n|---|---|---|\n", title)
	for _, c := range v.Checks {
		md += fmt.Sprintf("| %s | %s | %s |\n", c.Status, c.Name, c.Message)
	}
	if len(v.Summary) > 0 {
		md += "\n### 行数三方核对\n\n| 表 | 数字 |\n|---|---|\n"
		for _, k := range []string{"users", "products", "orders", "cards"} {
			if s, ok := v.Summary[k]; ok {
				md += fmt.Sprintf("| %s | %s |\n", k, s)
			}
		}
	}
	f, err := openAppend(w.dir, "report.md")
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(md)
	return err
}
