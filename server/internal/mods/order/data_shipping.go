package order

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/shipping"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
	"math/big"
	"sort"
)

var errQuoteRollback = errors.New("quote rollback")

func allocateCents(total, before, after, base int64) int64 {
	if base <= 0 {
		return 0
	}
	t := big.NewInt(total)
	b := big.NewInt(base)
	lo := new(big.Int).Quo(new(big.Int).Mul(t, big.NewInt(before)), b)
	hi := new(big.Int).Quo(new(big.Int).Mul(t, big.NewInt(after)), b)
	return hi.Sub(hi, lo).Int64()
}
func (s *StoreOrderService) ShippingRegions(ctx context.Context, req *storefrontv1.ShippingRegionsRequest) (*storefrontv1.ShippingRegionsReply, error) {
	if req.Country == "" {
		return &storefrontv1.ShippingRegionsReply{DataJson: string(shipping.RegionsJSON)}, nil
	}
	r, ok := shipping.Regions[req.Country]
	if !ok {
		return nil, kerrors.BadRequest("shipping.COUNTRY", "国家无效")
	}
	b, _ := json.Marshal(r)
	return &storefrontv1.ShippingRegionsReply{DataJson: string(b)}, nil
}
func (s *StoreOrderService) ReceiveShipment(ctx context.Context, req *storefrontv1.ReceiveShipmentRequest) (*emptypb.Empty, error) {
	if _, e := s.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: req.OrderNo, QueryPassword: req.QueryPassword}); e != nil {
		return nil, e
	}
	if e := data.ReceivePhysicalShipment(ctx, s.uc.Data, req.OrderNo, req.ShipmentId, "user", 0); e != nil {
		return nil, kerrors.BadRequest("shipping.RECEIVE", e.Error())
	}
	return &emptypb.Empty{}, nil
}

func orderRequestHash(in CreateOrderInput) string {
	values := []any{in.SubsiteID, in.UserID, in.Items, in.ShippingAddress, in.Contact, in.GuestContact, in.CouponCode, in.UsePoints, in.QueryPassword, in.ControlAnswers, in.RefCode}
	if in.SMSQuoteID != "" {
		values = append(values, in.SMSQuoteID)
	}
	b, _ := json.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func orderIdemHash(in CreateOrderInput) string {
	return fmt.Sprintf("idem-%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", in.SubsiteID, in.UserID, in.Contact, in.IdempotencyKey))))
}

// Replay verifies the complete request fingerprint before any one-use captcha,
// stock, coupon or current product checks can reject an already committed order.
func (uc *OrderUsecase) ReplayOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	if in.QuoteOnly || in.IdempotencyKey == "" {
		return nil, nil
	}
	if in.SubsiteID == 0 {
		in.SubsiteID = tenancy.FromContext(ctx).SubsiteID
	}
	in.Items = append([]OrderItemInput(nil), in.Items...)
	sort.Slice(in.Items, func(i, j int) bool {
		if in.Items[i].ProductID == in.Items[j].ProductID {
			return in.Items[i].SkuID < in.Items[j].SkuID
		}
		return in.Items[i].ProductID < in.Items[j].ProductID
	})
	if len(in.ShippingAddress) > 0 {
		a, e := shipping.Normalize(in.ShippingAddress)
		if e != nil {
			return nil, e
		}
		in.ShippingAddress = a
	} else {
		in.ShippingAddress = nil
	}
	prev, e := data.Client(ctx, uc.Data).Order.Query().Where(entorder.IdempotencyKey(orderIdemHash(in))).Only(ctx)
	if ent.IsNotFound(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if prev.RequestHash != "" && prev.RequestHash != orderRequestHash(in) {
		return nil, fmt.Errorf("order.FORM_INVALID: 同一请求标识不能用于不同订单内容")
	}
	return &CreateOrderResult{OrderNo: prev.OrderNo, TotalCents: prev.TotalAmount, ShippingCents: prev.ShippingAmount, ExpiresAt: prev.ExpiredAt}, nil
}
