package memberlevel

// StoreMemberLevelService 会员等级 storefront 面（：我的等级 + 进度 + 积分余额）。

import (
	"context"
	"encoding/json"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	walletport "github.com/NovaWorks/zcard-next/server/internal/mods/wallet/port"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreMemberLevelService 服务。
type StoreMemberLevelService struct {
	storefrontv1.UnimplementedStoreMemberLevelServiceServer
	repo   *MemberLevelRepoImpl
	points walletport.PointsReader
}

// NewStoreMemberLevelService 构造。
func NewStoreMemberLevelService(repo *MemberLevelRepoImpl, points walletport.PointsReader) *StoreMemberLevelService {
	return &StoreMemberLevelService{repo: repo, points: points}
}

// GetMyLevel 我的等级（阈值即时评估；未登录 401）。
func (s *StoreMemberLevelService) GetMyLevel(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.MyLevelReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	p, err := s.repo.ResolveProgress(ctx, claims.Subject)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.PROGRESS_FAILED", "等级解析失败")
	}
	reply := &storefrontv1.MyLevelReply{
		Source: p.Source, HasReferralLevel: p.HasReferral, PrivateLevel: p.Current != nil && p.Current.DisplayMode == "hidden",
		RechargedCents: p.RechargedCents,
		ConsumedCents:  p.ConsumedCents,
		Current:        toLevelBrief(p.Current),
		Next:           toLevelBrief(p.Next),
	}
	if s.points != nil {
		if pts, err := s.points.GetPoints(ctx, claims.Subject); err == nil {
			reply.Points = pts
		}
	}
	if p.Current != nil && p.Current.DisplayMode != "public" {
		reply.Next = nil
	}
	if reply.Next != nil && p.Next.DisplayMode == "public" {
		reply.Progress = &storefrontv1.LevelProgress{
			RechargeGapCents: p.RechargeGap,
			ConsumeGapCents:  p.ConsumeGap,
			Percent:          p.Percent,
		}
	}
	levels, err := s.repo.ListLevels(ctx)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.LIST_FAILED", "读取等级列表失败")
	}
	for _, lv := range levels {
		if lv.Enabled && lv.DisplayMode != "hidden" {
			reply.Levels = append(reply.Levels, toLevelBrief(lv))
		}
	}
	return reply, nil
}

func toLevelBrief(lv *ent.MemberLevel) *storefrontv1.LevelBrief {
	if lv == nil || lv.DisplayMode == "hidden" {
		return nil
	}
	if lv.DisplayMode == "contact" {
		return &storefrontv1.LevelBrief{Name: lv.Name, DisplayMode: "contact", DisplayText: "联系客服", AcquireMode: lv.AcquireMode}
	}
	brief := &storefrontv1.LevelBrief{
		DisplayMode: lv.DisplayMode, AcquireMode: lv.AcquireMode,
		Id: lv.ID, Name: lv.Name, Discount: lv.Discount,
		ThresholdType:     string(lv.ThresholdType),
		ThresholdRecharge: lv.ThresholdRecharge,
		ThresholdConsume:  lv.ThresholdConsume,
	}
	if len(lv.PointsRule) > 0 {
		if raw, err := json.Marshal(lv.PointsRule); err == nil {
			brief.PointsRuleJson = string(raw)
		}
	}
	return brief
}

// Preview exposes only the same public metadata as the member center.
func (s *StoreMemberLevelService) GetInviteBenefit(ctx context.Context, req *storefrontv1.InviteBenefitRequest) (*storefrontv1.InviteBenefitReply, error) {
	u, err := identity.NewUserRepo(s.repo.data).ResolvePromoCodeChecked(ctx, req.Code)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.INVITE_LOOKUP_FAILED", "推荐权益查询失败，请重试")
	}
	reply := &storefrontv1.InviteBenefitReply{}
	if u == nil || u.Status != user.StatusActive {
		return reply, nil
	}
	reply.Valid = true
	if u.InviteLevelID == 0 {
		return reply, nil
	}
	lv, err := data.Client(ctx, s.repo.data).MemberLevel.Get(ctx, u.InviteLevelID)
	if err != nil {
		return nil, errors.InternalServer("memberlevel.INVITE_LOOKUP_FAILED", "推荐权益暂不可用")
	}
	if !lv.Enabled {
		return reply, nil
	}
	reply.HasBenefit, reply.Level = true, toLevelBrief(lv)
	return reply, nil
}
