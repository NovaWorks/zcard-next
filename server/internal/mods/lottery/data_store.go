package lottery

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryactivity"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/go-kratos/kratos/v3/transport"
	"google.golang.org/protobuf/types/known/emptypb"
)

type StoreService struct {
	storefrontv1.UnimplementedStoreLotteryServiceServer
	repo *Repo
}

func NewStoreService(r *Repo) *StoreService { return &StoreService{repo: r} }
func noCache(ctx context.Context) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store, private")
	}
}
func (s *StoreService) publicActivity(ctx context.Context, a *ent.LotteryActivity, detail bool) (*storefrontv1.LotteryActivity, error) {
	out := &storefrontv1.LotteryActivity{Id: a.ID, Name: a.Name, Description: a.Description, Image: a.Image, Status: state(a, s.repo.now()), StartAt: unix(a.StartAt), EndAt: unix(a.EndAt), Timezone: a.Timezone, ChanceMode: a.ChanceMode, ChanceCount: a.ChanceCount, Revision: a.Revision}
	if !detail {
		return out, nil
	}
	ps, e := s.repo.prizes(ctx, a.ID)
	if e != nil {
		return nil, e
	}
	for _, p := range ps {
		remaining := p.Quantity - p.Issued
		if remaining < 0 {
			remaining = 0
		}
		out.Prizes = append(out.Prizes, &storefrontv1.LotteryPrize{Id: p.ID, Name: p.Name, Image: p.Image, Mode: p.Mode, Probability: p.Probability, Remaining: remaining})
	}
	if out.Status == "live" {
		if e = s.repo.ready(ctx, ps); e != nil {
			out.Status = "unavailable"
		}
	}
	return out, nil
}
func (s *StoreService) ListLotteryActivities(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.LotteryActivityList, error) {
	noCache(ctx)
	rows, e := data.Client(ctx, s.repo.Data).LotteryActivity.Query().Where(lotteryactivity.SubsiteID(site(ctx)), lotteryactivity.Published(true), lotteryactivity.StatusNotIn("archived", "ended"), lotteryactivity.EndAtGT(s.repo.now())).Order(ent.Desc(lotteryactivity.FieldID)).Limit(50).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &storefrontv1.LotteryActivityList{}
	for _, a := range rows {
		p, e := s.publicActivity(ctx, a, false)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}
func (s *StoreService) GetLotteryActivity(ctx context.Context, v *storefrontv1.LotteryIDRequest) (*storefrontv1.LotteryActivity, error) {
	noCache(ctx)
	a, e := s.repo.activity(ctx, v.Id, false)
	if e != nil {
		return nil, e
	}
	if !a.Published || a.Status == "archived" {
		return nil, bad("活动未开放")
	}
	return s.publicActivity(ctx, a, true)
}
func (s *StoreService) ClaimLotteryChances(ctx context.Context, v *storefrontv1.LotteryIDRequest) (*storefrontv1.LotteryChances, error) {
	noCache(ctx)
	uid, e := s.repo.member(ctx)
	if e != nil {
		return nil, e
	}
	n, per, e := s.repo.Claim(ctx, v.Id, uid)
	if e != nil {
		return nil, e
	}
	return &storefrontv1.LotteryChances{Remaining: n, Period: per}, nil
}
func storeDraw(d *ent.LotteryDraw) *storefrontv1.LotteryDraw {
	return &storefrontv1.LotteryDraw{DrawNo: d.DrawNo, ActivityId: d.ActivityID, ActivityName: d.ActivityName, PrizeName: d.PrizeName, Mode: d.Mode, Status: d.Status, CreatedAt: unix(d.CreatedAt), DeliveredAt: unix(d.DeliveredAt)}
}
func (s *StoreService) DrawLottery(ctx context.Context, v *storefrontv1.LotteryDrawRequest) (*storefrontv1.LotteryDraw, error) {
	noCache(ctx)
	uid, e := s.repo.member(ctx)
	if e != nil {
		return nil, e
	}
	d, e := s.repo.Draw(ctx, v.Id, uid, v.RequestKey)
	if e != nil {
		return nil, e
	}
	out := storeDraw(d)
	out.Content, e = s.repo.Content(ctx, d)
	return out, e
}
func (s *StoreService) ListMyLotteryDraws(ctx context.Context, v *storefrontv1.LotteryListRequest) (*storefrontv1.LotteryDrawList, error) {
	noCache(ctx)
	uid, e := s.repo.member(ctx)
	if e != nil {
		return nil, e
	}
	q := data.Client(ctx, s.repo.Data).LotteryDraw.Query().Where(lotterydraw.SubsiteID(site(ctx)), lotterydraw.UserID(uid))
	total, e := q.Clone().Count(ctx)
	if e != nil {
		return nil, e
	}
	off, size := paging(v.Page, v.PageSize)
	rows, e := q.Order(ent.Desc(lotterydraw.FieldID)).Offset(off).Limit(size).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &storefrontv1.LotteryDrawList{Total: int64(total)}
	for _, d := range rows {
		out.Items = append(out.Items, storeDraw(d))
	}
	return out, nil
}
func (s *StoreService) GetMyLotteryDraw(ctx context.Context, v *storefrontv1.LotteryResultRequest) (*storefrontv1.LotteryDraw, error) {
	noCache(ctx)
	uid, e := s.repo.member(ctx)
	if e != nil {
		return nil, e
	}
	d, e := s.repo.Result(ctx, v.DrawNo, uid)
	if e != nil {
		return nil, e
	}
	out := storeDraw(d)
	out.Content, e = s.repo.Content(ctx, d)
	return out, e
}
