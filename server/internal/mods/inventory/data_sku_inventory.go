package inventory

import (
	"context"
	"crypto/sha256"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	"sort"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/card"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/cardimport"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/predicate"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/securityauditlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

const assignmentLimit = 10000

func (r *CardRepoImpl) inventoryProduct(ctx context.Context, id uint64) (*ent.Product, error) {
	if id == 0 {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "请选择商品")
	}
	p, err := data.Client(ctx, r.data).Product.Query().Where(product.ID(id), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID), product.StatusGTE(0)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("inventory.PRODUCT_NOT_FOUND", "商品不存在或已删除")
	}
	return p, err
}

func (r *CardRepoImpl) inventorySkus(ctx context.Context, p *ent.Product, locked bool) ([]*ent.ProductSku, error) {
	q := data.Client(ctx, r.data).ProductSku.Query().Where(productsku.ProductID(p.ID), productsku.SubsiteID(p.SubsiteID)).Order(ent.Asc(productsku.FieldID))
	if locked && r.data.Dialect.Capabilities().SupportsSkipLocked {
		q = q.ForUpdate()
	}
	return q.All(ctx)
}

func importEligibility(p *ent.Product, sku *ent.ProductSku) string {
	if p.IsLocked {
		return "商品已锁定，请先解锁"
	}
	if p.ProductKind == "sms_channel" {
		return "渠道接码商品不使用卡密库存"
	}
	if p.GoodsType == "physical" {
		return "实体商品不能导入卡密，请调整实物库存"
	}
	if p.StockType != "card" {
		return "链接或兑换码商品无需导入卡密，请维护直发内容"
	}
	switch data.StockSource(p, sku) {
	case "local":
		return ""
	case "manual":
		return "人工交付规格不使用自动卡密库存"
	case "reuse":
		return "重复发货规格请在发货设置中维护内容"
	default:
		return "该规格使用上游采购，请先在发货设置中切换为本地卡密"
	}
}

func (r *CardRepoImpl) importTarget(ctx context.Context, p *ent.Product, skuID uint64, locked bool) (string, error) {
	skus, err := r.inventorySkus(ctx, p, locked)
	if err != nil {
		return "", err
	}
	var selected *ent.ProductSku
	name := "默认规格"
	if len(skus) > 0 && skuID == 0 {
		return "", errors.BadRequest("inventory.SKU_REQUIRED", "该商品有规格，请明确选择导入规格")
	}
	if skuID > 0 {
		for _, sku := range skus {
			if sku.ID == skuID {
				selected = sku
				name = sku.Name
				break
			}
		}
		if selected == nil {
			return "", errors.BadRequest("inventory.SKU_INVALID", "规格不存在或不属于该商品")
		}
	}
	if reason := importEligibility(p, selected); reason != "" {
		code := "inventory.TARGET_UNAVAILABLE"
		if p.GoodsType == "physical" {
			code = "inventory.PHYSICAL_PRODUCT"
		}
		if p.IsLocked {
			return "", data.ErrProductLocked
		}
		return "", errors.BadRequest(code, reason)
	}
	return name, nil
}

func inventoryTransportError(err error, reason, message string) error {
	if e := errors.FromError(err); e.Code < 500 {
		return err
	}
	return errors.InternalServer(reason, message)
}

func skuPredicate(sku *uint64) predicate.Card {
	if sku == nil {
		return func(*entsql.Selector) {}
	}
	if *sku == 0 {
		return card.Or(card.SkuIDIsNil(), card.SkuID(0))
	}
	return card.SkuID(*sku)
}

func (s *AdminInventoryService) skuNames(ctx context.Context, productID, subsiteID uint64) (map[uint64]string, error) {
	rows, err := data.Client(ctx, s.data).ProductSku.Query().Where(productsku.ProductID(productID), productsku.SubsiteID(subsiteID)).Select(productsku.FieldID, productsku.FieldName).All(ctx)
	out := map[uint64]string{0: "商品默认池"}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, err
}
func skuLabel(names map[uint64]string, id uint64) string {
	if name, ok := names[id]; ok {
		return name
	}
	return fmt.Sprintf("已删除规格 #%d", id)
}

