package settings

// 前台公开配置服务 + 多币种读取（P0-04 T3）。
// 白名单输出（SECRET 键物理隔离）；admin 货币 CRUD 在 service.go（AdminCurrencyService）。

import (
	"context"
	"sort"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StorefrontConfigService 前台配置服务（实现 storefrontv1）。
type StorefrontConfigService struct {
	storefrontv1.UnimplementedStorefrontConfigServiceServer
	repo Repo
}

// NewStorefrontConfigService 构造。
func NewStorefrontConfigService(repo Repo) *StorefrontConfigService {
	return &StorefrontConfigService{repo: repo}
}

// GetPublicConfig 白名单输出：DB 值优先，无值回落目录默认值；仅 PUBLIC_KEYS 键；
// SECRET 键双保险（即使误入 PublicKeys 也不下发）。
func (s *StorefrontConfigService) GetPublicConfig(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.PublicConfig, error) {
	type entry struct{ key, val string }
	var out []entry
	for _, gname := range GroupsSorted() {
		g, _ := Group(gname)
		keys := PublicKeysOf(gname)
		sort.Strings(keys)
		for _, k := range keys {
			if IsSecret(gname, k) {
				continue
			}
			val, _ := g.DefaultJSON(k)
			if raw, err := s.repo.Get(ctx, gname, k); err == nil && raw != nil {
				val = string(raw)
			}
			out = append(out, entry{gname + "." + k, val})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	reply := &storefrontv1.PublicConfig{}
	for _, e := range out {
		reply.Entries = append(reply.Entries, &storefrontv1.PublicConfigEntry{Key: e.key, ValueJson: e.val})
	}
	return reply, nil
}

// ListCurrencies 启用货币（rate 为 decimal 字符串，前端 decimal 换算杜绝浮点）。
func (s *StorefrontConfigService) ListCurrencies(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.Currencies, error) {
	rows, err := s.repo.Currencies(ctx)
	if err != nil {
		return nil, errors.InternalServer("settings.CURRENCY_LIST_FAILED", "读取货币失败")
	}
	out := &storefrontv1.Currencies{}
	for _, c := range rows {
		out.Currencies = append(out.Currencies, &storefrontv1.Currency{
			Code:      c.Code,
			Symbol:    c.Symbol,
			Position:  c.Position,
			Precision: c.Precision,
			RateJson:  c.Rate.String(),
		})
	}
	return out, nil
}

// CurrencyView 货币视图。
type CurrencyView struct {
	Code      string
	Symbol    string
	Position  string
	Precision int32
	Rate      decimal.Decimal
}

// Currencies 启用货币视图列表。
func (r *RepoImpl) Currencies(ctx context.Context) ([]CurrencyView, error) {
	rows, err := data.Client(ctx, r.data).Currency.Query().
		Where(currency.Enabled(true)).
		Order(ent.Asc(currency.FieldSort)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CurrencyView, 0, len(rows))
	for _, row := range rows {
		out = append(out, CurrencyView{
			Code: row.Code, Symbol: row.Symbol, Position: string(row.Position),
			Precision: row.Precision, Rate: decimal.NewFromFloat(row.Rate),
		})
	}
	return out, nil
}

// CurrencyByCode 按 code 取（P0-01 exchange 的取数端；port.CurrencyReader）。
func (r *RepoImpl) CurrencyByCode(ctx context.Context, code string) (string, int32, error) {
	row, err := data.Client(ctx, r.data).Currency.Query().Where(currency.Code(code)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", 2, errors.NotFound("settings.CURRENCY_NOT_FOUND", "货币不存在")
	}
	if err != nil {
		return "", 2, err
	}
	return decimal.NewFromFloat(row.Rate).String(), row.Precision, nil
}

var _ port.CurrencyReader = (*RepoImpl)(nil)
