package procurement

// 管理面 API（P2-02）：采购单列表/详情/手动重试/手动标记完成。

import (
	"context"
	"errors"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementorder"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminProcurementService 管理面采购服务。
type AdminProcurementService struct {
	adminv1.UnimplementedAdminProcurementServiceServer
	repo *ProcureRepo
	svc  *ProcureService
}

// NewAdminProcurementService 构造。
func NewAdminProcurementService(repo *ProcureRepo, svc *ProcureService) *AdminProcurementService {
	return &AdminProcurementService{repo: repo, svc: svc}
}

// ListProcurements 列表。
func (s *AdminProcurementService) ListProcurements(ctx context.Context, req *adminv1.ListProcurementsRequest) (*adminv1.ListProcurementsReply, error) {
	page, pageSize := procurePageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.List(ctx, req.GetStatus(), page, pageSize)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListProcurementsReply{Total: int64(total), Page: int32(page), PageSize: int32(pageSize)}
	for _, po := range rows {
		reply.Procurements = append(reply.Procurements, s.toProto(ctx, po))
	}
	return reply, nil
}

// GetProcurement 详情。
func (s *AdminProcurementService) GetProcurement(ctx context.Context, req *adminv1.GetProcurementRequest) (*adminv1.ProcurementOrder, error) {
	po, err := s.repo.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return s.toProto(ctx, po), nil
}

// RetryProcurement 手动重试：终态拒绝；否则按当前状态推进（提交/轮询）。
func (s *AdminProcurementService) RetryProcurement(ctx context.Context, req *adminv1.RetryProcurementRequest) (*adminv1.ProcurementOrder, error) {
	po, err := s.repo.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	switch string(po.Status) {
	case "fulfilled", "rejected", "refunded":
		return nil, errors.New("procurement.ALREADY_TERMINAL: 采购单已终态")
	case "manual":
		// 人工标记后可重试（重新提交）
	}
	// 重试统一走轮询入口（pending/submitted/polling 均安全）
	if err := s.svc.PollOne(ctx, req.GetId()); err != nil {
		return nil, err
	}
	po, err = s.repo.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return s.toProto(ctx, po), nil
}

// MarkProcurementManual 手动标记完成/转人工（人工拿货后回填）。
func (s *AdminProcurementService) MarkProcurementManual(ctx context.Context, req *adminv1.MarkProcurementManualRequest) (*adminv1.ProcurementOrder, error) {
	po, err := s.repo.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if string(po.Status) == "manual" {
		return s.toProto(ctx, po), nil
	}
	if err := s.repo.MarkManual(ctx, req.GetId(), orEmpty(req.GetRemark(), "管理员手动转人工")); err != nil {
		return nil, err
	}
	po, _ = s.repo.Get(ctx, req.GetId())
	return s.toProto(ctx, po), nil
}

// toProto 转协议（含采购项信息）。
func (s *AdminProcurementService) toProto(ctx context.Context, po *ent.ProcurementOrder) *adminv1.ProcurementOrder {
	out := &adminv1.ProcurementOrder{
		Id:               po.ID,
		OrderItemId:      po.OrderItemID,
		ConnectionId:     po.ConnectionID,
		UpstreamOrderId:  po.UpstreamOrderID,
		Status:           string(po.Status),
		FailStrategy:     string(po.FailStrategy),
		RetryCount:       po.RetryCount,
		DedupeKey:        po.DedupeKey,
		TraceId:          po.TraceID,
		LastError:        po.LastError,
		UpstreamRefundId: po.UpstreamRefundID,
		CreatedAt:        po.CreatedAt.Unix(),
		UpdatedAt:        po.UpdatedAt.Unix(),
	}
	if !po.NextRetryAt.IsZero() {
		out.NextRetryAt = po.NextRetryAt.Unix()
	}
	if !po.LastPollAt.IsZero() {
		out.LastPollAt = po.LastPollAt.Unix()
	}
	// 采购项（数量/成本/到手卡密行数）
	if item, err := s.repo.ItemByProcurement(ctx, po.ID); err == nil {
		out.ItemQuantity = item.Quantity
		out.ItemUnitCost = item.UnitCost
		out.ReceivedCards = int32(len(item.ReceivedContent))
	}
	return out
}

func procurePageParams(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}

func orEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// 编译期保证：procurementorder 包被引用（状态过滤语义）。
var _ = procurementorder.StatusPending
var _ = emptypb.Empty{}