// GetTargets returns only inventory metadata, without catalog prices or costs.
func (s *AdminInventoryService) GetTargets(ctx context.Context, req *adminv1.InventoryTargetsRequest) (*adminv1.InventoryTargetsReply, error) {
	p, err := s.repo.inventoryProduct(ctx, req.GetProductId())
	if err != nil {
		return nil, inventoryTransportError(err, "inventory.LIST_FAILED", "读取商品失败")
	}
	skus, err := s.repo.inventorySkus(ctx, p, false)
	if err != nil {
		return nil, errors.InternalServer("inventory.LIST_FAILED", "读取规格失败")
	}
	reply := &adminv1.InventoryTargetsReply{ProductId: p.ID, ProductName: p.Name, Revision: p.LockVersion, HasSkus: len(skus) > 0}
	def := &adminv1.InventoryTarget{SkuId: 0, SkuName: "商品默认池", Reason: importEligibility(p, nil)}
	if len(skus) > 0 {
		def.Reason = "该商品有规格，默认池仅供查看和历史归属，请选择具体规格导入"
	}
	def.CanImport = def.Reason == ""
	reply.Targets = append(reply.Targets, def)
	byID := map[uint64]*adminv1.InventoryTarget{0: def}
	for _, sku := range skus {
		reason := importEligibility(p, sku)
		target := &adminv1.InventoryTarget{SkuId: sku.ID, SkuName: sku.Name, CanImport: reason == "", Reason: reason}
		reply.Targets = append(reply.Targets, target)
		byID[sku.ID] = target
	}
	counts, err := inventoryCounts(ctx, data.Client(ctx, s.data).Card.Query().Where(card.ProductID(p.ID), card.SubsiteID(p.SubsiteID)), false)
	if err != nil {
		return nil, errors.InternalServer("inventory.LIST_FAILED", "读取库存失败")
	}
	for _, row := range counts {
		id := row.skuID()
		if byID[id] == nil {
			target := &adminv1.InventoryTarget{SkuId: id, SkuName: fmt.Sprintf("已删除规格 #%d", id), Reason: "规格已删除，存量仅供查看"}
			byID[id] = target
			reply.Targets = append(reply.Targets, target)
		}
		if target := byID[id]; target != nil {
			switch row.Status {
			case "available":
				target.Available += row.Count
			case "reserved":
				target.Reserved += row.Count
			case "disabled":
				target.Disabled += row.Count
			}
		}
	}
	sort.Slice(reply.Targets, func(i, j int) bool { return reply.Targets[i].SkuId < reply.Targets[j].SkuId })
	return reply, nil
}

func actorID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

