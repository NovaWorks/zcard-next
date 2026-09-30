package supply

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"sort"
	"strings"
	"time"
)

// A page is an immutable import scope; it never certifies a complete catalog.
func (s *AdminSupplyService) previewCatalogPage(ctx context.Context, req *adminv1.PreviewProductsRequest) (*adminv1.PreviewProductsReply, error) {
	conn, a, err := s.adapterForConnection(ctx, req.ConnectionId)
	if err != nil {
		return nil, err
	}
	if conn.Driver != "zcard" {
		copy := proto.Clone(req).(*adminv1.PreviewProductsRequest)
		copy.Page = 0
		return s.PreviewProducts(ctx, copy)
	}
	if string(conn.Status) != "active" {
		return nil, fmt.Errorf("货源已停用")
	}
	size := int(req.PageSize)
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 100 || req.Page < 1 {
		return nil, fmt.Errorf("分页范围无效")
	}
	list, err := a.ListProducts(adapter.WithCatalogRead(ctx), int(req.Page), size, true)
	if err != nil {
		return nil, err
	}
	tree, err := a.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := categoryPaths(tree)
	if err != nil {
		return nil, err
	}
	byCode := map[string]adapter.Product{}
	groups := map[string]*adminv1.PreviewCategory{}
	parents := map[string]string{}
	for _, c := range tree {
		parents[c.ID] = c.ParentID
	}
	for _, p := range list.Items {
		if p.ID == "" {
			return nil, fmt.Errorf("上游商品 ID 为空")
		}
		if _, ok := byCode[p.ID]; ok {
			return nil, fmt.Errorf("上游页内商品重复")
		}
		byCode[p.ID] = p
		g := groups[p.CategoryID]
		if g == nil {
			name := paths[p.CategoryID]
			if name == "" {
				name = "未分类"
				if p.CategoryID != "" && p.CategoryID != "0" {
					return nil, fmt.Errorf("上游商品分类缺失：%s", p.CategoryID)
				}
			}
			g = &adminv1.PreviewCategory{Code: p.CategoryID, Name: name, Path: name, ParentCode: parents[p.CategoryID]}
			groups[p.CategoryID] = g
		}
		g.Products = append(g.Products, &adminv1.PreviewProduct{Code: p.ID, Name: p.Name, PriceCents: p.Price, FactoryPriceCents: p.FactoryPrice, CategoryCode: p.CategoryID, CategoryName: g.Name, IsActive: p.IsActive, Stock: p.Stock, DeliveryKind: p.DeliveryKind, SmsProduct: p.SMSProduct})
	}
	cats := []*adminv1.PreviewCategory{}
	for _, g := range groups {
		cats = append(cats, g)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Code < cats[j].Code })
	current, err := s.repo.GetConnection(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	if previewIdentity(current) != previewIdentity(conn) {
		return nil, fmt.Errorf("货源已变化，请重新加载")
	}
	raw, err := json.Marshal(catalogPayload{Categories: cats, Products: byCode, Tree: tree, Page: int(req.Page), PageSize: list.PageSize, Total: list.Total, HasMore: list.HasMore, Capability: "sms_activation.v1"})
	if err != nil {
		return nil, err
	}
	row, err := s.repo.entClient(ctx).SupplyCatalogSnapshot.Create().SetConnectionID(conn.ID).SetSubsiteID(tenancy.FromContext(ctx).SubsiteID).SetToken(uuid.NewString()).SetIdentity(catalogIdentity(conn)).SetExpiresAt(time.Now().Add(catalogSnapshotLifetime).Unix()).SetStatus("ready").SetPayload(raw).SetLoadedCount(len(byCode)).Save(ctx)
	if err != nil {
		return nil, err
	}
	return s.previewSnapshot(ctx, &adminv1.PreviewProductsRequest{ConnectionId: conn.ID, SnapshotId: row.Token})
}
func categoryPaths(tree []adapter.Category) (map[string]string, error) {
	nodes := map[string]adapter.Category{}
	for _, c := range tree {
		if c.ID == "" {
			return nil, fmt.Errorf("上游分类 ID 为空")
		}
		if _, ok := nodes[c.ID]; ok {
			return nil, fmt.Errorf("上游分类 ID 重复")
		}
		nodes[c.ID] = c
	}
	paths := map[string]string{}
	visiting := map[string]bool{}
	var walk func(string, int) (string, error)
	walk = func(id string, depth int) (string, error) {
		if id == "" || id == "0" {
			return "", nil
		}
		if p, ok := paths[id]; ok {
			return p, nil
		}
		if visiting[id] || depth > 100 {
			return "", fmt.Errorf("上游分类父级循环或层级过深")
		}
		c, ok := nodes[id]
		if !ok {
			return "", fmt.Errorf("上游分类父级缺失：%s", id)
		}
		visiting[id] = true
		p, err := walk(c.ParentID, depth+1)
		if err != nil {
			return "", err
		}
		paths[id] = strings.TrimPrefix(p+" / "+c.Name, " / ")
		delete(visiting, id)
		return paths[id], nil
	}
	for id := range nodes {
		if _, err := walk(id, 0); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// Create parents first and match names only under the mapped parent.
func ensurePageCategory(ctx context.Context, c *ent.Client, tenant uint64, id string, tree map[string]adapter.Category, mapped map[string]uint64, leafName string) (uint64, error) {
	if id == "" || id == "0" {
		return 0, nil
	}
	if v := mapped[id]; v > 0 {
		return v, nil
	}
	node, ok := tree[id]
	if !ok {
		return 0, fmt.Errorf("上游分类缺失")
	}
	parent, err := ensurePageCategory(ctx, c, tenant, node.ParentID, tree, mapped, "")
	if err != nil {
		return 0, err
	}
	name := node.Name
	if leafName != "" {
		name = leafName
	}
	q := c.Category.Query().Where(category.SubsiteID(tenant), category.Name(name))
	if parent == 0 {
		q.Where(category.Or(category.ParentID(0), category.ParentIDIsNil()))
	} else {
		q.Where(category.ParentID(parent))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return 0, err
	}
	if len(rows) > 1 {
		return 0, fmt.Errorf("同一路径存在多个同名分类，请手动选择")
	}
	var local uint64
	if len(rows) == 1 {
		local = rows[0].ID
	} else {
		row, e := c.Category.Create().SetSubsiteID(tenant).SetName(name).SetParentID(parent).Save(ctx)
		if e != nil {
			return 0, e
		}
		local = row.ID
	}
	mapped[id] = local
	return local, nil
}
