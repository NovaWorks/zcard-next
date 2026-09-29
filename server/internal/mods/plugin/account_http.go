package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	ma "github.com/NovaWorks/zcard-next/server/internal/platform/marketaccount"
	ke "github.com/go-kratos/kratos/v3/errors"
	kh "github.com/go-kratos/kratos/v3/transport/http"
)

func accountHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var remote *ma.RemoteError
	if errors.As(err, &remote) {
		return ke.New(remote.Status, remote.Code, "市场请求未完成")
	}
	if errors.Is(err, ma.ErrInvalid) {
		return ke.BadRequest("market.INVALID_REQUEST", "市场请求格式不正确")
	}
	if errors.Is(err, ma.ErrUnsupported) {
		return ke.New(503, "market.ACCOUNT_UNSUPPORTED", "此市场暂不支持账户或公开目录")
	}
	if _, ok := err.(*ke.Error); ok {
		return err
	}
	return ke.ServiceUnavailable("market.DEPENDENCY_UNAVAILABLE", "市场暂不可用，请稍后重试")
}
func accountOperation(id string) string {
	return "/zcard.api.admin.v1.AdminPluginService/MarketAccount_" + strings.ReplaceAll(id, "-", "_")
}
func (s *AdminPluginService) RegisterMarketAccount(srv *kh.Server) {
	for _, endpoint := range ma.Endpoints() {
		if (endpoint.Phase != "C3" && endpoint.Phase != "C4") || endpoint.Service != "instance" {
			continue
		}
		ep := endpoint
		srv.Route("/").Handle(ep.Method, ep.Path, func(c kh.Context) error {
			c.Response().Header().Set("Cache-Control", "no-store")
			c.Response().Header().Set("Pragma", "no-cache")
			if ep.Path == ma.ChallengePath {
				return s.serveSiteChallenge(c)
			}
			kh.SetOperation(c, accountOperation(ep.ID))
			out, e := c.Middleware(func(ctx context.Context, _ any) (any, error) {
				if _, err := s.authorize(ctx, "plugin:manage", true, false); err != nil {
					return nil, err
				}
				claims := authn.ClaimsFromContext(ctx)
				if claims == nil {
					return nil, viewExpired
				}
				if err := s.manager.repo.accountRate(ctx, "host", localAdminKey(claims)); err != nil {
					return nil, err
				}
				ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
				defer cancel()
				if ep.ID == "host-listings" || ep.ID == "host-listing" || ep.ID == "host-facets" {
					origin, err := s.manager.repo.marketOrigin(ctx)
					if err != nil {
						return nil, err
					}
					client, err := ma.NewClient(origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
					if err != nil {
						return nil, err
					}
					defer client.Close()
					switch ep.ID {
					case "host-listings":
						q, err := ma.ParseListingQuery(c.Request().URL.RawQuery)
						if err != nil {
							return nil, err
						}
						var out ma.ListingPage
						err = client.Call(ctx, "listings", "", q, &out)
						return out, err
					case "host-listing":
						if c.Request().URL.RawQuery != "" {
							return nil, ma.ErrInvalid
						}
						var out ma.Listing
						err = client.Call(ctx, "listing", "", ma.PluginPath{PluginID: c.Vars().Get("pluginId")}, &out)
						return out, err
					default:
						if c.Request().URL.RawQuery != "" {
							return nil, ma.ErrInvalid
						}
						var out ma.Facets
						err = client.Call(ctx, "facets", "", ma.Empty{}, &out)
						return out, err
					}
				}
				if ep.ID == "host-products" || ep.ID == "host-product" {
					origin, err := s.manager.repo.marketOrigin(ctx)
					if err != nil {
						return nil, err
					}
					client, err := ma.NewClient(origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"))
					if err != nil {
						return nil, err
					}
					defer client.Close()
					if ep.ID == "host-products" {
						q, err := ma.ParseProductQuery(c.Request().URL.RawQuery)
						if err != nil {
							return nil, err
						}
						var result ma.ProductPage
						err = client.Call(ctx, "products", "", q, &result)
						return result, err
					}
					if c.Request().URL.RawQuery != "" {
						return nil, ma.ErrInvalid
					}
					var result ma.Product
					err = client.Call(ctx, "product", "", ma.PluginPath{PluginID: c.Vars().Get("pluginId")}, &result)
					return result, err
				}
				if c.Request().URL.RawQuery != "" {
					return nil, ma.ErrInvalid
				}
				input, err := ma.NewMessage(ep.Request)
				if err != nil {
					return nil, err
				}
				raw, err := io.ReadAll(io.LimitReader(c.Request().Body, int64(ep.MaxRequestBytes)+1))
				if err != nil || ma.Decode(ep.Request, raw, input, ep.MaxRequestBytes) != nil {
					return nil, ma.ErrInvalid
				}
				if ep.ID == "host-view-start" || ep.ID == "host-start" {
					return s.manager.startAccountView(ctx, claims)
				}
				if ep.ID == "host-challenge" {
					return s.configureSiteChallenge(ctx, c, input.(*ma.Verification))
				}
				var proof ma.HostContext
				var account string
				var page ma.PageQuery
				switch in := input.(type) {
				case *ma.HostContext:
					proof = *in
				case *ma.HostConfirm:
					proof = ma.HostContext{ContextID: in.ContextID, ContextSecret: in.ContextSecret}
					account = in.AccountID
				case *ma.HostPage:
					proof = ma.HostContext{ContextID: in.ContextID, ContextSecret: in.ContextSecret}
					page = ma.PageQuery{Cursor: in.Cursor, Limit: in.Limit}
				default:
					return nil, ma.ErrInvalid
				}
				return s.manager.accountViewCall(ctx, claims, proof, strings.TrimPrefix(strings.TrimPrefix(ep.ID, "host-view-"), "host-"), account, page)
			})(c, nil)
			if e != nil {
				return accountHTTPError(e)
			}
			raw, e := json.Marshal(out)
			if e != nil {
				return accountHTTPError(ma.ErrUnavailable)
			}
			check, _ := ma.NewMessage(ep.Response)
			if ma.Decode(ep.Response, raw, check, ep.MaxResponseBytes) != nil {
				return accountHTTPError(ma.ErrUnavailable)
			}
			return c.JSON(200, out)
		})
	}
}
func (s *AdminPluginService) configureSiteChallenge(ctx context.Context, c kh.Context, in *ma.Verification) (any, error) {
	id, e := s.manager.bindingIdentity()
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(in.Origin)
	if e != nil || u.Host != c.Request().Host || in.Challenge.InstanceID != id || in.ExpiresAt <= time.Now().Unix() || in.ExpiresAt > time.Now().Add(ma.PairLifetime).Unix() {
		return nil, ma.ErrInvalid
	}
	s.manager.repo.marketMu.Lock()
	defer s.manager.repo.marketMu.Unlock()
	e = s.manager.repo.putMarketSetting(ctx, "site_challenge", in)
	return ma.Accepted{Accepted: true}, e
}
func (s *AdminPluginService) serveSiteChallenge(c kh.Context) error {
	ip, _, e := net.SplitHostPort(c.Request().RemoteAddr)
	if e != nil {
		ip = c.Request().RemoteAddr
	}
	if e = s.manager.repo.accountRate(c, "challenge_read", ip); e != nil {
		return accountHTTPError(e)
	}
	if c.Request().URL.RawQuery != "" {
		return ke.NotFound("market.NOT_FOUND", "验证不存在")
	}
	s.manager.repo.marketMu.Lock()
	defer s.manager.repo.marketMu.Unlock()
	var v ma.Verification
	if s.manager.repo.marketSetting(c, "site_challenge", &v) != nil {
		return ke.NotFound("market.NOT_FOUND", "验证不存在")
	}
	id, err := s.manager.bindingIdentity()
	if err != nil || id != v.Challenge.InstanceID {
		return ke.NotFound("market.NOT_FOUND", "验证不存在")
	}
	u, e := url.Parse(v.Origin)
	if e != nil || v.ExpiresAt <= time.Now().Unix() || u.Host != c.Request().Host {
		return ke.NotFound("market.NOT_FOUND", "验证不存在")
	}
	return c.JSON(200, v.Challenge)
}