func eligibleUnassigned() []predicate.Card {
	return []predicate.Card{card.Or(card.SkuIDIsNil(), card.SkuID(0)), card.StatusEQ(card.StatusAvailable), card.OwnerID(0), card.Or(card.OrderIDIsNil(), card.OrderID(0)), card.LockedAtIsNil()}
}
func uniqueCardIDs(ids []uint64) ([]uint64, error) {
	if len(ids) == 0 || len(ids) > assignmentLimit {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "请选择 1–10000 张卡密")
	}
	out := append([]uint64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i, id := range out {
		if id == 0 || (i > 0 && out[i-1] == id) {
			return nil, errors.BadRequest("inventory.INVALID_INPUT", "卡密 ID 不能为空或重复")
		}
	}
	return out, nil
}
func (s *AdminInventoryService) AssignPreview(ctx context.Context, req *adminv1.AssignPreviewRequest) (*adminv1.AssignPreviewReply, error) {
	p, err := s.repo.inventoryProduct(ctx, req.GetProductId())
	if err != nil {
		return nil, inventoryTransportError(err, "inventory.PREVIEW_FAILED", "读取商品失败")
	}
	if req.GetSkuId() == 0 {
		return nil, errors.BadRequest("inventory.SKU_REQUIRED", "请选择归属的具体规格")
	}
	name, err := s.repo.importTarget(ctx, p, req.GetSkuId(), false)
	if err != nil {
		return nil, inventoryTransportError(err, "inventory.PREVIEW_FAILED", "规格不可用")
	}
	if (len(req.CardIds) > 0) == (req.GetImportId() > 0) {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "请选择卡密或一个原始导入批次")
	}
	c := data.Client(ctx, s.data)
	base := c.Card.Query().Where(card.ProductID(p.ID), card.SubsiteID(p.SubsiteID))
	if req.GetImportId() > 0 {
		if _, err = c.CardImport.Query().Where(cardimport.ID(req.GetImportId()), cardimport.ProductID(p.ID), cardimport.SubsiteID(p.SubsiteID)).Only(ctx); err != nil {
			return nil, errors.NotFound("inventory.BATCH_NOT_FOUND", "导入批次不存在")
		}
		base = base.Where(card.ImportID(req.GetImportId()))
	} else {
		ids, e := uniqueCardIDs(req.CardIds)
		if e != nil {
			return nil, e
		}
		base = base.Where(card.IDIn(ids...))
	}
	total, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := base.Clone().Where(eligibleUnassigned()...).Order(ent.Asc(card.FieldID)).Limit(assignmentLimit + 1).IDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) > assignmentLimit {
		return nil, errors.BadRequest("inventory.TOO_MANY_CARDS", "单次最多归属 10000 张卡密，请分批选择")
	}
	blocked, err := lotteryCardReferences(ctx, c, ids)
	if err != nil {
		return nil, err
	}
	allowed := ids[:0]
	for _, id := range ids {
		if !blocked[id] {
			allowed = append(allowed, id)
		}
	}
	ids = allowed
	if req.GetImportId() == 0 && (total != len(req.CardIds) || len(ids) != total) {
		return nil, errors.Conflict("inventory.CARDS_CHANGED", "所选卡密不属于该商品、已归属规格或已被使用，请刷新")
	}
	return &adminv1.AssignPreviewReply{CardIds: ids, EligibleCount: int64(len(ids)), ExcludedCount: int64(total - len(ids)), Revision: p.LockVersion, SkuName: name}, nil
}
func (s *AdminInventoryService) AssignCards(ctx context.Context, req *adminv1.AssignCardsRequest) (*adminv1.AssignCardsReply, error) {
	ids, err := uniqueCardIDs(req.CardIds)
	if err != nil {
		return nil, err
	}
	if len(req.GetRequestKey()) > 128 {
		return nil, errors.BadRequest("inventory.INVALID_INPUT", "请求标识过长")
	}
	if req.ExpectedRevision == nil {
		return nil, errors.BadRequest("inventory.PREVIEW_REQUIRED", "请先预览归属结果")
	}
	if req.GetSkuId() == 0 {
		return nil, errors.BadRequest("inventory.SKU_REQUIRED", "请选择归属的具体规格")
	}
	err = data.Tx(ctx, s.data, func(ctx context.Context) error {
		p, e := data.GuardProductWrite(ctx, s.data, req.GetProductId())
		if e != nil {
			return e
		}
		if p.LockVersion != req.GetExpectedRevision() {
			return errors.Conflict("inventory.CONFIG_CHANGED", "商品设置已变化，请重新预览")
		}
		if _, e = s.repo.importTarget(ctx, p, req.GetSkuId(), true); e != nil {
			return e
		}
		c := data.Client(ctx, s.data)
		q := c.Card.Query().Where(card.IDIn(ids...), card.ProductID(p.ID), card.SubsiteID(p.SubsiteID)).Order(ent.Asc(card.FieldID))
		if s.data.Dialect.Capabilities().SupportsSkipLocked {
			q = q.ForUpdate()
		}
		rows, e := q.All(ctx)
		if e != nil {
			return e
		}
		if len(rows) != len(ids) {
			return errors.Conflict("inventory.CARDS_CHANGED", "卡密归属已变化，请刷新后重试")
		}
		for _, row := range rows {
			if row.SkuID != 0 || row.Status != card.StatusAvailable || row.OwnerID != 0 || row.OrderID != 0 || !row.LockedAt.IsZero() {
				return errors.Conflict("inventory.CARDS_CHANGED", "卡密已归属、预占或使用，请刷新后重试")
			}
		}
		references, e := lotteryCardReferences(ctx, c, ids)
		if e != nil {
			return e
		}
		if len(references) > 0 {
			return errors.Conflict("inventory.CARDS_CHANGED", "卡密已有发奖记录，不能更改归属")
		}
		predicates := append(eligibleUnassigned(), card.IDIn(ids...), card.ProductID(p.ID), card.SubsiteID(p.SubsiteID))
		n, e := c.Card.Update().Where(predicates...).SetSkuID(req.GetSkuId()).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != len(ids) {
			return errors.Conflict("inventory.CARDS_CHANGED", "卡密状态已变化，未执行归属")
		}
		// Preserve import_id and import target snapshots; assignment has its own audit.
		_, e = c.SecurityAuditLog.Create().SetActorType(securityauditlog.ActorTypeAdmin).SetNillableActorID(nilOr(actorID(ctx))).SetAction("inventory.assign_sku").SetMetadata(map[string]any{"product_id": p.ID, "subsite_id": p.SubsiteID, "from_sku_id": 0, "sku_id": req.GetSkuId(), "card_ids": ids, "assigned": n, "request_key_hash": fmt.Sprintf("%x", sha256.Sum256([]byte(req.GetRequestKey())))}).Save(ctx)
		return e
	})
	if err != nil {
		return nil, inventoryTransportError(err, "inventory.ASSIGN_FAILED", "归属失败")
	}
	return &adminv1.AssignCardsReply{Assigned: int64(len(ids))}, nil
}

