package memberlevel

// AdminMemberLevelService 管理面会员等级服务（P3-01：CRUD + points_rule 透传）。

import (
	"context"
	"encoding/json"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminMemberLevelService 服务。
type AdminMemberLevelService struct {
	adminv1.UnimplementedAdminMemberLevelServiceServer
	repo *MemberLevelRepoImpl
}

// NewAdminMemberLevelService 构造。
func NewAdminMemberLevelService(repo *MemberLevelRepoImpl) *AdminMemberLevelService {
	return &AdminMemberLevelService{repo: repo}
}

// parsePointsRule points_rule JSON 解析（空串 = 不设置；"{}" = 清除）。
func parsePointsRule(raw string) (map[string]any, error) {
	if raw == "" {
		return nil, nil
	}
	var rule map[string]any
	if err := json.Unmarshal([]byte(raw), &rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// ListMemberLevels 等级列表。
func (s *AdminMemberLevelService) ListMemberLevels(ctx context.Context, _ *emptypb.Empty) (*adminv1.MemberLevelList, error) {
	rows, err := s.repo.ListLevels(ctx)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.LIST_FAILED", "读取等级失败")
	}
	reply := &adminv1.MemberLevelList{}
	for _, lv := range rows {
		reply.Levels = append(reply.Levels, levelPB(lv))
	}
	return reply, nil
}

// CreateMemberLevel 创建。
func (s *AdminMemberLevelService) CreateMemberLevel(ctx context.Context, req *adminv1.CreateMemberLevelRequest) (*adminv1.MemberLevel, error) {
	if req.GetName() == "" {
		return nil, errors.BadRequest("memberlevel.INVALID_INPUT", "名称必填")
	}
	rule, err := parsePointsRule(req.GetPointsRuleJson())
	if err != nil {
		return nil, errors.BadRequest("memberlevel.POINTS_RULE_INVALID", "积分规则 JSON 非法（应为 {\"spend_cents\":X,\"points\":Y}）")
	}
	lv, err := s.repo.CreateLevel(ctx, req.GetName(), req.GetThresholdType(),
		req.GetThresholdRecharge(), req.GetThresholdConsume(), req.GetDiscount(), req.GetSort(), req.GetEnabled(), rule)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.CREATE_FAILED", "创建失败")
	}
	return levelPB(lv), nil
}

// UpdateMemberLevel 更新。
func (s *AdminMemberLevelService) UpdateMemberLevel(ctx context.Context, req *adminv1.UpdateMemberLevelRequest) (*adminv1.MemberLevel, error) {
	rule, err := parsePointsRule(req.GetPointsRuleJson())
	if err != nil {
		return nil, errors.BadRequest("memberlevel.POINTS_RULE_INVALID", "积分规则 JSON 非法（应为 {\"spend_cents\":X,\"points\":Y}）")
	}
	lv, err := s.repo.UpdateLevel(ctx, req.GetId(), req.GetName(), req.GetDiscount(), req.GetSort(), req.GetEnabled(), rule)
	if err != nil {
		return nil, errors.NotFound("memberlevel.NOT_FOUND", "等级不存在")
	}
	return levelPB(lv), nil
}

// DeleteMemberLevel 删除。
func (s *AdminMemberLevelService) DeleteMemberLevel(ctx context.Context, req *adminv1.DeleteMemberLevelRequest) (*emptypb.Empty, error) {
	if err := s.repo.DeleteLevel(ctx, req.GetId()); err != nil {
		return nil, errors.NotFound("memberlevel.NOT_FOUND", "等级不存在")
	}
	return &emptypb.Empty{}, nil
}

// levelPB 等级转协议（含 points_rule JSON）。
func levelPB(lv *ent.MemberLevel) *adminv1.MemberLevel {
	out := &adminv1.MemberLevel{
		Id: lv.ID, Name: lv.Name, ThresholdType: string(lv.ThresholdType),
		ThresholdRecharge: lv.ThresholdRecharge, ThresholdConsume: lv.ThresholdConsume,
		Discount: lv.Discount, Sort: lv.Sort, Enabled: lv.Enabled,
	}
	if len(lv.PointsRule) > 0 {
		if raw, err := json.Marshal(lv.PointsRule); err == nil {
			out.PointsRuleJson = string(raw)
		}
	}
	return out
}
