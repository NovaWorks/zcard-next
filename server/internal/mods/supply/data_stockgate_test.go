package supply

// P2-02 T4 下单前上游库存预检决策矩阵：非上游跳过、明确不足拒单、
// 库存未知(-1)/查询失败/商品不可读 fail-open 放行、SKU 上游标识透传。

import (
	"context"
	"errors"
	"testing"

	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	orderport "github.com/NovaWorks/zcard-next/server/internal/mods/order/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// fakeGateReader ProductReader 桩（仅 Get/SkuUpstreamCode 有意义）。
type fakeGateReader struct {
	prods map[uint64]*catalogport.Product
	skus  map[uint64]string
}

func (r fakeGateReader) ListVisible(ctx context.Context, f catalogport.VisibleFilter) ([]catalogport.Product, int64, error) {
	return nil, 0, errors.New("not implemented")
}
func (r fakeGateReader) Get(ctx context.Context, subsiteID, id uint64) (*catalogport.Product, error) {
	if p, ok := r.prods[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}
func (r fakeGateReader) SkuUpstreamCode(ctx context.Context, subsiteID, skuID uint64) string {
	return r.skus[skuID]
}

func TestCheckOrderItems(t *testing.T) {
	ctx := context.Background()
	reader := fakeGateReader{
		prods: map[uint64]*catalogport.Product{
			1: {ID: 1, Name: "本地卡密", StockType: "card"},
			2: {ID: 2, Name: "上游商品", StockType: "card", UpstreamSourceID: 9, UpstreamProductCode: "UP-2"},
			3: {ID: 3, Name: "直发链接", StockType: "url"},
		},
		skus: map[uint64]string{77: "race=1|color=红"},
	}

	cases := []struct {
		name      string
		items     []orderport.UpstreamStockItem
		stock     int32
		stockErr  error
		wantCall  bool
		wantCode  string // 期望透传的上游规格标识
		wantOrder bool   // true = 应放行（nil）
	}{
		{name: "非上游项跳过", items: []orderport.UpstreamStockItem{{ProductID: 1, Quantity: 3}}, wantCall: false, wantOrder: true},
		{name: "直发项跳过", items: []orderport.UpstreamStockItem{{ProductID: 3, Quantity: 3}}, wantCall: false, wantOrder: true},
		{name: "上游充足放行", items: []orderport.UpstreamStockItem{{ProductID: 2, Quantity: 3}}, stock: 5, wantCall: true, wantOrder: true},
		{name: "上游不足拒单", items: []orderport.UpstreamStockItem{{ProductID: 2, Quantity: 3}}, stock: 2, wantCall: true, wantOrder: false},
		{name: "库存未知放行", items: []orderport.UpstreamStockItem{{ProductID: 2, Quantity: 3}}, stock: -1, wantCall: true, wantOrder: true},
		{name: "查询失败放行", items: []orderport.UpstreamStockItem{{ProductID: 2, Quantity: 3}}, stockErr: errors.New("boom"), wantCall: true, wantOrder: true},
		{name: "SKU标识透传", items: []orderport.UpstreamStockItem{{ProductID: 2, SkuID: 77, Quantity: 1}}, stock: 9, wantCall: true, wantCode: "race=1|color=红", wantOrder: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var gotCode string
			stock := func(connectionID uint64, productCode, skuCode string) (int32, error) {
				called = true
				gotCode = skuCode
				if connectionID != 9 || productCode != "UP-2" {
					t.Fatalf("取数参数错误: conn=%d code=%s", connectionID, productCode)
				}
				return tc.stock, tc.stockErr
			}
			err := checkOrderItems(ctx, reader, stock, 0, tc.items)
			if called != tc.wantCall {
				t.Fatalf("取数调用=%v want %v", called, tc.wantCall)
			}
			if gotCode != tc.wantCode {
				t.Fatalf("SKU 透传=%q want %q", gotCode, tc.wantCode)
			}
			if tc.wantOrder && err != nil {
				t.Fatalf("应放行: %v", err)
			}
			if !tc.wantOrder {
				if !errors.Is(err, port.ErrUpstreamNoStock) {
					t.Fatalf("应拒绝且归因无库存: %v", err)
				}
			}
		})
	}

	// 商品不可读 → fail-open
	if err := checkOrderItems(ctx, reader, func(uint64, string, string) (int32, error) { return 0, nil }, 0,
		[]orderport.UpstreamStockItem{{ProductID: 404, Quantity: 1}}); err != nil {
		t.Fatalf("不可读商品应放行: %v", err)
	}
}
