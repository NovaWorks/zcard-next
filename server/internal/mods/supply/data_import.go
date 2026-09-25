package supply

// D：交互式商品导入（预览 + 勾选 + 定价策略 + 类目映射）。
//
// PreviewProducts 实时经适配器拉目录（分页协议最多 100 页），
// 按上游分类聚合树并标注 already_imported；60s 进程内缓存
// （1.x 同款——避免导入弹窗反复打上游）
// ImportProducts 将勾选快照、定价和类目映射持久化为后台任务；实现见
// data_import_task.go / data_import_worker.go，逐商品保存检查点并自动恢复。
//
// 定价模式：channel（跟随渠道）| percent（本次加价%）| fixed（+固定金额）| equal（原价）|
// pending（待定价：不算价、导入后不上架 status=0，运营补价后再上）。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"google.golang.org/protobuf/proto"
)

// previewCache 连接级预览缓存（60s）。
var previewCache = struct {
	sync.Mutex
	m map[uint64]previewEntry
}{m: map[uint64]previewEntry{}}

type previewEntry struct {
	identity   [32]byte
	at         time.Time
	categories []*adminv1.PreviewCategory
	byCode     map[string]adapter.Product
}

const (
	previewTTL      = 60 * time.Second
	previewMaxPages = 100 // ×50/页 = 5000 商品上限（导入预览须覆盖目标站全部分类与商品）
)

