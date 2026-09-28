package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/marketclient"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/encoding/protojson"
)

const marketPrefix = "/api/v1/admin/plugins/market"

type marketSelection struct {
	Origin  string          `json:"origin"`
	Version string          `json:"version"`
	Command json.RawMessage `json:"command"`
}

func (s *AdminPluginService) marketClient(origin string) (*marketclient.Client, error) {
	packages, ok := s.manager.packages.(*FilePackages)
	if !ok {
		return nil, fmt.Errorf("market package store unavailable")
	}
	return marketclient.New(origin, os.Getenv("ZCARD_MARKET_TEST_ORIGIN"), packages.keys)
}
func (s *AdminPluginService) catalog(ctx context.Context, origin string) (mc.Envelope, error) {
	client, e := s.marketClient(origin)
	if e != nil {
		return mc.Envelope{}, e
	}
	defer client.Close()
	env, e := client.Catalog(ctx)
	if e == nil {
		e = s.manager.repo.acceptCatalog(ctx, env.Catalog)
	}
	return env, e
}
func (s *AdminPluginService) RegisterMarket(srv *khttp.Server) {
	for _, spec := range []struct{ method, path, op string }{{"GET", "config", "GetMarketConfig"}, {"PUT", "config", "SetMarketConfig"}, {"GET", "catalog", "GetMarketCatalog"}, {"POST", "inspect", "InspectMarketPlugin"}, {"POST", "install", "InstallMarketPlugin"}} {
		route := spec
		srv.Route("/").Handle(route.method, marketPrefix+"/"+route.path, func(c khttp.Context) error {
			khttp.SetOperation(c, "/zcard.api.admin.v1.AdminPluginService/"+route.op)
			out, e := c.Middleware(func(ctx context.Context, _ any) (any, error) {
				if _, e := s.authorize(ctx, "plugin:manage", true, false); e != nil {
					return nil, e
				}
				// One overall deadline covers catalog + artifact + final freshness check.
				ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
				defer cancel()
				origin, e := s.manager.repo.marketOrigin(ctx)
				if e != nil {
					return nil, e
				}
				switch route.op {
				case "GetMarketConfig":
					return map[string]string{"origin": origin}, nil
				case "SetMarketConfig":
					var in struct {
						Origin string `json:"origin"`
					}
					if e = json.NewDecoder(c.Request().Body).Decode(&in); e != nil {
						return nil, errors.BadRequest("plugin.INVALID_MARKET", "市场地址格式错误")
					}
					if in.Origin != "" {
						normalized, e := mc.Origin(in.Origin)
						if e != nil {
							return nil, errors.BadRequest("plugin.INVALID_MARKET", "请输入不含路径或凭据的 HTTPS 市场地址")
						}
						in.Origin = normalized
						client, e := s.marketClient(in.Origin)
						if e != nil {
							return nil, errors.BadRequest("plugin.INVALID_MARKET", "市场需要 HTTPS，本地验收须显式配置精确回环地址")
						}
						client.Close()
					}
					e = s.manager.repo.configureMarket(ctx, in.Origin)
					return map[string]string{"origin": in.Origin}, e
				case "GetMarketCatalog":
					env, e := s.catalog(ctx, origin)
					if e != nil {
						return nil, errors.New(503, "plugin.MARKET_UNAVAILABLE", "市场目录不可用或校验失败；已安装的免费插件继续运行")
					}
					incompatible := map[string]string{}
					host := s.manager.packages.(*FilePackages).host
					for _, entry := range env.Catalog.Entries {
						if _, e := pc.CheckCompatibility(entry.Manifest, host); e != nil {
							incompatible[entry.Descriptor.ArchiveSHA256] = e.Error()
						}
					}
					return map[string]any{"catalog": env.Catalog, "incompatible": incompatible}, nil
				default:
					return s.marketInstall(ctx, c.Request().Body, origin, route.path == "inspect")
				}
			})(c, nil)
			if e != nil {
				return e
			}
			return c.Result(200, out)
		})
	}
}
func (s *AdminPluginService) marketInstall(ctx context.Context, body io.Reader, origin string, inspect bool) (any, error) {
	var in marketSelection
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&in); e != nil || in.Origin != origin {
		return nil, errors.BadRequest("plugin.INVALID_MARKET_SELECTION", "市场已切换或请求不合法，请刷新目录")
	}
	cmd := &adminv1.OperatePluginRequest{}
	if e := protojson.Unmarshal(in.Command, cmd); e != nil {
		return nil, errors.BadRequest("plugin.INVALID_COMMAND", "操作参数不合法")
	}
	command, e := s.command(ctx, cmd)
	if e != nil {
		return nil, e
	}
	if e = validateCommand(command); e != nil {
		return nil, apiError(e)
	}
	if command.Action != "import" && command.Action != "upgrade" {
		return nil, errors.BadRequest("plugin.INVALID_COMMAND", "市场仅支持安装和升级")
	}
	if !inspect {
		if _, found, e := s.manager.repo.completedMarketOperation(ctx, command); e != nil {
			return nil, apiError(e)
		} else if found {
			v, e := s.manager.Operate(ctx, command)
			if e != nil {
				return nil, apiError(e)
			}
			return s.operationPB(ctx, v), nil
		}
	}
	env, e := s.catalog(ctx, origin)
	if e != nil {
		return nil, errors.New(503, "plugin.MARKET_UNAVAILABLE", "目录不可用或校验失败")
	}
	var selected *mc.Entry
	for _, entry := range env.Catalog.Entries {
		if entry.Descriptor.PluginID == command.PluginID && entry.Descriptor.Version == in.Version && entry.Descriptor.ArchiveSHA256 == command.TargetDigest {
			selected = &entry
			break
		}
	}
	if selected == nil {
		return nil, errors.New(409, "plugin.MARKET_VERSION_WITHDRAWN", "版本已撤回或目录已更新，请刷新")
	}
	if command.Action == "upgrade" {
		state, e := s.manager.Status(ctx, command.PluginID)
		if e != nil {
			return nil, apiError(e)
		}
		if state.DesiredDigest != "" && state.DesiredDigest != command.TargetDigest {
			previous, r, e := s.manager.packages.Open(ctx, state.DesiredDigest)
			if e != nil {
				return nil, apiError(e)
			}
			r.Close()
			if semver.Compare("v"+selected.Descriptor.Version, "v"+previous.Manifest.Version) <= 0 {
				return nil, errors.New(409, "plugin.MARKET_DOWNGRADE", "市场升级必须选择更高版本；主动回滚请使用已验证的本地回滚流程")
			}
		}
	}
	client, e := s.marketClient(origin)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	archive, e := client.Download(ctx, *selected)
	if e != nil {
		return nil, errors.New(503, "plugin.MARKET_DOWNLOAD_FAILED", "工件下载中断或校验失败，请重试")
	}
	descriptor, _ := json.Marshal(selected.Descriptor)
	req := &adminv1.ImportPluginRequest{Command: cmd, DescriptorJson: descriptor, Signature: selected.Signature, Archive: archive}
	preview, e := s.InspectPlugin(ctx, req)
	if e != nil {
		return nil, e
	}
	// The ZIP manifest is independently signed; reject any catalog metadata mismatch.
	artifact, reader, e := s.manager.packages.Open(ctx, command.TargetDigest)
	if e != nil {
		return nil, apiError(e)
	}
	reader.Close()
	actual, _ := json.Marshal(artifact.Manifest)
	advertised, _ := json.Marshal(selected.Manifest)
	if string(actual) != string(advertised) {
		return nil, errors.New(409, "plugin.MARKET_MANIFEST_MISMATCH", "目录与工件声明不一致")
	}
	if inspect {
		return preview, nil
	}
	// Refresh after download so withdrawn versions cannot be installed using an old UI.
	fresh, e := s.catalog(ctx, origin)
	if e != nil {
		return nil, errors.New(503, "plugin.MARKET_UNAVAILABLE", "安装前目录复核失败")
	}
	found := false
	for _, entry := range fresh.Catalog.Entries {
		if entry.Descriptor.ArchiveSHA256 == command.TargetDigest && entry.Descriptor.PluginID == command.PluginID && entry.Descriptor.Version == in.Version {
			found = true
		}
	}
	if !found {
		return nil, errors.New(409, "plugin.MARKET_VERSION_WITHDRAWN", "版本已撤回，请刷新目录")
	}
	// Serialize the last checkpoint/config comparison with configuration writes.
	s.manager.repo.marketMu.Lock()
	defer s.manager.repo.marketMu.Unlock()
	if e = s.manager.repo.acceptCatalogLocked(ctx, fresh.Catalog); e != nil {
		return nil, errors.New(409, "plugin.MARKET_CHANGED", "目录已变化，请重新确认")
	}
	return s.ImportPlugin(ctx, req)
}
