package lottery

import (
	"context"
	"encoding/json"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryactivity"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotterydraw"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryprize"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/lotteryrevision"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"google.golang.org/protobuf/types/known/emptypb"
	"strings"
	"time"
)

type AdminService struct {
	adminv1.UnimplementedAdminLotteryServiceServer
	repo *Repo
}

func NewAdminService(r *Repo) *AdminService { return &AdminService{repo: r} }
func validConfig(v *adminv1.LotteryActivity) error {
	if strings.TrimSpace(v.Name) == "" || len([]rune(v.Name)) > 100 || len([]rune(v.Description)) > 5000 || len(v.Image) > 2048 || !validImage(v.Image) {
		return bad("活动名称、说明或图片地址不正确")
	}
	if v.EndAt <= v.StartAt || v.StartAt <= 0 {
		return bad("结束时间必须晚于开始时间")
	}
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return bad("活动时区无效")
	}
	if v.ChanceMode != "once" && v.ChanceMode != "daily" && v.ChanceMode != "manual" {
		return bad("次数规则无效")
	}
	if v.ChanceCount < 0 || v.ChanceCount > 1000 || (v.ChanceMode != "manual" && v.ChanceCount == 0) {
		return bad("自动赠送次数应为 1～1000")
	}
	if len(v.Prizes) == 0 || len(v.Prizes) > 12 {
		return bad("请配置 1～12 种奖品")
	}
	total := int64(0)
	seen := map[uint64]bool{}
	for _, p := range v.Prizes {
		if p == nil || strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 || len(p.Image) > 2048 || !validImage(p.Image) {
			return bad("奖品名称或图片无效")
		}
		if p.Id != 0 && seen[p.Id] {
			return bad("奖品重复")
		}
		seen[p.Id] = true
		if p.Quantity < 1 || p.Quantity > 1000000 || p.Probability < 0 || p.Probability > 10000 {
			return bad("奖品数量应为 1～1000000，中奖概率应为 0～100%")
		}
		total += int64(p.Probability)
		if p.Mode != "card" && p.Mode != "text" && p.Mode != "manual" {
			return bad("奖品发放方式无效")
		}
		if p.Mode == "card" {
			if p.ProductId == 0 {
				return bad("自动发卡需要选择商品")
			}
		} else {
			if strings.TrimSpace(p.Content) == "" || len([]rune(p.Content)) > 10000 {
				return bad("请填写发放内容或领取说明，最多 10000 字")
			}
		}
	}
	if total <= 0 || total > 10000 {
		return bad("奖品概率合计须大于 0 且不超过 100%")
	}
	return nil
}
func (s *AdminService) SaveLotteryActivity(ctx context.Context, v *adminv1.LotteryActivity) (*adminv1.LotteryActivity, error) {
	if e := validConfig(v); e != nil {
		return nil, e
	}
	var id uint64
	e := data.Tx(ctx, s.repo.Data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.Data)
		var a *ent.LotteryActivity
		var e error
		if v.Id == 0 {
			a, e = c.LotteryActivity.Create().SetSubsiteID(site(ctx)).SetName(v.Name).SetStartAt(time.Unix(v.StartAt, 0)).SetEndAt(time.Unix(v.EndAt, 0)).Save(ctx)
		} else {
			a, e = s.repo.activity(ctx, v.Id, true)
			if e != nil {
				return e
			}
			if a.Revision != v.Revision {
				return bad("活动已被修改，请刷新后重试")
			}
			if a.Status == "live" || a.Status == "archived" || state(a, s.repo.now()) == "ended" {
				return bad("请先暂停活动再编辑；已结束活动请新建或复制")
			}
			if a.Published && (v.ChanceMode != a.ChanceMode || v.ChanceCount != a.ChanceCount || v.Timezone != a.Timezone || v.StartAt != a.StartAt.Unix()) {
				return bad("发布后不能更改次数规则、时区或开始时间，请复制为新活动")
			}
		}
		if e != nil {
			return e
		}
		id = a.ID
		update := c.LotteryActivity.UpdateOneID(id).SetName(strings.TrimSpace(v.Name)).SetDescription(v.Description).SetImage(v.Image).SetStartAt(time.Unix(v.StartAt, 0)).SetEndAt(time.Unix(v.EndAt, 0)).SetTimezone(v.Timezone).SetChanceMode(v.ChanceMode).SetChanceCount(v.ChanceCount)
		if v.Id != 0 {
			update = update.AddRevision(1)
		}
		a, e = update.Save(ctx)
		if e != nil {
			return e
		}
		if _, e = c.LotteryPrize.Update().Where(lotteryprize.ActivityID(id), lotteryprize.SubsiteID(site(ctx))).SetEnabled(false).Save(ctx); e != nil {
			return e
		}
		for i, p := range v.Prizes {
			if p.Mode == "card" {
				if _, e = s.repo.eligibleCard(ctx, p.ProductId, p.SkuId); e != nil {
					return e
				}
			}
			var row *ent.LotteryPrize
			if p.Id == 0 {
				row, e = c.LotteryPrize.Create().SetSubsiteID(site(ctx)).SetActivityID(id).SetName(p.Name).SetMode(p.Mode).Save(ctx)
			} else {
				row, e = c.LotteryPrize.Query().Where(lotteryprize.ID(p.Id), lotteryprize.ActivityID(id), lotteryprize.SubsiteID(site(ctx))).Only(ctx)
			}
			if e != nil {
				if ent.IsNotFound(e) {
					return bad("奖品不属于该活动")
				}
				return e
			}
			if p.Quantity < row.Issued {
				return bad("奖品总量不能少于已发数量")
			}
			if row.Issued > 0 && (row.Mode != p.Mode || row.ProductID != p.ProductId || row.SkuID != p.SkuId) {
				return bad("已发放奖品不能更换来源，请新增奖品")
			}
			sealed := []byte(nil)
			if p.Mode != "card" {
				sealed, e = s.repo.Cipher.SealLottery(p.Content, fmt.Sprintf("prize:%d", row.ID), site(ctx))
				if e != nil {
					return e
				}
			}
			_, e = c.LotteryPrize.UpdateOneID(row.ID).SetName(strings.TrimSpace(p.Name)).SetImage(p.Image).SetMode(p.Mode).SetProductID(p.ProductId).SetSkuID(p.SkuId).SetProbability(p.Probability).SetQuantity(p.Quantity).SetSort(int32(i)).SetEnabled(true).SetContent(sealed).Save(ctx)
			if e != nil {
				return e
			}
		}
		ps, e := s.repo.prizes(ctx, id)
		if e != nil {
			return e
		}
		_, e = c.LotteryRevision.Create().SetSubsiteID(site(ctx)).SetActivityID(id).SetRevision(a.Revision).SetAdminID(actor(ctx)).SetSnapshot(snapshot(a, ps)).Save(ctx)
		return e
	})
	if e != nil {
		return nil, e
	}
	return s.GetLotteryActivity(ctx, &adminv1.LotteryIDRequest{Id: id})
}
func adminActivity(a *ent.LotteryActivity) *adminv1.LotteryActivity {
	return &adminv1.LotteryActivity{Id: a.ID, Name: a.Name, Description: a.Description, Image: a.Image, Status: a.Status, StartAt: unix(a.StartAt), EndAt: unix(a.EndAt), Timezone: a.Timezone, ChanceMode: a.ChanceMode, ChanceCount: a.ChanceCount, Revision: a.Revision, Published: a.Published}
}
func (s *AdminService) GetLotteryActivity(ctx context.Context, v *adminv1.LotteryIDRequest) (*adminv1.LotteryActivity, error) {
	noCache(ctx)
	a, e := s.repo.activity(ctx, v.Id, false)
	if e != nil {
		return nil, e
	}
	out := adminActivity(a)
	ps, e := s.repo.prizes(ctx, a.ID)
	if e != nil {
		return nil, e
	}
	for _, p := range ps {
		content := ""
		if len(p.Content) > 0 {
			content, e = s.repo.Cipher.OpenLottery(p.Content, fmt.Sprintf("prize:%d", p.ID), site(ctx))
			if e != nil {
				return nil, e
			}
		}
		out.Prizes = append(out.Prizes, &adminv1.LotteryPrize{Id: p.ID, Name: p.Name, Image: p.Image, Mode: p.Mode, ProductId: p.ProductID, SkuId: p.SkuID, Probability: p.Probability, Quantity: p.Quantity, Issued: p.Issued, Content: content})
	}
	return out, nil
}
func paging(page, size int32) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	if page > 100000 {
		page = 100000
	}
	return int((page - 1) * size), int(size)
}
func (s *AdminService) ListLotteryActivities(ctx context.Context, v *adminv1.LotteryListRequest) (*adminv1.LotteryActivityList, error) {
	q := data.Client(ctx, s.repo.Data).LotteryActivity.Query().Where(lotteryactivity.SubsiteID(site(ctx)))
	if v.Keyword != "" {
		q = q.Where(lotteryactivity.NameContains(v.Keyword))
	}
	if v.Status != "" {
		q = q.Where(lotteryactivity.Status(v.Status))
	}
	total, e := q.Clone().Count(ctx)
	if e != nil {
		return nil, e
	}
	off, size := paging(v.Page, v.PageSize)
	rows, e := q.Order(ent.Desc(lotteryactivity.FieldID)).Offset(off).Limit(size).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &adminv1.LotteryActivityList{Total: int64(total)}
	for _, a := range rows {
		p := adminActivity(a)
		p.Status = state(a, s.repo.now())
		out.Items = append(out.Items, p)
	}
	return out, nil
}
func (s *AdminService) SetLotteryStatus(ctx context.Context, v *adminv1.LotteryStatusRequest) (*adminv1.LotteryActivity, error) {
	if v.Status != "live" && v.Status != "paused" && v.Status != "ended" && v.Status != "archived" {
		return nil, bad("活动状态无效")
	}
	e := data.Tx(ctx, s.repo.Data, func(ctx context.Context) error {
		a, e := s.repo.activity(ctx, v.Id, true)
		if e != nil {
			return e
		}
		if a.Revision != v.Revision {
			return bad("活动已被修改，请刷新后重试")
		}
		if state(a, s.repo.now()) == "ended" && v.Status != "ended" && v.Status != "archived" {
			return bad("已结束活动不能恢复，请新建或复制活动")
		}
		if a.Status == "archived" {
			return bad("已归档活动不能恢复")
		}
		ps, e := s.repo.prizes(ctx, a.ID)
		if e != nil {
			return e
		}
		if v.Status == "live" {
			if a.Status == "ended" || !s.repo.now().Before(a.EndAt) {
				return bad("活动已结束，请复制为新活动")
			}
			if e = s.repo.ready(ctx, ps); e != nil {
				return e
			}
		}
		c := data.Client(ctx, s.repo.Data)
		u := c.LotteryActivity.UpdateOneID(a.ID).SetStatus(v.Status).AddRevision(1)
		if v.Status == "live" {
			u = u.SetPublished(true)
		}
		a, e = u.Save(ctx)
		if e != nil {
			return e
		}
		_, e = c.LotteryRevision.Create().SetSubsiteID(site(ctx)).SetActivityID(a.ID).SetRevision(a.Revision).SetAdminID(actor(ctx)).SetSnapshot(snapshot(a, ps)).Save(ctx)
		return e
	})
	if e != nil {
		return nil, e
	}
	return s.GetLotteryActivity(ctx, &adminv1.LotteryIDRequest{Id: v.Id})
}
func (s *AdminService) GrantLotteryChances(ctx context.Context, v *adminv1.LotteryGrantRequest) (*emptypb.Empty, error) {
	e := s.repo.Grant(ctx, v.Id, v.UserId, v.Count, v.Remark, v.RequestKey)
	return &emptypb.Empty{}, e
}
func adminDraw(d *ent.LotteryDraw) *adminv1.LotteryDraw {
	return &adminv1.LotteryDraw{DrawNo: d.DrawNo, UserId: d.UserID, ActivityId: d.ActivityID, ActivityName: d.ActivityName, PrizeId: d.PrizeID, PrizeName: d.PrizeName, Mode: d.Mode, Status: d.Status, CreatedAt: unix(d.CreatedAt), DeliveredAt: unix(d.DeliveredAt), AdminId: d.AdminID, Remark: d.Remark, CostCents: d.Cost, ProductId: d.ProductID, SkuId: d.SkuID, Revision: d.Revision}
}
func (s *AdminService) ListLotteryDraws(ctx context.Context, v *adminv1.LotteryListRequest) (*adminv1.LotteryDrawList, error) {
	c := data.Client(ctx, s.repo.Data)
	q := c.LotteryDraw.Query().Where(lotterydraw.SubsiteID(site(ctx)))
	if !v.IncludeMissed {
		q = q.Where(lotterydraw.StatusNEQ("missed"))
	}
	if v.ActivityId > 0 {
		q = q.Where(lotterydraw.ActivityID(v.ActivityId))
	}
	if v.UserId > 0 {
		q = q.Where(lotterydraw.UserID(v.UserId))
	}
	if v.PrizeId > 0 {
		q = q.Where(lotterydraw.PrizeID(v.PrizeId))
	}
	if v.Status != "" {
		q = q.Where(lotterydraw.Status(v.Status))
	}
	if v.StartAt > 0 {
		q = q.Where(lotterydraw.CreatedAtGTE(time.Unix(v.StartAt, 0)))
	}
	if v.EndAt > 0 {
		q = q.Where(lotterydraw.CreatedAtLTE(time.Unix(v.EndAt, 0)))
	}
	if v.Keyword != "" {
		ids, e := c.User.Query().Where(user.UsernameContains(v.Keyword)).Limit(1000).IDs(ctx)
		if e != nil {
			return nil, e
		}
		q = q.Where(lotterydraw.Or(lotterydraw.DrawNo(v.Keyword), lotterydraw.UserIDIn(ids...)))
	}
	total, e := q.Clone().Count(ctx)
	if e != nil {
		return nil, e
	}
	off, size := paging(v.Page, v.PageSize)
	rows, e := q.Order(ent.Desc(lotterydraw.FieldID)).Offset(off).Limit(size).All(ctx)
	if e != nil {
		return nil, e
	}
	ids := []uint64{}
	for _, d := range rows {
		ids = append(ids, d.UserID)
	}
	users, e := c.User.Query().Where(user.IDIn(ids...)).All(ctx)
	if e != nil {
		return nil, e
	}
	names := map[uint64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	out := &adminv1.LotteryDrawList{Total: int64(total)}
	for _, d := range rows {
		p := adminDraw(d)
		p.Username = names[d.UserID]
		out.Items = append(out.Items, p)
	}
	return out, nil
}
func (s *AdminService) RevealLotteryDraw(ctx context.Context, v *adminv1.LotteryDrawRequest) (*adminv1.LotteryDraw, error) {
	noCache(ctx)
	d, e := s.repo.Result(ctx, v.DrawNo, 0)
	if e != nil {
		return nil, e
	}
	if e = s.repo.AuditReveal(ctx, d); e != nil {
		return nil, e
	}
	out := adminDraw(d)
	out.Content, e = s.repo.Content(ctx, d)
	return out, e
}
func (s *AdminService) DeliverLotteryDraw(ctx context.Context, v *adminv1.LotteryDrawRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, s.repo.Deliver(ctx, v.DrawNo, v.Remark)
}
func (s *AdminService) ListLotteryHistory(ctx context.Context, v *adminv1.LotteryIDRequest) (*adminv1.LotteryHistoryList, error) {
	rows, e := data.Client(ctx, s.repo.Data).LotteryRevision.Query().Where(lotteryrevision.ActivityID(v.Id), lotteryrevision.SubsiteID(site(ctx))).Order(ent.Desc(lotteryrevision.FieldRevision)).Limit(100).All(ctx)
	if e != nil {
		return nil, e
	}
	out := &adminv1.LotteryHistoryList{}
	for _, r := range rows {
		raw, e := json.Marshal(r.Snapshot)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, &adminv1.LotteryHistory{Revision: r.Revision, CreatedAt: unix(r.CreatedAt), AdminId: r.AdminID, SnapshotJson: string(raw)})
	}
	return out, nil
}
