package supply

import (
	"context"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"github.com/google/uuid"
	"slices"
	"strings"
	"time"
)

type StoreSMSChannelService struct {
	storefrontv1.UnimplementedStoreSMSChannelServiceServer
	gw *Gateway
}

func NewStoreSMSChannelService(g *Gateway) *StoreSMSChannelService {
	return &StoreSMSChannelService{gw: g}
}

type retailChannel struct {
	p        *ent.Product
	conn     *ent.SupplyConnection
	mapping  *ent.SupplyMapping
	client   supplyport.SMSChannelClient
	revision string
	user     uint64
	rule     productPricingRule
}

func (s *StoreSMSChannelService) channel(ctx context.Context, id uint64, requireLogin bool) (*retailChannel, error) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store, private")
	}
	claims := identity.ClaimsFromContext(ctx)
	member := claims != nil && claims.Realm == authn.RealmUser && claims.Subject != 0
	if requireLogin && !member {
		return nil, errors.Unauthorized("sms.LOGIN_REQUIRED", "请登录使用接码商品")
	}
	if tenancy.FromContext(ctx).SubsiteID != 0 {
		return nil, errors.BadRequest("sms.MAIN_SITE_ONLY", "接码商品仅支持主站")
	}
	base, err := data.BaseCurrency(ctx, s.gw.repo.data)
	if err != nil {
		return nil, err
	}
	if base != "CNY" {
		return nil, errors.BadRequest("sms.CURRENCY_UNSUPPORTED", "接码仅支持人民币基础币种")
	}
	p, e := data.ProductForDelivery(ctx, data.Client(ctx, s.gw.repo.data), 0, id)
	if e != nil || p.ProductKind != supplyport.SMSProductKind || p.DeliveryKind != supplyport.SMSDelivery || (p.Status != 1 && !(member && p.Status == 2)) || p.UpstreamSourceID == 0 {
		return nil, errors.NotFound("sms.PRODUCT_UNAVAILABLE", "接码商品不可售")
	}
	hidden, e := data.HiddenCategoryIDs(ctx, data.Client(ctx, s.gw.repo.data), 0)
	if e != nil {
		return nil, e
	}
	if data.CategoryHidden(hidden, p.CategoryID) {
		return nil, errors.NotFound("sms.PRODUCT_UNAVAILABLE", "接码商品不可售")
	}
	var user uint64
	if member {
		user = claims.Subject
	}

	conn, e := s.gw.repo.GetConnection(ctx, p.UpstreamSourceID)
	if e != nil {
		return nil, e
	}
	if conn.Driver != "zcard" || string(conn.Status) != "active" || conn.ExchangeRate != 1 {
		return nil, errors.BadRequest("sms.CONNECTION_UNAVAILABLE", "接码须使用启用的人民币 ZCard 货源")
	}
	m, e := s.gw.repo.GetMapping(ctx, conn.ID, p.UpstreamProductCode, "")
	if e != nil || m.LocalProductID != p.ID {
		return nil, errors.BadRequest("sms.MAPPING_INVALID", "接码商品映射无效")
	}
	rule, e := readProductRule(m.PricingOverride)
	if e != nil {
		return nil, e
	}
	if rule == nil || rule.Mode == PriceModePending {
		return nil, errors.BadRequest("sms.PRICING_REQUIRED", "请先设置接码商品加价规则")
	}
	if _, fixed := m.PricingOverride["price"]; fixed {
		return nil, errors.BadRequest("sms.PRICING_INVALID", "接码商品须按选项加价，不能设置统一固定售价")
	}
	a, e := s.gw.adapterFor(ctx, conn.ID)
	if e != nil {
		return nil, e
	}
	ping, e := a.Ping(ctx)
	if e != nil {
		return nil, e
	}
	for _, cap := range []string{supplyport.SMSProductCatalog, supplyport.SMSProductPurchase, supplyport.SMSPolling} {
		if !slices.Contains(ping.Capabilities, cap) {
			return nil, errors.BadRequest("sms.UPGRADE_REQUIRED", "货源尚不支持渠道商品，请升级供货端")
		}
	}
	if ping.Currency != "CNY" || ping.Balance < 0 {
		return nil, errors.BadRequest("sms.ACCOUNT_UNAVAILABLE", "供货账户尚未就绪")
	}
	client, ok := a.(supplyport.SMSChannelClient)
	if !ok {
		return nil, errors.BadRequest("sms.UNSUPPORTED", "货源不支持渠道商品")
	}
	return &retailChannel{p: p, conn: conn, mapping: m, client: client, revision: data.SMSRetailPricingRevision(conn, m), user: user, rule: *rule}, nil
}
func channelFilter(country, platform, keyword string, page, size int32) (supplyport.SMSChannelFilter, error) {
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 50
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 || len(country) > 64 || len(platform) > 64 || len([]rune(keyword)) > 100 {
		return supplyport.SMSChannelFilter{}, errors.BadRequest("sms.INVALID_FILTER", "筛选或分页无效")
	}
	return supplyport.SMSChannelFilter{CountryID: country, PlatformID: platform, Keyword: strings.TrimSpace(keyword), Page: int(page), PageSize: int(size)}, nil
}
func (ch *retailChannel) price(cost int64) (int64, error) {
	price := ch.rule.price(ch.conn, cost)
	if !money.ValidCents(cost) || cost <= 0 || !money.ValidCents(price) || price < cost {
		return 0, errors.BadRequest("sms.PRICE_INVALID", "接码售价须大于零且不低于供货价")
	}
	return price, nil
}
func (s *StoreSMSChannelService) unchanged(ctx context.Context, ch *retailChannel) error {
	c := data.Client(ctx, s.gw.repo.data)
	p, e := c.Product.Get(ctx, ch.p.ID)
	if e != nil {
		return e
	}
	rev, e := data.SMSRetailCurrentRevision(ctx, c, p)
	if e != nil {
		return e
	}
	if p.LockVersion != ch.p.LockVersion || p.Status < 1 || rev != ch.revision {
		return errors.Conflict("sms.QUOTE_CHANGED", "商品或加价规则已变化，请刷新选价")
	}
	return nil
}
func (s *StoreSMSChannelService) Options(ctx context.Context, r *storefrontv1.SMSChannelBrowseRequest) (*storefrontv1.SMSChannelOptionsReply, error) {
	f, e := channelFilter(r.CountryId, r.PlatformId, r.Keyword, r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	ch, e := s.channel(ctx, r.ProductId, false)
	if e != nil {
		return nil, e
	}
	options, e := ch.client.SMSChannelOptions(ctx, ch.p.UpstreamProductCode, f)
	if e != nil {
		return nil, e
	}
	if e = s.unchanged(ctx, ch); e != nil {
		return nil, e
	}
	out := &storefrontv1.SMSChannelOptionsReply{}
	for _, v := range options.Countries {
		out.Countries = append(out.Countries, &storefrontv1.SMSChannelOption{Id: v.ID, Name: v.Name})
	}
	for _, v := range options.Platforms {
		out.Platforms = append(out.Platforms, &storefrontv1.SMSChannelOption{Id: v.ID, Name: v.Name})
	}
	return out, nil
}
func (s *StoreSMSChannelService) Offers(ctx context.Context, r *storefrontv1.SMSChannelBrowseRequest) (*storefrontv1.SMSChannelOffersReply, error) {
	f, e := channelFilter(r.CountryId, r.PlatformId, r.Keyword, r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	ch, e := s.channel(ctx, r.ProductId, false)
	if e != nil {
		return nil, e
	}
	offers, e := ch.client.SMSChannelOffers(ctx, ch.p.UpstreamProductCode, f)
	if e != nil {
		return nil, e
	}
	if e = s.unchanged(ctx, ch); e != nil {
		return nil, e
	}
	out := &storefrontv1.SMSChannelOffersReply{HasMore: offers.HasMore}
	for _, v := range offers.Offers {
		price, e := ch.price(int64(v.PriceCents))
		if e != nil {
			return nil, e
		}
		out.Offers = append(out.Offers, &storefrontv1.SMSChannelOffer{OfferId: v.OfferID, Name: v.Name, PriceCents: price, Stock: v.Stock})
	}
	return out, nil
}
func (s *StoreSMSChannelService) Quote(ctx context.Context, r *storefrontv1.SMSChannelQuoteRequest) (*storefrontv1.SMSChannelQuoteReply, error) {
	if !data.SMSSalesEnabled() {
		return nil, errors.BadRequest("sms.SALES_CLOSED", "接码新购已暂停")
	}
	if r.OfferId == "" || len(r.OfferId) > 256 {
		return nil, errors.BadRequest("sms.INVALID_OFFER", "请选择报价选项")
	}
	f, e := channelFilter(r.CountryId, r.PlatformId, "", r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	ch, e := s.channel(ctx, r.ProductId, true)
	if e != nil {
		return nil, e
	}
	quote, e := ch.client.SMSChannelQuote(ctx, ch.p.UpstreamProductCode, r.OfferId, f)
	if e != nil {
		return nil, e
	}
	price, e := ch.price(int64(quote.AmountCents))
	if e != nil {
		return nil, e
	}
	expiry := min(int64(quote.ExpiresAt), time.Now().Add(5*time.Minute).Unix())
	if expiry <= time.Now().Unix()+5 {
		return nil, errors.Conflict("sms.QUOTE_EXPIRED", "报价已过期，请重新选价")
	}
	var saved *ent.SMSRetailQuote
	e = data.Tx(ctx, s.gw.repo.data, func(ctx context.Context) error {
		// Buying does not edit catalog settings; an admin lock must not block sales.
		if _, e := data.Client(ctx, s.gw.repo.data).Product.Update().Where(product.ID(ch.p.ID), product.LockVersion(ch.p.LockVersion)).AddPrice(0).Save(ctx); e != nil {
			return e
		}
		if e := data.Client(ctx, s.gw.repo.data).SupplyConnection.UpdateOneID(ch.conn.ID).AddRetryMax(0).Exec(ctx); e != nil {
			return e
		}
		if e := s.unchanged(ctx, ch); e != nil {
			return e
		}
		var e error
		saved, e = data.Client(ctx, s.gw.repo.data).SMSRetailQuote.Create().SetID(uuid.NewString()).SetUserID(ch.user).SetProductID(ch.p.ID).SetConnectionID(ch.conn.ID).SetConnectionIdentity(data.SMSConnectionIdentity(ch.conn)).SetPricingRevision(ch.revision).SetProductRevision(ch.p.LockVersion).SetUpstreamQuoteID(quote.QuoteID).SetUpstreamProductID(ch.p.UpstreamProductCode).SetCostCents(int64(quote.AmountCents)).SetAmountCents(price).SetExpiresAt(expiry).SetOfferName(quote.OfferName).SetSelection(map[string]string{"country_id": r.CountryId, "platform_id": r.PlatformId, "offer_name": quote.OfferName}).Save(ctx)
		return e
	})
	if e != nil {
		return nil, e
	}
	return &storefrontv1.SMSChannelQuoteReply{QuoteId: saved.ID, AmountCents: price, Currency: "CNY", ExpiresAt: expiry, OfferName: quote.OfferName}, nil
}
