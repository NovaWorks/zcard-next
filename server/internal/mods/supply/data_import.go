package supply

// P2-10 D：交互式商品导入（预览 + 勾选 + 定价策略 + 类目映射）。
//
//   PreviewProducts  实时经适配器拉全量（≤20 页 = 1000 商品上限，防失控），
//                   按上游分类聚合树并标注 already_imported；60s 进程内缓存
//                   （1.x 同款——避免导入弹窗反复打上游）
//   ImportProducts   勾选 codes → 从预览缓存取商品 → 逐个 upsert（复用 syncOne
//                   的价格保护与映射机制）→ 定价策略四模式 + 类目映射 + 存默认
//
// 定价模式：percent（连接加价%）| fixed（+固定金额）| equal（原价）|
// pending（待定价：不算价、导入后不上架 status=0，运营补价后再上）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
)

// previewCache 连接级预览缓存（60s）。
var previewCache = struct {
	sync.Mutex
	m map[uint64]previewEntry
}{m: map[uint64]previewEntry{}}

type previewEntry struct {
	at        time.Time
	categories []*adminv1.PreviewCategory
	byCode    map[string]adapter.Product
}

const (
	previewTTL      = 60 * time.Second
	previewMaxPages = 20 // ×50/页 = 1000 商品上限
)

// PreviewProducts 上游商品预览（缓存 60s）。
func (s *AdminSupplyService) PreviewProducts(ctx context.Context, req *adminv1.PreviewProductsRequest) (*adminv1.PreviewProductsReply, error) {
	entry, err := s.loadPreview(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	reply := &adminv1.PreviewProductsReply{}
	for _, cat := range entry.categories {
		reply.Categories = append(reply.Categories, cat)
		reply.Total += int32(len(cat.Products))
	}
	return reply, nil
}

// loadPreview 取/建预览缓存。
func (s *AdminSupplyService) loadPreview(ctx context.Context, connectionID uint64) (*previewEntry, error) {
	previewCache.Lock()
	if e, ok := previewCache.m[connectionID]; ok && time.Since(e.at) < previewTTL {
		previewCache.Unlock()
		return &e, nil
	}
	previewCache.Unlock()

	conn, a, err := s.adapterForConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	// 拉全量（≤20 页）
	byCat := map[string][]adapter.Product{}
	catNames := map[string]string{}
	byCode := map[string]adapter.Product{}
	total := 0
	for page := 1; page <= previewMaxPages; page++ {
		list, err := a.ListProducts(ctx, page, 50, true)
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := list.Items[i]
			byCat[p.CategoryID] = append(byCat[p.CategoryID], p)
			byCode[p.ID] = p
			total++
		}
		if !list.HasMore {
			break
		}
	}
	// 分类名（dujiao 支持；acg 空分类走商品内嵌）
	cats, _ := a.ListCategories(ctx)
	for _, c := range cats {
		catNames[c.ID] = c.Name
	}
	// 已导入标注
	imported := map[string]bool{}
	if ms, _, err := s.repo.ListMappings(ctx, connectionID, 1, 100000); err == nil {
		for _, m := range ms {
			if m.UpstreamSku == "" {
				imported[m.UpstreamProduct] = true
			}
		}
	}
	// 聚合（分类顺序稳定：按首个商品出现顺序不可控 → 用 catNames/ID 排序太重，保持 map 迭代 + 排序键）
	entry := &previewEntry{at: time.Now(), byCode: byCode}
	orderedCats := make([]string, 0, len(byCat))
	for c := range byCat {
		orderedCats = append(orderedCats, c)
	}
	sortStrings(orderedCats)
	for _, catID := range orderedCats {
		pc := &adminv1.PreviewCategory{Code: catID, Name: catNames[catID]}
		if pc.Name == "" {
			pc.Name = "分类 " + catID
		}
		for _, p := range byCat[catID] {
			pc.Products = append(pc.Products, &adminv1.PreviewProduct{
				Code: p.ID, Name: p.Name,
				PriceCents: p.Price, FactoryPriceCents: p.FactoryPrice,
				CategoryCode: catID, CategoryName: pc.Name,
				IsActive: p.IsActive, Stock: p.Stock,
				AlreadyImported: imported[p.ID],
			})
		}
		entry.categories = append(entry.categories, pc)
	}
	previewCache.Lock()
	previewCache.m[connectionID] = *entry
	previewCache.Unlock()
	_ = conn
	return entry, nil
}

