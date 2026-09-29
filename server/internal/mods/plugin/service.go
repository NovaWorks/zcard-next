package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strconv"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	authzport "github.com/NovaWorks/zcard-next/server/internal/mods/authz/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/authn"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/transport"
	"google.golang.org/protobuf/types/known/emptypb"
)

type AdminPluginService struct {
	adminv1.UnimplementedAdminPluginServiceServer
	manager *Manager
	az      authzport.Authorizer
}

func NewAdminPluginService(m *Manager, az authzport.Authorizer) *AdminPluginService {
	return &AdminPluginService{manager: m, az: az}
}
func (s *AdminPluginService) authorize(ctx context.Context, permission string, instance, product bool) (port.Actor, error) {
	claims := authn.ClaimsFromContext(ctx)
	if claims == nil || claims.Realm != authn.RealmAdmin || claims.Subject == 0 {
		return port.Actor{}, errors.Unauthorized("plugin.UNAUTHORIZED", "请登录管理员账号")
	}
	tc, e := tenancy.Require(ctx)
	if e != nil {
		return port.Actor{}, errors.Forbidden("plugin.SCOPE_REQUIRED", "缺少明确站点范围")
	}
	if !s.az.Allowed(ctx, claims.RoleID, permission) || (product && !s.az.Allowed(ctx, claims.RoleID, "catalog:write")) {
		return port.Actor{}, errors.Forbidden("plugin.FORBIDDEN", "权限不足")
	}
	super := s.az.RoleCode(ctx, claims.RoleID) == authzport.RoleSuperAdmin
	if instance && (!super || tc.SubsiteID != 0) {
		return port.Actor{}, errors.Forbidden("plugin.INSTANCE_ADMIN_REQUIRED", "此操作需要主站实例管理员")
	}
	return port.Actor{AdminID: claims.Subject, SubsiteID: tc.SubsiteID, ScopeVerified: true, InstanceAdmin: super && tc.SubsiteID == 0}, nil
}
func integer(v string, zero bool) (uint64, error) {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != v || (!zero && n == 0) {
		return 0, errors.BadRequest("plugin.INVALID_ID", "ID或版本号格式错误")
	}
	return n, nil
}
func (s *AdminPluginService) key(ctx context.Context, product, id string, a port.Actor) (port.RuleKey, error) {
	n, err := integer(product, false)
	if err != nil {
		return port.RuleKey{}, err
	}
	k := port.RuleKey{SubsiteID: a.SubsiteID, ProductID: n, PluginID: id}
	if id == "" {
		k.PluginID = "member-purchase-gate"
	}
	if err = s.manager.repo.scope(ctx, k); err != nil {
		return k, apiError(err)
	}
	if err = s.manager.repo.product(ctx, k); err != nil {
		return k, apiError(err)
	}
	return k, nil
}
func apiError(err error) error {
	if err == nil {
		return nil
	}
	var ce *pc.Error
	if !stderrors.As(err, &ce) {
		return err
	}
	code := 400
	msg := "插件配置或工件不合法"
	switch ce.Code {
	case pc.Conflict, pc.Incompatible, pc.PaidUnsupported:
		code = 409
		msg = "插件版本或操作状态冲突，请刷新后重试"
	case pc.Forbidden:
		code = 403
		msg = "权限不足或资源不在当前范围"
	case pc.Unavailable:
		code = 503
		msg = "插件能力暂不可用"
	}
	return errors.New(code, string(ce.Code), msg)
}
func statusPB(v Status, available bool) *adminv1.PluginStatusReply {
	out := &adminv1.PluginStatusReply{PluginId: v.State.PluginID, DesiredEnabled: v.State.DesiredEnabled, DesiredGeneration: strconv.FormatUint(v.State.DesiredGeneration, 10), ObservedGeneration: strconv.FormatUint(v.State.ObservedGeneration, 10), DesiredDigest: v.DesiredDigest, ObservedDigest: v.ObservedDigest, OperationId: v.State.OperationID, Phase: string(v.State.Phase), ReconciliationPending: v.State.ReconciliationPending, Uninstalled: v.Uninstalled, ApprovedScopes: v.ApprovedScopes, RuntimeAvailable: available}
	for _, reason := range v.State.BlockReasons {
		out.BlockReasons = append(out.BlockReasons, string(reason))
	}
	return out
}
func (s *AdminPluginService) status(ctx context.Context, id string) (*adminv1.PluginStatusReply, error) {
	v, e := s.manager.Status(ctx, id)
	if e != nil {
		return nil, apiError(e)
	}
	if v.State.DesiredGeneration == 0 {
		return nil, errors.NotFound("plugin.NOT_FOUND", "插件未安装")
	}
	return statusPB(v, s.manager.loader != nil), nil
}
func (s *AdminPluginService) ListPlugins(ctx context.Context, _ *emptypb.Empty) (*adminv1.ListPluginsReply, error) {
	a, e := s.authorize(ctx, "plugin:read", false, false)
	if e != nil {
		return nil, e
	}
	ids, e := s.manager.repo.installedIDs(ctx)
	if e != nil {
		return nil, e
	}
	out := &adminv1.ListPluginsReply{InstanceManagementAllowed: a.InstanceAdmin && s.az.Allowed(ctx, authn.ClaimsFromContext(ctx).RoleID, "plugin:manage")}
	for _, id := range ids {
		v, e := s.status(ctx, id)
		if e != nil {
			return nil, e
		}
		out.Plugins = append(out.Plugins, v)
	}
	return out, nil
}
func (s *AdminPluginService) GetPlugin(ctx context.Context, in *adminv1.PluginRequest) (*adminv1.PluginStatusReply, error) {
	if _, e := s.authorize(ctx, "plugin:read", false, false); e != nil {
		return nil, e
	}
	return s.status(ctx, in.GetPluginId())
}
func (s *AdminPluginService) command(ctx context.Context, in *adminv1.OperatePluginRequest) (Command, error) {
	a, e := s.authorize(ctx, "plugin:manage", true, false)
	if e != nil {
		return Command{}, e
	}
	if in == nil {
		return Command{}, errors.BadRequest("plugin.COMMAND_REQUIRED", "缺少操作参数")
	}
	gen, e := integer(in.ExpectedGeneration, true)
	if e != nil {
		return Command{}, e
	}
	return Command{OperationID: in.OperationId, PluginID: in.PluginId, Action: in.Action, TargetDigest: in.TargetDigest, ExpectedGeneration: gen, ApprovedScopes: in.ApprovedScopes, Actor: a, ConfirmPaid: in.ConfirmPaid}, nil
}
func (s *AdminPluginService) operationPB(ctx context.Context, v Operation) *adminv1.PluginOperationReply {
	state, _ := s.manager.Status(ctx, v.Command.PluginID)
	return &adminv1.PluginOperationReply{OperationId: v.Command.OperationID, PluginId: v.Command.PluginID, Action: v.Command.Action, TargetGeneration: strconv.FormatUint(v.TargetGeneration, 10), Phase: string(v.Phase), FailureCode: v.FailureCode, Status: statusPB(state, s.manager.loader != nil)}
}
func (s *AdminPluginService) OperatePlugin(ctx context.Context, in *adminv1.OperatePluginRequest) (*adminv1.PluginOperationReply, error) {
	c, e := s.command(ctx, in)
	if e != nil {
		return nil, e
	}
	if c.Action == "import" {
		return nil, errors.BadRequest("plugin.USE_IMPORT", "请使用签名包导入接口")
	}
	v, e := s.manager.Operate(ctx, c)
	if e != nil {
		return nil, apiError(e)
	}
	return s.operationPB(ctx, v), nil
}

