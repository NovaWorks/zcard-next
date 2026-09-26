package dashboard

// 工作台按北京时间聚合可见订单；支付与成功退款分别按发生时间统计。

import (
	"context"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/affiliatecommission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/payment"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/refundorder"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplieraccount"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/wallettransaction"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/withdrawal"
	"github.com/NovaWorks/zcard-next/server/internal/platform/businessday"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// Metric 统计项。
type Metric struct {
	Orders            int64
	Revenue           int64
	PaidOrders        int64
	Cost              int64
	Profit            int64
	Refunds           int64
	NetRevenue        int64
	UnknownCostOrders int64
	NewUsers          int64
}

// TrendPoint 趋势点。
type TrendPoint struct {
	Refunds    int64
	NetRevenue int64
	Date       string
	Orders     int64
	Revenue    int64
	PaidCount  int64
	Cost       int64
	Profit     int64
}

// TopProduct 商品排行。
type TopProduct struct {
	ProductID uint64
	Name      string
	SoldQty   int64
	Revenue   int64
}

// TopChannel 支付渠道排行。
type TopChannel struct {
	ChannelID    uint64
	Name         string
	ChannelState string
	Amount       int64
	Channel      string
	TotalCount   int64
	SuccessCount int64
	FailedCount  int64
}

// DashboardRepoImpl 报表仓储。
type DashboardRepoImpl struct {
	data *data.Data
	now  func() time.Time
}

// NewDashboardRepoImpl 构造。
func NewDashboardRepoImpl(d *data.Data) *DashboardRepoImpl {
	return &DashboardRepoImpl{data: d, now: time.Now}
}

// GetLowStockCount 库存预警商品数：上架商品（status=1）按本地/上游分别统计有限库存 < threshold。
func (r *DashboardRepoImpl) GetLowStockCount(ctx context.Context, threshold int) (int64, error) {
	if threshold < 1 {
		threshold = 5
	}
	subsite := tenancy.FromContext(ctx).SubsiteID
	client := data.Client(ctx, r.data)
	products, err := client.Product.Query().Where(product.StatusEQ(1), product.SubsiteID(subsite)).All(ctx)
	if err != nil {
		return 0, err
	}
	var low int64
	for _, p := range products {
		yes, err := data.HasLowStock(ctx, r.data, p, threshold)
		if err != nil {
			return 0, err
		}
		if yes {
			low++
		}
	}

	return low, nil
}

func (r *DashboardRepoImpl) GetPendingSupplierApplications(ctx context.Context) (int64, error) {
	n, err := data.Client(ctx, r.data).SupplierAccount.Query().Where(supplieraccount.StatusEQ(supplieraccount.StatusApplying)).Count(ctx)
	return int64(n), err
}

// GetPending returns current actionable orders; payment review receipts are separate.
func (r *DashboardRepoImpl) GetPending(ctx context.Context) (withdrawals, refunds, fulfilling int64, err error) {
	subsite := tenancy.FromContext(ctx).SubsiteID
	c := data.Client(ctx, r.data)
	n, err := c.Withdrawal.Query().Where(withdrawal.StatusEQ(withdrawal.StatusPending)).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	withdrawals = int64(n)
	n, err = c.Order.Query().Where(order.SubsiteID(subsite), order.AdminDeletedAtIsNil(), order.Or(order.StatusEQ(order.StatusRefundPending), order.HasRefundsWith(refundorder.StatusIn(refundorder.StatusCreated, refundorder.StatusProcessing)))).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	refunds = int64(n)
	n, err = c.Order.Query().Where(order.SubsiteID(subsite), order.AdminDeletedAtIsNil(), order.StatusIn(order.StatusPaid, order.StatusFulfilling, order.StatusPartiallyDelivered)).Count(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	fulfilling = int64(n)
	return

}

// ReconciliationSummary 对账汇总（：订单×支付×充值×佣金四向基础核对）。
type ReconciliationSummary struct {
	Date                string
	OrderPaidTotal      int64
	PaymentSuccessTotal int64
	WalletRechargeTotal int64
	CommissionTotal     int64
	OrderCount          int64
	MismatchCount       int64
}

// GetReconciliation 当日对账（口径：本地时区日界——运营对账口径；金额一律分）。
func (r *DashboardRepoImpl) GetReconciliation(ctx context.Context, date string) (*ReconciliationSummary, error) {
	var day time.Time
	if d, err := time.ParseInLocation("20060102", date, businessday.Location); err == nil {
		day = d
	} else {
		now := businessday.Now()
		day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, businessday.Location)
	}
	start := day.UTC()
	end := day.AddDate(0, 0, 1).UTC()

	client := data.Client(ctx, r.data)
	out := &ReconciliationSummary{Date: day.Format("20060102")}

	// 1) 当日已支付订单
	orders, err := client.Order.Query().
		Where(order.PaidAtGTE(start), order.PaidAtLT(end)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range orders {
		out.OrderPaidTotal += o.TotalAmount
		out.OrderCount++
	}

	// 2) 当日支付单成功额
	pays, err := client.Payment.Query().
		Where(payment.PaidAtGTE(start), payment.PaidAtLT(end)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pays {
		out.PaymentSuccessTotal += p.ChargedAmount
	}

	// 3) 当日钱包充值（direction=in & type=recharge）
	recharges, err := client.WalletTransaction.Query().
		Where(
			wallettransaction.CreatedAtGTE(start),
			wallettransaction.CreatedAtLT(end),
			wallettransaction.DirectionEQ("in"),
			wallettransaction.TypeEQ("recharge"),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range recharges {
		out.WalletRechargeTotal += w.Amount
	}

	// 4) 当日佣金计提（正佣金）
	commissions, err := client.AffiliateCommission.Query().
		Where(
			affiliatecommission.CreatedAtGTE(start),
			affiliatecommission.CreatedAtLT(end),
			affiliatecommission.AmountGT(0),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range commissions {
		out.CommissionTotal += c.Amount
	}

	// 5) 差异：订单额 vs 支付单额（余额支付订单会天然差异——差额 = 余额支付部分 + 手续费；
	// 报表层只报差异数，人工判读）
	diff := out.OrderPaidTotal - out.PaymentSuccessTotal - out.WalletRechargeTotal
	if diff < 0 {
		diff = -diff
	}
	if diff > 0 {
		out.MismatchCount = 1 // 汇总级差异标记（明细级对账 M4 reconciliation_jobs）
	}
	return out, nil
}