// ImportProducts 勾选导入。
func (s *AdminSupplyService) ImportProducts(ctx context.Context, req *adminv1.ImportProductsRequest) (*adminv1.ImportProductsReply, error) {
	if len(req.GetCodes()) == 0 {
		return nil, fmt.Errorf("supply: 未勾选任何商品")
	}
	conn, err := s.repo.GetConnection(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	entry, err := s.loadPreview(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	// 定价策略（缺省回退连接默认 settings.import_pricing）
	mode := req.GetPricingMode()
	markupPercent := req.GetMarkupPercent()
	markupAmount := req.GetMarkupAmountCents()
	if mode == "" {
		mode = PriceModePercent
	}
	if mode == PriceModePercent && markupPercent == 0 {
		if def, ok := conn.Settings["import_pricing"].(map[string]any); ok {
			mode, _ = def["mode"].(string)
			markupPercent, _ = def["markup_percent"].(float64)
			markupAmount = toInt64(def["markup_amount_cents"])
		}
	}
	// 类目映射（上游分类 code → 本地分类 id；显式 map 优先，其次连接持久化的
	// settings.category_map，再次既有 mapping 行）
	categoryMap := map[string]uint64{}
	for k, v := range categoryMapFromSettings(conn.Settings) {
		categoryMap[k] = v
	}
	for k, v := range req.GetCategoryMap() {
		if v > 0 {
			categoryMap[k] = v
		}
	}
	ms, _, _ := s.repo.ListMappings(ctx, conn.ID, 1, 100000)
	for _, m := range ms {
		if m.LocalCategoryID > 0 && m.UpstreamCategory != "" {
			if _, ok := categoryMap[m.UpstreamCategory]; !ok {
				categoryMap[m.UpstreamCategory] = m.LocalCategoryID
			}
		}
	}

	reply := &adminv1.ImportProductsReply{}
	for _, code := range req.GetCodes() {
		p, ok := entry.byCode[code]
		if !ok {
			reply.Failed++
			continue
		}
		if _, err := s.sync.ImportOne(ctx, conn, &p, categoryMap, mode, markupPercent, markupAmount); err != nil {
			reply.Failed++
			if reply.ErrorContext == "" {
				reply.ErrorContext = fmt.Sprintf("code=%s: %v", code, err)
			}
			continue
		}
		if _, err := s.repo.GetMapping(ctx, conn.ID, code, ""); err == ErrNotFound {
			reply.Imported++
		} else {
			reply.Updated++
		}
	}
	// 连接级持久化：导入默认价（save_default）+ 类目映射（settings.category_map，
	// 与本次实际生效的合并结果一并落库——后续全量同步自动套用同一映射）
	settingsDirty := false
	settings := conn.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	if req.GetSaveDefault() {
		settings["import_pricing"] = map[string]any{
			"mode": mode, "markup_percent": markupPercent, "markup_amount_cents": markupAmount,
		}
		settingsDirty = true
	}
	if len(req.GetCategoryMap()) > 0 {
		merged := map[string]any{}
		for k, v := range categoryMap {
			merged[k] = v
		}
		settings["category_map"] = merged
		settingsDirty = true
	}
	if settingsDirty {
		if _, err := s.repo.entClient(ctx).SupplyConnection.UpdateOneID(conn.ID).SetSettings(settings).Save(ctx); err != nil {
			return nil, fmt.Errorf("supply: 保存连接默认失败: %w", err)
		}
	}
	// 预览缓存失效（导入后 already_imported 标注需刷新）
	previewCache.Lock()
	delete(previewCache.m, conn.ID)
	previewCache.Unlock()
	return reply, nil
}

// adapterForConnection 连接 → 适配器装配（预览/导入共用）。
func (s *AdminSupplyService) adapterForConnection(ctx context.Context, connectionID uint64) (*ent.SupplyConnection, adapter.Adapter, error) {
	conn, err := s.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, nil, err
	}
	credsJSON, err := s.repo.OpenCredentials(conn)
	if err != nil {
		return nil, nil, err
	}
	var creds adapter.Credentials
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		return nil, nil, err
	}
	a, err := adapter.New(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
	if err != nil {
		return nil, nil, err
	}
	return conn, a, nil
}

func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}