// Inspect verifies the signed package without changing desired state or approving scopes.
// The bounded immutable package cache is shared with import; no plugin code runs.
func (s *AdminPluginService) InspectPlugin(ctx context.Context, in *adminv1.ImportPluginRequest) (*adminv1.PluginPackageReply, error) {
	c, e := s.command(ctx, in.GetCommand())
	if e != nil {
		return nil, e
	}
	if e = validateCommand(c); e != nil {
		return nil, apiError(e)
	}
	if s.manager.coordinator.readOnly {
		return nil, apiError(contractError(pc.Unavailable, "single-process mode required"))
	}
	if e = s.manager.prunePackages(ctx, c.TargetDigest); e != nil {
		return nil, apiError(e)
	}
	a, e := s.manager.packages.Stage(ctx, in.DescriptorJson, in.Signature, bytes.NewReader(in.Archive))
	if e != nil {
		return nil, apiError(e)
	}
	if a.Manifest.ID != c.PluginID || a.Descriptor.ArchiveSHA256 != c.TargetDigest {
		return nil, apiError(contractError(pc.InvalidContract, "package identity mismatch"))
	}
	return &adminv1.PluginPackageReply{PluginId: a.Manifest.ID, Version: a.Manifest.Version, Digest: a.Descriptor.ArchiveSHA256, Scopes: a.Manifest.Scopes, EntitlementMode: a.Manifest.Entitlement.Mode}, nil
}
func (s *AdminPluginService) ImportPlugin(ctx context.Context, in *adminv1.ImportPluginRequest) (*adminv1.PluginOperationReply, error) {
	c, e := s.command(ctx, in.GetCommand())
	if e != nil {
		return nil, e
	}
	v, e := s.manager.Import(ctx, c, in.DescriptorJson, in.Signature, bytes.NewReader(in.Archive))
	if e != nil {
		return nil, apiError(e)
	}
	return s.operationPB(ctx, v), nil
}
func (s *AdminPluginService) GetOperation(ctx context.Context, in *adminv1.PluginOperationRequest) (*adminv1.PluginOperationReply, error) {
	a, e := s.authorize(ctx, "plugin:read", false, false)
	if e != nil {
		return nil, e
	}
	v, e := s.manager.repo.operation(ctx, in.OperationId)
	if e != nil {
		return nil, errors.NotFound("plugin.OPERATION_NOT_FOUND", "操作不存在")
	}
	if !a.InstanceAdmin && (a.AdminID != v.Command.Actor.AdminID || a.SubsiteID != v.Command.Actor.SubsiteID) {
		return nil, errors.Forbidden("plugin.FORBIDDEN", "无权查看此操作")
	}
	return s.operationPB(ctx, v), nil
}
func rulePB(k port.RuleKey, v port.Rule, gen uint64, e error) *adminv1.ProductPluginRule {
	out := &adminv1.ProductPluginRule{PluginId: k.PluginID, ProductId: strconv.FormatUint(k.ProductID, 10), Required: v.Requirement.Required, RequirementRevision: strconv.FormatUint(v.Requirement.Revision, 10), Generation: strconv.FormatUint(gen, 10), ConfigValid: e == nil}
	if e != nil {
		out.ErrorCode = string(pc.Unavailable)
		return out
	}
	cfg := v.Config
	out.Config = &adminv1.PluginConfig{SchemaVersion: uint32(cfg.SchemaVersion), Revision: string(cfg.Revision), Enabled: cfg.Enabled, AllowedLevelIds: []string{}}
	for _, id := range cfg.AllowedLevelIDs {
		out.Config.AllowedLevelIds = append(out.Config.AllowedLevelIds, string(id))
	}
	return out
}
func (s *AdminPluginService) etag(ctx context.Context, v any) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		claims := authn.ClaimsFromContext(ctx)
		if claims == nil {
			return
		}
		permissions, err := s.az.PermissionsOf(ctx, claims.RoleID)
		if err != nil {
			tr.ReplyHeader().Set("Cache-Control", "no-store")
			return
		}
		permissions = slices.Clone(permissions)
		slices.Sort(permissions)
		b, _ := json.Marshal(struct {
			Value       any
			Role        uint64
			Permissions []string
		}{v, claims.RoleID, permissions})
		tr.ReplyHeader().Set("ETag", `"`+digest(b)+`"`)
		tr.ReplyHeader().Set("Cache-Control", "private, no-cache")
	}
}
func (s *AdminPluginService) GetProductRule(ctx context.Context, in *adminv1.ProductPluginRequest) (*adminv1.ProductPluginRule, error) {
	a, e := s.authorize(ctx, "plugin:read", false, true)
	if e != nil {
		return nil, e
	}
	k, e := s.key(ctx, in.ProductId, in.PluginId, a)
	if e != nil {
		return nil, e
	}
	v, e := s.manager.repo.Get(ctx, k)
	if e != nil {
		var ce *pc.Error
		if !stderrors.As(e, &ce) || ce.Code != pc.Unavailable {
			return nil, apiError(e)
		}
	}
	state, se := s.manager.Status(ctx, k.PluginID)
	if se != nil {
		return nil, apiError(se)
	}
	out := rulePB(k, v, state.State.DesiredGeneration, e)
	s.etag(ctx, struct {
		Rule  any
		Actor port.Actor
	}{out, a})
	return out, nil
}
func (s *AdminPluginService) SaveProductRule(ctx context.Context, in *adminv1.SaveProductRuleRequest) (*adminv1.ProductPluginRule, error) {
	a, e := s.authorize(ctx, "plugin:configure", false, true)
	if e != nil {
		return nil, e
	}
	k, e := s.key(ctx, in.ProductId, in.PluginId, a)
	if e != nil {
		return nil, e
	}
	if in.Config == nil {
		return nil, errors.BadRequest("plugin.CONFIG_REQUIRED", "配置必填")
	}
	gen, e := integer(in.ExpectedGeneration, true)
	if e != nil {
		return nil, e
	}
	cr, e := integer(in.ExpectedConfigRevision, true)
	if e != nil {
		return nil, e
	}
	rr, e := integer(in.ExpectedRequirementRevision, true)
	if e != nil {
		return nil, e
	}
	cfg := pc.Config{SchemaVersion: int(in.Config.SchemaVersion), Revision: pc.Decimal(in.Config.Revision), Enabled: in.Config.Enabled, AllowedLevelIDs: []pc.Decimal{}}
	for _, id := range in.Config.AllowedLevelIds {
		cfg.AllowedLevelIDs = append(cfg.AllowedLevelIDs, pc.Decimal(id))
	}
	v, e := s.manager.repo.Save(ctx, port.SaveConfig{Key: k, Actor: a, Expected: port.Expected{Generation: gen, ConfigRevision: cr, RequirementRevision: rr, SchemaVersion: int(in.SchemaVersion)}, Config: cfg})
	if e != nil {
		return nil, apiError(e)
	}
	return rulePB(k, v, gen, nil), nil
}
func (s *AdminPluginService) ReleaseProductRule(ctx context.Context, in *adminv1.ReleaseProductRuleRequest) (*adminv1.ProductPluginRule, error) {
	a, e := s.authorize(ctx, "plugin:release", false, true)
	if e != nil {
		return nil, e
	}
	k, e := s.key(ctx, in.ProductId, in.PluginId, a)
	if e != nil {
		return nil, e
	}
	rev, e := integer(in.ExpectedRequirementRevision, true)
	if e != nil {
		return nil, e
	}
	_, e = s.manager.repo.Release(ctx, port.ReleaseRule{Key: k, Actor: a, ExpectedRequirementRevision: rev})
	if e != nil {
		return nil, apiError(e)
	}
	v, e := s.manager.repo.Get(ctx, k)
	if e != nil {
		return nil, apiError(e)
	}
	state, e := s.manager.Status(ctx, k.PluginID)
	if e != nil {
		return nil, apiError(e)
	}
	return rulePB(k, v, state.State.DesiredGeneration, nil), nil
}
func (s *AdminPluginService) ListProductRules(ctx context.Context, in *adminv1.ProductPluginRequest) (*adminv1.ListProductRulesReply, error) {
	a, e := s.authorize(ctx, "plugin:read", false, true)
	if e != nil {
		return nil, e
	}
	k, e := s.key(ctx, in.ProductId, "", a)
	if e != nil {
		return nil, e
	}
	reqs, e := s.manager.repo.requirements(ctx, a.SubsiteID, k.ProductID)
	if e != nil {
		return nil, e
	}
	out := &adminv1.ListProductRulesReply{}
	for _, r := range reqs {
		v, e := s.GetProductRule(ctx, &adminv1.ProductPluginRequest{PluginId: r.Key.PluginID, ProductId: in.ProductId})
		if e != nil {
			return nil, e
		}
		out.Rules = append(out.Rules, v)
	}
	return out, nil
}
func (s *AdminPluginService) GetProductSchema(ctx context.Context, in *adminv1.ProductPluginRequest) (*adminv1.PluginSchemaReply, error) {
	a, e := s.authorize(ctx, "plugin:read", false, true)
	if e != nil {
		return nil, e
	}
	k, e := s.key(ctx, in.ProductId, in.PluginId, a)
	if e != nil {
		return nil, e
	}
	state, e := s.manager.Status(ctx, k.PluginID)
	if e != nil {
		return nil, apiError(e)
	}
	if state.Uninstalled || state.DesiredDigest == "" {
		return nil, errors.NotFound("plugin.NOT_FOUND", "插件未安装")
	}
	artifact, reader, e := s.manager.packages.Open(ctx, state.DesiredDigest)
	if e != nil {
		return nil, apiError(e)
	}
	_ = reader.Close()
	schema, _ := pc.Schema(pc.ConfigKind)
	ui, _ := json.Marshal(artifact.Manifest.UIContributions)
	out := &adminv1.PluginSchemaReply{PluginId: k.PluginID, Generation: strconv.FormatUint(state.State.DesiredGeneration, 10), ConfigSchemaJson: string(schema), UiSchemaJson: string(ui), RuntimeAvailable: s.manager.loader != nil}
	s.etag(ctx, struct {
		Schema any
		Actor  port.Actor
	}{out, a})
	return out, nil
}
func (s *AdminPluginService) ListContributions(ctx context.Context, in *adminv1.ProductPluginRequest) (*adminv1.PluginContributionsReply, error) {
	a, e := s.authorize(ctx, "plugin:read", false, true)
	if e != nil {
		return nil, e
	}
	if _, e = s.key(ctx, in.ProductId, "", a); e != nil {
		return nil, e
	}
	out := &adminv1.PluginContributionsReply{}
	defer func() {
		s.etag(ctx, struct {
			Content any
			Actor   port.Actor
			Product string
		}{out, a, in.ProductId})
	}()
	if s.manager.loader == nil {
		out.UnavailableReasons = []string{"runtime_not_available"}
		return out, nil
	}
	ids, e := s.manager.repo.installedIDs(ctx)
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		l, e := s.manager.coordinator.Acquire(ctx, id)
		if e != nil {
			continue
		}
		// Read contribution metadata from the same immutable generation as the lease.
		pinned, ok := l.(*lease)
		if ok {
			rt, ready := pinned.slot.runtime.(*artifactRuntime)
			if ready && !rt.Faulted() {
				schema, _ := pc.Schema(pc.ConfigKind)
				ui, _ := json.Marshal(rt.manifest.UIContributions)
				out.Contributions = append(out.Contributions, &adminv1.PluginSchemaReply{PluginId: id, Generation: strconv.FormatUint(l.Generation(), 10), ConfigSchemaJson: string(schema), UiSchemaJson: string(ui), RuntimeAvailable: true})
			}
		}
		l.Release()
	}
	return out, nil
}
func (s *AdminPluginService) ListLevelOptions(ctx context.Context, in *adminv1.ProductPluginRequest) (*adminv1.PluginLevelOptionsReply, error) {
	a, e := s.authorize(ctx, "plugin:configure", false, true)
	if e != nil {
		return nil, e
	}
	if _, e = s.key(ctx, in.ProductId, "", a); e != nil {
		return nil, e
	}
	rows, e := s.manager.repo.levels(ctx)
	if e != nil {
		return nil, e
	}
	if len(rows) > 1000 {
		return nil, errors.New(413, "plugin.TOO_MANY_LEVELS", "等级选项超过上限")
	}
	out := &adminv1.PluginLevelOptionsReply{}
	for _, r := range rows {
		out.Options = append(out.Options, &adminv1.PluginLevelOption{Id: strconv.FormatUint(r.ID, 10), Name: r.Name, Enabled: r.Enabled})
	}
	return out, nil
}
func (s *AdminPluginService) GetImpact(ctx context.Context, in *adminv1.PluginRequest) (*adminv1.PluginImpactReply, error) {
	a, e := s.authorize(ctx, "plugin:manage", true, false)
	if e != nil {
		return nil, e
	}
	n, ids, e := s.manager.repo.impact(ctx, in.PluginId, a.SubsiteID)
	if e != nil {
		return nil, e
	}
	out := &adminv1.PluginImpactReply{AffectedTotal: strconv.Itoa(n), Truncated: len(ids) > 1000}
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	for _, id := range ids {
		out.VisibleProductIds = append(out.VisibleProductIds, strconv.FormatUint(id, 10))
	}
	return out, nil
}