// PreviewProducts 上游商品预览（缓存 60s）。
func (s *AdminSupplyService) PreviewProducts(ctx context.Context, req *adminv1.PreviewProductsRequest) (*adminv1.PreviewProductsReply, error) {
	if req.GetAsync() || req.GetSnapshotId() != "" {
		return s.previewSnapshot(ctx, req)
	}
	entry, err := s.loadPreview(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	conn, a, err := s.adapterForConnection(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	return s.pricePreview(ctx, conn, a, entry, req.GetQuoteCode())
}

// pricePreview never writes quotes into the shared catalog cache. Expanding the
// UI quotes one product at a time, rather than blocking a large catalog load.
func (s *AdminSupplyService) pricePreview(ctx context.Context, conn *ent.SupplyConnection, a adapter.Adapter, entry *previewEntry, code string) (*adminv1.PreviewProductsReply, error) {
	if entry.identity != previewIdentity(conn) {
		return nil, fmt.Errorf("货源账号已变化，请刷新商品目录")
	}
	if code != "" {
		if _, ok := entry.byCode[code]; !ok {
			return nil, fmt.Errorf("商品已不在预览目录，请重新加载")
		}
	}
	quoter, needsQuote := a.(adapter.AccountQuoter)
	reply := &adminv1.PreviewProductsReply{}
	locals, err := s.repo.entClient(ctx).Product.Query().Where(product.UpstreamSourceID(conn.ID), product.StatusGTE(0), product.SubsiteID(tenancy.FromContext(ctx).SubsiteID)).All(ctx)
	if err != nil {
		return nil, err
	}
	locked := map[string]bool{}
	protected := map[string]bool{}
	localCats := map[string]uint64{}
	for _, p := range locals {
		locked[p.UpstreamProductCode] = p.IsLocked
		protected[p.UpstreamProductCode] = p.CategoryProtected
		localCats[p.UpstreamProductCode] = p.CategoryID
	}
	for _, cat := range entry.categories {
		out := &adminv1.PreviewCategory{Code: cat.Code, Name: cat.Name}
		for _, cached := range cat.Products {
			if code != "" && cached.Code != code {
				continue
			}
			p := proto.Clone(cached).(*adminv1.PreviewProduct)
			_, p.AlreadyImported = localCats[p.Code]
			p.IsLocked = locked[p.Code]
			p.CategoryProtected = protected[p.Code]
			p.LocalCategoryId = localCats[p.Code]
			source := entry.byCode[p.Code]
			p.QuoteStatus, p.CostPriceCents = "pending", -1
			if needsQuote {
				p.PriceCents, p.FactoryPriceCents = -1, -1
				if code != "" {
					quoteCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					quoted, err := quoter.QuoteProduct(quoteCtx, &source)
					cancel()
					if err == nil && quoted != nil {
						source = *quoted
						p.PriceCents, p.FactoryPriceCents = source.Price, source.FactoryPrice
					} else {
						p.QuoteStatus = "failed"
					}
				}
			}
			if !needsQuote || (code != "" && p.QuoteStatus != "failed") {
				p.CostPriceCents = accountCost(conn, &source)
				p.QuoteStatus = "ready"
				p.CostIsMinimum = len(source.SKUs) > 0
				if p.CostPriceCents < 0 {
					p.QuoteStatus = "failed"
				}
			}
			out.Products = append(out.Products, p)
			reply.Total++
		}
		if len(out.Products) > 0 {
			reply.Categories = append(reply.Categories, out)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.repo.GetConnection(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	if previewIdentity(current) != previewIdentity(conn) || current.ExchangeRate != conn.ExchangeRate {
		return nil, fmt.Errorf("货源账号或汇率已变化，请重新加载商品目录")
	}
	return reply, nil
}

// loadPreview 取/建预览缓存。
func previewIdentity(conn *ent.SupplyConnection) [32]byte {
	return sha256.Sum256([]byte(conn.Driver + "\x00" + conn.BaseURL + "\x00" + string(conn.Credentials)))
}

func (s *AdminSupplyService) loadPreview(ctx context.Context, connectionID uint64) (*previewEntry, error) {
	current, err := s.repo.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	previewCache.Lock()
	if e, ok := previewCache.m[connectionID]; ok && e.identity == previewIdentity(current) && time.Since(e.at) < previewTTL {
		previewCache.Unlock()
		return &e, nil
	}
	previewCache.Unlock()

	entry, err := s.fetchPreview(ctx, connectionID, nil)
	if err == nil {
		previewCache.Lock()
		previewCache.m[connectionID] = *entry
		previewCache.Unlock()
	}
	return entry, err
}

func (s *AdminSupplyService) fetchPreview(ctx context.Context, connectionID uint64, progress func(int)) (*previewEntry, error) {
	conn, a, err := s.adapterForConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	// 预览优先只拉目录，避免 ACG 皮肤站逐品 inventory 查询拖垮大目录。
	byCat := map[string][]adapter.Product{}
	catNames := map[string]string{}
	byCode := map[string]adapter.Product{}
	var categories []adapter.Category
	for page := 1; page <= previewMaxPages; page++ {
		var list *adapter.ProductList
		var err error
		if previewer, ok := a.(adapter.ImportPreviewer); ok {
			list, err = previewer.PreviewProducts(ctx)
		} else {
			list, err = a.ListProducts(ctx, page, 50, true)
		}
		if err != nil {
			return nil, err
		}
		if list.Categories != nil {
			categories = list.Categories
		}
		for i := range list.Items {
			p := list.Items[i]
			byCat[p.CategoryID] = append(byCat[p.CategoryID], p)
			byCode[p.ID] = p
		}
		if progress != nil {
			progress(len(byCode))
		}
		if len(byCode) > 50000 {
			return nil, catalogLoadError("商品目录超过 50000 件，请缩小货源目录范围")
		}
		if !list.HasMore {
			break
		}
		if page == previewMaxPages {
			return nil, catalogLoadError("商品目录超过最大页数，未保存不完整目录，请缩小货源目录范围")
		}
	}
	// ACG 已携带分类，避免为分类名再次下载整个商品目录。
	if categories == nil {
		categories, err = a.ListCategories(ctx)
		if err != nil {
			return nil, err
		}
	}
	for _, c := range categories {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 聚合（分类顺序稳定：按首个商品出现顺序不可控 → 用 catNames/ID 排序太重，保持 map 迭代 + 排序键）
	entry := &previewEntry{at: time.Now(), byCode: byCode, identity: previewIdentity(conn)}
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
	return entry, nil
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
	factory := s.adapterFactory
	if factory == nil {
		factory = adapter.New
	}
	a, err := factory(conn.Driver, conn.BaseURL, creds, parseRetryIntervals(conn.RetryIntervals))
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
