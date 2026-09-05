package memberlevel

// StoreMemberLevelService 会员等级 storefront 面（：我的等级 + 进度 + 积分余额）。

import (
	"context"
	"encoding/json"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
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
	if p.Next != nil {
		reply.Progress = &storefrontv1.LevelProgress{
			RechargeGapCents: p.RechargeGap,
			ConsumeGapCents:  p.ConsumeGap,
			Percent:          p.Percent,
		}
	}
	return reply, nil
}

func toLevelBrief(lv *ent.MemberLevel) *storefrontv1.LevelBrief {
	if lv == nil {
		return nil
	}
	brief := &storefrontv1.LevelBrief{
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
