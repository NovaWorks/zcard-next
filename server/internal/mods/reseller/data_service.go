package reseller

// 管理面 API（P3-04 主站面）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/sanitize"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminResellerService 主站分站管理。
type AdminResellerService struct {
	adminv1.UnimplementedAdminResellerServiceServer
	repo *ResellerRepo
}

// NewAdminResellerService 构造。
func NewAdminResellerService(repo *ResellerRepo) *AdminResellerService {
	return &AdminResellerService{repo: repo}
}

// ReviewApply 审核。
func (s *AdminResellerService) ReviewApply(ctx context.Context, req *adminv1.ReviewApplyRequest) (*adminv1.ResellerProfile, error) {
	p, err := s.repo.Review(ctx, req.GetId(), req.GetApprove(), req.GetReason(),
		adminUID(ctx), req.GetDefaultMarkupPercent(), req.GetMaxMarkupPercent(), req.GetConfirmDays())
	if err != nil {
		return nil, mapErr(err)
	}
	return toProfilePB(p), nil
}

// ListProfiles 列表。
func (s *AdminResellerService) ListProfiles(ctx context.Context, req *adminv1.ListProfilesRequest) (*adminv1.ListProfilesReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListProfiles(ctx, req.GetStatus(), page, size)
	if err != nil {
		return nil, mapErr(err)
	}
	reply := &adminv1.ListProfilesReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, p := range rows {
		reply.Profiles = append(reply.Profiles, toProfilePB(p))
	}
	return reply, nil
}

// UpsertPricing 定价规则。
func (s *AdminResellerService) UpsertPricing(ctx context.Context, req *adminv1.UpsertPricingRequest) (*emptypb.Empty, error) {
	// 上限取分站 profile.max_markup_percent（subsite_id = profile 主键）
	max := 0.0
	if p, err := s.repo.GetProfile(ctx, req.GetSubsiteId()); err == nil {
		max = p.MaxMarkupPercent
	}
	if _, err := s.repo.UpsertPricing(ctx, req.GetSubsiteId(), req.GetProductId(), req.GetSkuId(), req.GetMode(), req.GetValue(), max); err != nil {
		return nil, mapErr(err)
	}
	return &emptypb.Empty{}, nil
}

// Ledger 账本流水。
func (s *AdminResellerService) Ledger(ctx context.Context, req *adminv1.LedgerRequest) (*adminv1.LedgerReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.Ledger(ctx, req.GetSubsiteId(), req.GetStatus(), page, size)
	if err != nil {
		return nil, mapErr(err)
	}
	reply := &adminv1.LedgerReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, e := range rows {
		item := &adminv1.LedgerEntry{
			Id: e.ID, OrderId: e.OrderID, Type: string(e.Type), Amount: e.Amount,
			Status: string(e.Status), IdempotencyKey: e.IdempotencyKey,
			CreatedAt: e.CreatedAt.Unix(),
		}
		if !e.AvailableAt.IsZero() {
			item.AvailableAt = e.AvailableAt.Unix()
		}
		reply.Entries = append(reply.Entries, item)
	}
	return reply, nil
}

// Balance 余额（缓存 + 重算对账）。
func (s *AdminResellerService) Balance(ctx context.Context, req *adminv1.BalanceRequest) (*adminv1.BalanceReply, error) {
	acc, err := s.repo.GetBalance(ctx, req.GetSubsiteId())
	if err != nil {
		return nil, mapErr(err)
	}
	ra, rl, rn, err := s.repo.RecomputeBalance(ctx, req.GetSubsiteId())
	if err != nil {
		return nil, mapErr(err)
	}
	return &adminv1.BalanceReply{
		Available: acc.Available, Locked: acc.Locked, Negative: acc.Negative,
		RecomputedAvailable: ra, RecomputedLocked: rl, RecomputedNegative: rn,
	}, nil
}

func toProfilePB(p *ent.ResellerProfile) *adminv1.ResellerProfile {
	out := &adminv1.ResellerProfile{
		Id: p.ID, UserId: p.UserID, Status: string(p.Status),
		ApplyReason: p.ApplyReason, RejectReason: p.RejectReason,
		Level:                int32(p.Level),
		DefaultMarkupPercent: p.DefaultMarkupPercent,
		MaxMarkupPercent:     p.MaxMarkupPercent,
		ConfirmDays:          p.ConfirmDays,
		CreatedAt:            p.CreatedAt.Unix(),
	}
	if !p.ReviewedAt.IsZero() {
		out.ReviewedAt = p.ReviewedAt.Unix()
	}
	return out
}

func mapErr(err error) error {
	switch err {
	case ErrNotFound:
		return errors.NotFound("reseller.NOT_FOUND", "记录不存在")
	case ErrMarkupExceed:
		return errors.BadRequest("reseller.MARKUP_EXCEED", "超过分站加价率上限")
	case ErrBelowBase:
		return errors.BadRequest("reseller.BELOW_BASE", "分站价不得低于主站基础价")
	}
	return errors.InternalServer("reseller.ERROR", err.Error())
}

func adminUID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