// batchInfos distinguishes immutable import targets from current card locations.
func (s *AdminInventoryService) batchInfos(ctx context.Context, imports []*ent.CardImport) ([]*adminv1.ImportBatch, error) {
	if len(imports) == 0 {
		return nil, nil
	}
	c := data.Client(ctx, s.data)
	site := tenancy.FromContext(ctx).SubsiteID
	ids, productIDs := []uint64{}, []uint64{}
	for _, imp := range imports {
		ids = append(ids, imp.ID)
		productIDs = append(productIDs, imp.ProductID)
	}
	products, e := c.Product.Query().Where(product.IDIn(productIDs...), product.SubsiteID(site)).Select(product.FieldID, product.FieldName).All(ctx)
	if e != nil {
		return nil, e
	}
	productNames := map[uint64]string{}
	for _, p := range products {
		productNames[p.ID] = p.Name
	}
	skus, e := c.ProductSku.Query().Where(productsku.ProductIDIn(productIDs...), productsku.SubsiteID(site)).Select(productsku.FieldID, productsku.FieldName).All(ctx)
	if e != nil {
		return nil, e
	}
	names := map[uint64]string{0: "商品默认池"}
	for _, sku := range skus {
		names[sku.ID] = sku.Name
	}
	counts, e := inventoryCounts(ctx, c.Card.Query().Where(card.ImportIDIn(ids...), card.SubsiteID(site)), true)
	if e != nil {
		return nil, e
	}
	groups := map[uint64]map[uint64]*adminv1.InventorySkuCount{}
	for _, row := range counts {
		if groups[row.ImportID] == nil {
			groups[row.ImportID] = map[uint64]*adminv1.InventorySkuCount{}
		}
		id := row.skuID()
		g := groups[row.ImportID][id]
		if g == nil {
			g = &adminv1.InventorySkuCount{SkuId: id, SkuName: skuLabel(names, id)}
			groups[row.ImportID][id] = g
		}
		switch row.Status {
		case "available":
			g.Available += row.Count
		case "reserved":
			g.Reserved += row.Count
		case "used":
			g.Used += row.Count
		case "disabled":
			g.Disabled += row.Count
		}
	}
	out := make([]*adminv1.ImportBatch, 0, len(imports))
	for _, imp := range imports {
		pb := toImportBatch(imp)
		pb.ProductName = productNames[imp.ProductID]
		for _, g := range groups[imp.ID] {
			pb.Remaining = append(pb.Remaining, g)
		}
		sort.Slice(pb.Remaining, func(i, j int) bool { return pb.Remaining[i].SkuId < pb.Remaining[j].SkuId })
		out = append(out, pb)
	}
	return out, nil
}
func cancellationRevision(rows []*ent.Card) string {
	var b strings.Builder
	for _, row := range rows {
		lockedAt := int64(0)
		if !row.LockedAt.IsZero() {
			lockedAt = row.LockedAt.UnixNano()
		}
		fmt.Fprintf(&b, "%d:%d:%s:%d:%d:%d:%d;", row.ID, row.SkuID, row.Status, row.Version, row.OwnerID, row.OrderID, lockedAt)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}
func (r *CardRepoImpl) batchCards(ctx context.Context, imp *ent.CardImport, locked bool) ([]*ent.Card, error) {
	q := data.Client(ctx, r.data).Card.Query().Where(card.ImportID(imp.ID), card.ProductID(imp.ProductID), card.SubsiteID(imp.SubsiteID)).Select(card.FieldID, card.FieldSkuID, card.FieldStatus, card.FieldVersion, card.FieldOwnerID, card.FieldOrderID, card.FieldLockedAt).Order(ent.Asc(card.FieldID))
	if locked && r.data.Dialect.Capabilities().SupportsSkipLocked {
		q = q.ForUpdate()
	}
	return q.All(ctx)
}
func (s *AdminInventoryService) PreviewCancelImport(ctx context.Context, req *adminv1.CancelImportRequest) (*adminv1.CancelImportPreviewReply, error) {
	imp, err := data.Client(ctx, s.data).CardImport.Query().Where(cardimport.ID(req.GetId()), cardimport.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.NotFound("inventory.BATCH_NOT_FOUND", "导入批次不存在")
	}
	if err != nil {
		return nil, err
	}
	if _, err = s.repo.inventoryProduct(ctx, imp.ProductID); err != nil {
		return nil, err
	}
	rows, err := s.repo.batchCards(ctx, imp, false)
	if err != nil {
		return nil, err
	}
	infos, err := s.batchInfos(ctx, []*ent.CardImport{imp})
	if err != nil {
		return nil, err
	}
	names, err := s.skuNames(ctx, imp.ProductID, imp.SubsiteID)
	if err != nil {
		return nil, err
	}
	groups := map[uint64]*adminv1.InventorySkuCount{}
	for _, row := range rows {
		g := groups[row.SkuID]
		if g == nil {
			g = &adminv1.InventorySkuCount{SkuId: row.SkuID, SkuName: skuLabel(names, row.SkuID)}
			groups[row.SkuID] = g
		}
		switch row.Status {
		case card.StatusAvailable:
			g.Available++
		case card.StatusReserved:
			g.Reserved++
		case card.StatusUsed:
			g.Used++
		case card.StatusDisabled:
			g.Disabled++
		}
	}
	infos[0].Remaining = nil
	for _, g := range groups {
		infos[0].Remaining = append(infos[0].Remaining, g)
	}
	sort.Slice(infos[0].Remaining, func(i, j int) bool { return infos[0].Remaining[i].SkuId < infos[0].Remaining[j].SkuId })
	return &adminv1.CancelImportPreviewReply{Batch: infos[0], Revision: cancellationRevision(rows)}, nil
}

// Count in SQL rather than loading encrypted card content for inventory metadata.
type inventoryCount struct {
	ImportID uint64  `json:"import_id"`
	SkuID    *uint64 `json:"sku_id"`
	Status   string  `json:"status"`
	Count    int64   `json:"count"`
}

func (c inventoryCount) skuID() uint64 {
	if c.SkuID == nil {
		return 0
	}
	return *c.SkuID
}
func inventoryCounts(ctx context.Context, q *ent.CardQuery, byImport bool) ([]inventoryCount, error) {
	fields := []string{card.FieldSkuID, card.FieldStatus}
	if byImport {
		fields = append(fields, card.FieldImportID)
	}
	var rows []inventoryCount
	err := q.GroupBy(fields[0], fields[1:]...).Aggregate(ent.Count()).Scan(ctx, &rows)
	return rows, err
}

func lotteryCardReferences(ctx context.Context, c *ent.Client, ids []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	for start := 0; start < len(ids); start += dedupChunk {
		end := start + dedupChunk
		if end > len(ids) {
			end = len(ids)
		}
		refs, err := c.LotteryDraw.Query().Where(lotterydraw.CardIDIn(ids[start:end]...), lotterydraw.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).Select(lotterydraw.FieldCardID).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if ref.CardID != nil {
				out[*ref.CardID] = true
			}
		}
	}
	return out, nil
}
