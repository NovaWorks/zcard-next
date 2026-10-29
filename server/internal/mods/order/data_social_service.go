package order

import (
	"context"
	"encoding/json"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"time"
)

type StoreSocialProductService struct {
	storefrontv1.UnimplementedStoreSocialProductServiceServer
	uc *OrderUsecase
}

func NewStoreSocialProductService(uc *OrderUsecase) *StoreSocialProductService {
	return &StoreSocialProductService{uc: uc}
}
func socialUser(ctx context.Context) (uint64, error) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		tr.ReplyHeader().Set("Cache-Control", "no-store, private")
	}
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil || claims.Subject == 0 || claims.Realm != authn.RealmUser {
		return 0, errors.Unauthorized("social.LOGIN_REQUIRED", "请登录购买服务")
	}
	if tenancy.FromContext(ctx).SubsiteID != 0 {
		return 0, errors.BadRequest("social.MAIN_SITE_ONLY", "服务目前仅支持主站")
	}
	return claims.Subject, nil
}
func (s *StoreSocialProductService) product(ctx context.Context, id uint64) (*ent.Product, supplyport.SocialClient, error) {
	if _, e := socialUser(ctx); e != nil {
		return nil, nil, e
	}
	currency, e := data.BaseCurrency(ctx, s.uc.Data)
	if e != nil {
		return nil, nil, e
	}
	if currency != "CNY" {
		return nil, nil, errors.BadRequest("social.CURRENCY_UNSUPPORTED", "服务购买仅支持人民币基础币种")
	}
	c := data.Client(ctx, s.uc.Data)
	p, e := data.ProductForDelivery(ctx, c, 0, id)
	if e != nil || p.DeliveryKind != supplyport.SocialDelivery || p.ProductKind != supplyport.SocialProductKind || p.Status != 1 || p.UpstreamSourceID == 0 {
		return nil, nil, errors.NotFound("social.PRODUCT_UNAVAILABLE", "服务商品不可售")
	}
	hidden, e := data.HiddenCategoryIDs(ctx, c, 0)
	if e != nil {
		return nil, nil, e
	}
	if data.CategoryHidden(hidden, p.CategoryID) {
		return nil, nil, errors.NotFound("social.PRODUCT_UNAVAILABLE", "服务商品不可售")
	}
	conn, e := c.SupplyConnection.Get(ctx, p.UpstreamSourceID)
	if e != nil || string(conn.Status) != "active" || conn.Driver != "zcard" || conn.ExchangeRate != 1 {
		return nil, nil, errors.BadRequest("social.CONNECTION_UNAVAILABLE", "服务货源暂不可购")
	}
	gw, ok := s.uc.StockGate.(supplyport.SocialClientGateway)
	if !ok {
		return nil, nil, errors.ServiceUnavailable("social.UNAVAILABLE", "服务暂不可用")
	}
	client, e := gw.OpenSocial(ctx, p.UpstreamSourceID)
	return p, client, e
}
func (s *StoreSocialProductService) Schema(ctx context.Context, req *storefrontv1.SocialProductRequest) (*structpb.Struct, error) {
	p, client, e := s.product(ctx, req.ProductId)
	if e != nil {
		return nil, e
	}
	schema, e := client.SocialSchema(ctx, p.UpstreamProductCode)
	if e != nil {
		return nil, e
	}
	b, e := json.Marshal(schema)
	if e != nil {
		return nil, e
	}
	var obj map[string]any
	if json.Unmarshal(b, &obj) != nil {
		return nil, errors.InternalServer("social.INVALID_SCHEMA", "服务定义无法读取")
	}
	return structpb.NewStruct(obj)
}
func (s *StoreSocialProductService) Quote(ctx context.Context, req *storefrontv1.SocialRetailQuoteRequest) (*storefrontv1.SocialRetailQuoteReply, error) {
	uid, e := socialUser(ctx)
	if e != nil {
		return nil, e
	}
	p, client, e := s.product(ctx, req.ProductId)
	if e != nil {
		return nil, e
	}
	if s.uc.AccessCipher == nil {
		return nil, errors.ServiceUnavailable("social.UNAVAILABLE", "服务暂不可用")
	}
	c := data.Client(ctx, s.uc.Data)
	conn, e := c.SupplyConnection.Get(ctx, p.UpstreamSourceID)
	if e != nil {
		return nil, e
	}
	identity := data.SocialConnectionIdentity(conn)
	schema, e := client.SocialSchema(ctx, p.UpstreamProductCode)
	if e != nil {
		return nil, e
	}
	if schema == nil || schema.Version != 1 || schema.Revision == "" || !schema.Purchasable {
		return nil, errors.BadRequest("social.NOT_PURCHASABLE", "此服务暂不支持购买")
	}
	if req.SchemaRevision != schema.Revision {
		return nil, errors.Conflict("social.SCHEMA_CHANGED", "服务规则已更新，请刷新后填写")
	}
	params, quantity, e := supplyport.ValidateSocialParameters(*schema, supplyport.SocialParameters(req.Parameters))
	if e != nil {
		return nil, errors.BadRequest("social.INVALID_PARAMETERS", e.Error())
	}
	quote, e := client.QuoteSocial(ctx, p.UpstreamProductCode, params, schema.Revision)
	if e != nil {
		return nil, e
	}
	if quote == nil || quote.QuoteID == "" || quote.ProductID != p.UpstreamProductCode || quote.SchemaRevision != schema.Revision || quote.Currency != "CNY" || quote.AmountCents <= 0 || !money.ValidCents(int64(quote.AmountCents)) || quote.ExpiresAt <= supplyport.Integer(time.Now().Unix()) || socialHash(quote.Parameters) != socialHash(params) {
		return nil, errors.BadRequest("social.INVALID_QUOTE", "供货报价无效，请稍后重试")
	}
	pricer, ok := s.uc.StockGate.(interface {
		PriceSocialRetail(context.Context, uint64, uint64, int64) (int64, string, error)
	})
	if !ok {
		return nil, errors.ServiceUnavailable("social.PRICING_UNAVAILABLE", "服务定价尚未配置")
	}
	amount, pricingRevision, e := pricer.PriceSocialRetail(ctx, p.UpstreamSourceID, p.ID, int64(quote.AmountCents))
	if e != nil {
		return nil, e
	}
	if amount < int64(quote.AmountCents) || !money.ValidCents(amount) {
		return nil, errors.BadRequest("social.INVALID_PRICE", "服务总价无效")
	}
	raw, _ := json.Marshal(params)
	sealed, e := s.uc.AccessCipher.Seal(string(raw), p.ID, p.SubsiteID)
	if e != nil {
		return nil, e
	}
	schemaJSON, _ := json.Marshal(schema)
	id := uuid.NewString()
	expires := int64(quote.ExpiresAt)
	if cap := time.Now().Add(5 * time.Minute).Unix(); expires > cap {
		expires = cap
	}
	e = data.Tx(ctx, s.uc.Data, func(ctx context.Context) error {
		c := data.Client(ctx, s.uc.Data)
		if e := c.Product.UpdateOneID(p.ID).AddLockVersion(0).Exec(ctx); e != nil {
			return e
		}
		current, e := c.Product.Get(ctx, p.ID)
		if e != nil {
			return e
		}
		currentConn, e := c.SupplyConnection.Get(ctx, conn.ID)
		if e != nil {
			return e
		}
		m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(p.UpstreamProductCode), supplymapping.UpstreamSku("")).Only(ctx)
		if e != nil {
			return e
		}
		if current.LockVersion != p.LockVersion || current.Status != 1 || current.UpstreamSourceID != conn.ID || current.UpstreamProductCode != p.UpstreamProductCode || identity != data.SocialConnectionIdentity(currentConn) || pricingRevision != data.SocialRetailPricingRevision(currentConn, m) {
			return errors.Conflict("social.QUOTE_CHANGED", "服务或定价已变化，请重新报价")
		}
		return c.SocialQuote.Create().SetID(id).SetRole("retail").SetUserID(uid).SetSubsiteID(0).SetProductID(p.ID).SetConnectionID(conn.ID).SetConnectionIdentity(identity).SetProductRevision(p.LockVersion).SetSchemaRevision(schema.Revision).SetPricingRevision(pricingRevision).SetUpstreamProductID(p.UpstreamProductCode).SetParamsCipher(sealed).SetSchemaJSON(string(schemaJSON)).SetSupplyQuoteID(quote.QuoteID).SetCurrency("CNY").SetAmountCents(amount).SetCostCents(int64(quote.AmountCents)).SetExpiresAt(expires).Exec(ctx)
	})
	if e != nil {
		return nil, e
	}
	return &storefrontv1.SocialRetailQuoteReply{QuoteId: id, AmountCents: amount, Currency: "CNY", ExpiresAt: expires, Quantity: quantity}, nil
}
func (s *StoreSocialProductService) Buy(ctx context.Context, req *storefrontv1.SocialRetailBuyRequest) (*storefrontv1.CreateOrderReply, error) {
	uid, e := socialUser(ctx)
	if e != nil {
		return nil, e
	}
	if _, e := uuid.Parse(req.RequestId); e != nil {
		return nil, errors.BadRequest("social.REQUEST_ID", "购买请求标识无效")
	}
	result, e := s.uc.CreateOrder(ctx, CreateOrderInput{SocialQuoteID: req.QuoteId, UserID: uid, SubsiteID: 0, ClientIP: orderClientIP(ctx), IdempotencyKey: "social_" + req.RequestId, Items: []OrderItemInput{{ProductID: req.ProductId, Quantity: 1}}})
	if e != nil {
		return nil, mapOrderErr(e)
	}
	return &storefrontv1.CreateOrderReply{OrderNo: result.OrderNo, TotalCents: result.TotalCents, ExpiresAt: result.ExpiresAt.Unix()}, nil
}
