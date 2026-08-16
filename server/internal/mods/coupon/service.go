package coupon

// AdminCouponService 管理面优惠券服务（M1b 基础版：批量生成 + 列表 + 作废）。

import (
	"context"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminCouponService 服务。
type AdminCouponService struct {
	adminv1.UnimplementedAdminCouponServiceServer
	repo *CouponRepoImpl
}

// NewAdminCouponService 构造。
func NewAdminCouponService(repo *CouponRepoImpl) *AdminCouponService {
	return &AdminCouponService{repo: repo}
}

// ListCoupons 券列表。
func (s *AdminCouponService) ListCoupons(ctx context.Context, req *adminv1.ListCouponsRequest) (*adminv1.CouponList, error) {
	rows, err := s.repo.ListCoupons(ctx, req.GetStatus())
	if err != nil {
		return nil, errors.InternalServer("coupon.LIST_FAILED", "读取券失败")
	}
	reply := &adminv1.CouponList{}
	for _, c := range rows {
		reply.Coupons = append(reply.Coupons, &adminv1.Coupon{
			Id: c.ID, BatchId: c.BatchID, Name: c.Name, Type: string(c.Type),
			Value: c.Value, Code: c.Code, Status: string(c.Status),
		})
	}
	return reply, nil
}

// CreateCouponBatch 批量生成。
func (s *AdminCouponService) CreateCouponBatch(ctx context.Context, req *adminv1.CreateCouponBatchRequest) (*adminv1.CreateCouponBatchReply, error) {
	if req.GetName() == "" || req.GetCount() <= 0 {
		return nil, errors.BadRequest("coupon.INVALID_INPUT", "名称/数量必填且数量>0")
	}
	var expireAt *time.Time
	if req.GetExpireAt() > 0 {
		t := time.Unix(req.GetExpireAt(), 0).UTC()
		expireAt = &t
	}
	n, err := s.repo.CreateBatch(ctx, req.GetName(), req.GetType(), req.GetValue(), req.GetCount(), expireAt)
	if err != nil {
		return nil, errors.InternalServer("coupon.CREATE_FAILED", "生成失败: "+err.Error())
	}
	return &adminv1.CreateCouponBatchReply{Count: n}, nil
}

// DisableCoupon 作废（按 batch_id）。
func (s *AdminCouponService) DisableCoupon(ctx context.Context, req *adminv1.DisableCouponRequest) (*emptypb.Empty, error) {
	if _, err := s.repo.Disable(ctx, req.GetBatchId()); err != nil {
		return nil, errors.InternalServer("coupon.DISABLE_FAILED", "作废失败")
	}
	return &emptypb.Empty{}, nil
}
