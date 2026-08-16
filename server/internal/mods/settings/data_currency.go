package settings

// admin 货币管理实现（P0-04 T3）：rate 走 decimal 解析（拒绝非法字符串），无浮点入口。

import (
	"context"
	"strconv"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminCurrencyService 货币管理（实现 adminv1）。
type AdminCurrencyService struct {
	adminv1.UnimplementedAdminCurrencyServiceServer
	data *data.Data
}

// NewAdminCurrencyService 构造。
func NewAdminCurrencyService(d *data.Data) *AdminCurrencyService {
	return &AdminCurrencyService{data: d}
}

// ListCurrencies 全部货币（含停用）。
func (s *AdminCurrencyService) ListCurrencies(ctx context.Context, _ *emptypb.Empty) (*adminv1.CurrencyList, error) {
	rows, err := data.Client(ctx, s.data).Currency.Query().Order(ent.Asc(currency.FieldSort)).All(ctx)
	if err != nil {
		return nil, errors.InternalServer("settings.CURRENCY_LIST_FAILED", "读取货币失败")
	}
	out := &adminv1.CurrencyList{}
	for _, r := range rows {
		out.Currencies = append(out.Currencies, toPBCurrency(r))
	}
	return out, nil
}

// CreateCurrency 新增（code 唯一）。
func (s *AdminCurrencyService) CreateCurrency(ctx context.Context, req *adminv1.CreateCurrencyRequest) (*adminv1.Currency, error) {
	rate, err := parseRate(req.GetRateJson())
	if err != nil {
		return nil, err
	}
	pos := req.GetPosition()
	if pos == "" {
		pos = "prefix"
	}
	prec := req.GetPrecision()
	if prec < 0 || prec > 8 {
		return nil, errors.BadRequest("settings.CURRENCY_BAD_PRECISION", "小数位 0-8")
	}
	row, err := data.Client(ctx, s.data).Currency.Create().
		SetCode(req.GetCode()).
		SetSymbol(req.GetSymbol()).
		SetPosition(currency.Position(pos)).
		SetPrecision(prec).
		SetRate(rate).
		Save(ctx)
	if err != nil {
		return nil, errors.InternalServer("settings.CURRENCY_CREATE_FAILED", "创建失败（code 可能重复）")
	}
	return toPBCurrency(row), nil
}

// UpdateCurrency 修改。
func (s *AdminCurrencyService) UpdateCurrency(ctx context.Context, req *adminv1.UpdateCurrencyRequest) (*adminv1.Currency, error) {
	q := data.Client(ctx, s.data).Currency.Update().Where(currency.Code(req.GetCode()))
	if req.GetSymbol() != "" {
		q.SetSymbol(req.GetSymbol())
	}
	if req.GetPosition() != "" {
		q.SetPosition(currency.Position(req.GetPosition()))
	}
	if req.GetPrecision() > 0 {
		q.SetPrecision(req.GetPrecision())
	}
	if req.GetRateJson() != "" {
		rate, err := parseRate(req.GetRateJson())
		if err != nil {
			return nil, err
		}
		q.SetRate(rate)
	}
	q.SetEnabled(req.GetEnabled())
	if req.GetSort() >= 0 {
		q.SetSort(req.GetSort())
	}
	rows, err := q.Save(ctx)
	if err != nil || rows == 0 {
		return nil, errors.NotFound("settings.CURRENCY_NOT_FOUND", "货币不存在")
	}
	row, _ := data.Client(ctx, s.data).Currency.Query().Where(currency.Code(req.GetCode())).Only(ctx)
	return toPBCurrency(row), nil
}

// DeleteCurrency 删除。
func (s *AdminCurrencyService) DeleteCurrency(ctx context.Context, req *adminv1.DeleteCurrencyRequest) (*emptypb.Empty, error) {
	n, err := data.Client(ctx, s.data).Currency.Delete().Where(currency.Code(req.GetCode())).Exec(ctx)
	if err != nil {
		return nil, errors.InternalServer("settings.CURRENCY_DELETE_FAILED", "删除失败")
	}
	if n == 0 {
		return nil, errors.NotFound("settings.CURRENCY_NOT_FOUND", "货币不存在")
	}
	return &emptypb.Empty{}, nil
}

func parseRate(s string) (float64, error) {
	d, err := decimal.NewFromString(s)
	if err != nil || d.IsNegative() {
		return 0, errors.BadRequest("settings.CURRENCY_BAD_RATE", "汇率必须为非负 decimal 字符串")
	}
	f, _ := d.Float64()
	return f, nil
}

func toPBCurrency(r *ent.Currency) *adminv1.Currency {
	return &adminv1.Currency{
		Code: r.Code, Symbol: r.Symbol, Position: string(r.Position),
		Precision: r.Precision, RateJson: strconv.FormatFloat(r.Rate, 'f', -1, 64),
		Enabled: r.Enabled, Sort: r.Sort,
	}
}