func pageParams(page, pageSize int32) (int, int) {
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

// ── 分站主自服务面（reseller: 域；数据按本人 profile 隔离）─────────

// myProfile 当前登录分站主的 profile（claims.Subject → user_id 匹配）。
func (s *AdminResellerService) myProfile(ctx context.Context) (*ent.ResellerProfile, error) {
	uid := adminUID(ctx)
	p, err := s.repo.ProfileByUser(ctx, uid)
	if err != nil {
		return nil, errors.Forbidden("reseller.NOT_RESELLER", "非分站主或未过审")
	}
	if string(p.Status) != "approved" {
		return nil, errors.Forbidden("reseller.NOT_APPROVED", "分站未过审")
	}
	return p, nil
}

// MySites 名下域名。
func (s *AdminResellerService) MySites(ctx context.Context, _ *adminv1.MySitesRequest) (*adminv1.MySitesReply, error) {
	p, err := s.myProfile(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := s.repo.ListSites(ctx, p.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	reply := &adminv1.MySitesReply{}
	for _, site := range sites {
		reply.Sites = append(reply.Sites, toSitePB(site))
	}
	return reply, nil
}

// AddSite 登记域名（本人 profile 下）。
func (s *AdminResellerService) AddSite(ctx context.Context, req *adminv1.AddSiteRequest) (*adminv1.ResellerSiteItem, error) {
	p, err := s.myProfile(ctx)
	if err != nil {
		return nil, err
	}
	site, err := s.repo.AddSite(ctx, p.ID, req.GetDomain(), req.GetSiteName(), req.GetIsPrimary())
	if err != nil {
		return nil, mapErr(err)
	}
	return toSitePB(site), nil
}

// VerifySite 触发验证（本人站点归属校验）。
func (s *AdminResellerService) VerifySite(ctx context.Context, req *adminv1.VerifySiteRequest) (*adminv1.VerifySiteReply, error) {
	p, err := s.myProfile(ctx)
	if err != nil {
		return nil, err
	}
	site, err := s.repo.GetSite(ctx, req.GetSiteId())
	if err != nil {
		return nil, mapErr(err)
	}
	if site.ProfileID != p.ID {
		return nil, errors.Forbidden("reseller.NOT_YOUR_SITE", "非本人域名")
	}
	ok, method, err := s.repo.VerifySite(ctx, site.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &adminv1.VerifySiteReply{Verified: ok, Method: method}, nil
}

// SetWhitelabel 白标设置（站名/LOGO/favicon）。
func (s *AdminResellerService) SetWhitelabel(ctx context.Context, req *adminv1.SetWhitelabelRequest) (*emptypb.Empty, error) {
	p, err := s.myProfile(ctx)
	if err != nil {
		return nil, err
	}
	site, err := s.repo.GetSite(ctx, req.GetSiteId())
	if err != nil {
		return nil, mapErr(err)
	}
	if site.ProfileID != p.ID {
		return nil, errors.Forbidden("reseller.NOT_YOUR_SITE", "非本人域名")
	}
	if err := s.repo.SetWhitelabel(ctx, site.ID, req.GetSiteName(), req.GetLogo(), req.GetFavicon()); err != nil {
		return nil, mapErr(err)
	}
	return &emptypb.Empty{}, nil
}

// CreateProduct 分站主：自营商品上架（等级权限位——level>=1 允许自助上架；
// subsite_id = 本人 profile.ID，与域名访问的下单上下文一致）。
func (s *AdminResellerService) CreateProduct(ctx context.Context, req *adminv1.CreateResellerProductRequest) (*adminv1.ResellerProduct, error) {
	p, err := s.myProfile(ctx)
	if err != nil {
		return nil, err
	}
	if p.Level < 1 {
		return nil, errors.Forbidden("reseller.NO_LIST_PERMISSION", "当前等级无自助上架权限")
	}
	if req.GetName() == "" || !money.ValidCents(req.GetPriceCents()) || !money.ValidCents(req.GetFactoryPriceCents()) {
		return nil, errors.BadRequest("reseller.PRODUCT_INVALID", "名称必填、价格与成本须为非负且不超上限")
	}
	prod, err := s.repo.CreateOwnProduct(ctx, p.ID, OwnProductInput{
		Name:         req.GetName(),
		CategoryID:   req.GetCategoryId(),
		Description:  sanitize.HTML(req.GetDescription()),
		Cover:        req.GetCover(),
		Price:        req.GetPriceCents(),
		FactoryPrice: req.GetFactoryPriceCents(),
		StockType:    req.GetStockType(),
		DeliveryMode: req.GetDeliveryMode(),
		StockVisible: req.GetStockVisible(),
		Sort:         req.GetSort(),
		Status:       int8(req.GetStatus()),
	})
	if err != nil {
		return nil, errors.InternalServer("reseller.PRODUCT_CREATE_FAILED", "上架失败: "+err.Error())
	}
	out := &adminv1.ResellerProduct{
		Id: prod.ID, SubsiteId: prod.SubsiteID, Name: prod.Name, Slug: prod.Slug,
		PriceCents: prod.Price, Status: int32(prod.Status),
	}
	if !prod.CreatedAt.IsZero() {
		out.CreatedAt = prod.CreatedAt.Unix()
	}
	return out, nil
}

func toSitePB(site *ent.ResellerSite) *adminv1.ResellerSiteItem {
	return &adminv1.ResellerSiteItem{
		Id: site.ID, ProfileId: site.ProfileID, Domain: site.Domain,
		VerificationStatus: string(site.VerificationStatus),
		VerificationToken:  site.VerificationToken,
		IsPrimary:          site.IsPrimary, SiteName: site.SiteName, Logo: site.Logo,
	}
}
