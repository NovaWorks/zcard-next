package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/installedplugin"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/pluginoperation"
	"github.com/NovaWorks/zcard-next/server/internal/mods/plugin/port"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
)

func operationOf(row *ent.PluginOperation) Operation {
	return Operation{Command: Command{OperationID: row.OperationID, PluginID: row.PluginID, Action: row.Action, TargetDigest: row.TargetDigest, ExpectedGeneration: uint64(row.ExpectedGeneration), ApprovedScopes: row.ApprovedScopes, Actor: port.Actor{AdminID: row.ActorID, SubsiteID: row.SubsiteID, ScopeVerified: true, InstanceAdmin: true, LocalOperator: row.ActorID == 0}}, TargetGeneration: uint64(row.TargetGeneration), Phase: port.Phase(row.Phase), FailureCode: row.FailureCode}
}
func (r *Repo) operation(ctx context.Context, id string) (Operation, error) {
	row, err := data.Client(ctx, r.data).PluginOperation.Query().Where(pluginoperation.OperationID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return Operation{}, contractError(pc.Unavailable, "operation record missing")
	}
	if err != nil {
		return Operation{}, err
	}
	return operationOf(row), nil
}
func (r *Repo) begin(ctx context.Context, c Command, hash string) (Operation, bool, error) {
	db := data.Client(ctx, r.data)
	row, err := db.PluginOperation.Query().Where(pluginoperation.OperationID(c.OperationID)).Only(ctx)
	if err == nil {
		if row.RequestSha256 != hash {
			return Operation{}, true, contractError(pc.Conflict, "operation ID reused with different parameters")
		}
		return operationOf(row), true, nil
	}
	if !ent.IsNotFound(err) {
		return Operation{}, false, err
	}
	row, err = db.PluginOperation.Create().SetOperationID(c.OperationID).SetPluginID(c.PluginID).SetAction(c.Action).SetRequestSha256(hash).SetActorID(c.Actor.AdminID).SetSubsiteID(c.Actor.SubsiteID).SetExpectedGeneration(int64(c.ExpectedGeneration)).SetTargetDigest(c.TargetDigest).SetApprovedScopes(c.ApprovedScopes).SetPhase(string(port.PhasePreparing)).Save(ctx)
	if err != nil {
		return Operation{}, false, err
	}
	return operationOf(row), false, nil
}
func (r *Repo) fail(ctx context.Context, op Operation) error {
	_, err := data.Client(ctx, r.data).PluginOperation.Update().Where(pluginoperation.OperationID(op.Command.OperationID), pluginoperation.Phase(string(port.PhasePreparing))).SetPhase(string(port.PhaseFailed)).SetFailureCode(op.FailureCode).Save(ctx)
	return err
}
func (r *Repo) status(ctx context.Context, id string) (Status, error) {
	row, err := data.Client(ctx, r.data).InstalledPlugin.Query().Where(installedplugin.PluginID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return Status{State: port.State{PluginID: id}}, nil
	}
	if err != nil {
		return Status{}, err
	}
	s := Status{State: port.State{PluginID: row.PluginID, DesiredEnabled: row.DesiredEnabled, DesiredGeneration: uint64(row.DesiredGeneration), ObservedGeneration: uint64(row.ObservedGeneration), OperationID: row.CurrentOperationID}, DesiredDigest: row.DesiredDigest, ObservedDigest: row.ObservedDigest, ApprovedScopes: row.ApprovedScopes, Uninstalled: row.Uninstalled}
	for _, reason := range row.BlockReasons {
		s.State.BlockReasons = append(s.State.BlockReasons, port.BlockReason(reason))
	}
	if row.CurrentOperationID != "" {
		op, e := r.operation(ctx, row.CurrentOperationID)
		if e != nil {
			var ce *pc.Error
			if errors.As(e, &ce) {
				s.State.Phase = port.PhaseFailed
				s.State.ReconciliationPending = true
				return s, nil
			}
			return s, e
		}
		s.State.Phase = op.Phase
		s.State.ReconciliationPending = op.Phase != port.PhaseReconciled
	}
	return s, nil
}
func (r *Repo) installedIDs(ctx context.Context) ([]string, error) {
	return data.Client(ctx, r.data).InstalledPlugin.Query().Order(ent.Asc(installedplugin.FieldPluginID)).Select(installedplugin.FieldPluginID).Strings(ctx)
}
func (r *Repo) intent(ctx context.Context, op Operation, enabled, uninstalled bool) error {
	return data.RuleWriteTx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		row, err := c.InstalledPlugin.Query().Where(installedplugin.PluginID(op.Command.PluginID)).Only(ctx)
		if ent.IsNotFound(err) {
			if op.Command.ExpectedGeneration != 0 {
				return contractError(pc.Conflict, "plugin disappeared")
			}
			_, err = c.InstalledPlugin.Create().SetPluginID(op.Command.PluginID).SetDesiredEnabled(enabled).SetDesiredGeneration(int64(op.TargetGeneration)).SetDesiredDigest(op.Command.TargetDigest).SetApprovedScopes(op.Command.ApprovedScopes).SetCurrentOperationID(op.Command.OperationID).SetUninstalled(uninstalled).Save(ctx)
		} else if err == nil {
			// A recovery disable/uninstall may supersede an unconfirmed intent.
			// Keep its own operation result terminal so retrying the old UUID
			// cannot reconcile or return a different, newer command.
			if row.CurrentOperationID != op.Command.OperationID {
				if _, e := c.PluginOperation.Update().Where(pluginoperation.OperationID(row.CurrentOperationID), pluginoperation.PhaseIn(string(port.PhaseReady), string(port.PhasePublished))).SetPhase(string(port.PhaseFailed)).SetFailureCode(string(pc.Conflict)).Save(ctx); e != nil {
					return e
				}
			}
			q := c.InstalledPlugin.Update().Where(installedplugin.ID(row.ID), installedplugin.DesiredGeneration(int64(op.Command.ExpectedGeneration))).SetDesiredEnabled(enabled).SetDesiredGeneration(int64(op.TargetGeneration)).SetDesiredDigest(op.Command.TargetDigest).SetCurrentOperationID(op.Command.OperationID).SetUninstalled(uninstalled)
			if op.Command.Action != "disable" && op.Command.Action != "uninstall" {
				q.SetApprovedScopes(op.Command.ApprovedScopes)
			}
			var n int
			n, err = q.Save(ctx)
			if err == nil && n != 1 {
				return contractError(pc.Conflict, "generation changed")
			}
		}
		if err != nil {
			return err
		}
		n, err := c.PluginOperation.Update().Where(pluginoperation.OperationID(op.Command.OperationID), pluginoperation.Phase(string(port.PhasePreparing))).SetTargetGeneration(int64(op.TargetGeneration)).SetTargetDigest(op.Command.TargetDigest).SetPhase(string(port.PhaseReady)).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("operation no longer preparing")
		}
		return nil
	})
}
func (r *Repo) confirm(ctx context.Context, op Operation) error {
	return data.RuleWriteTx(ctx, r.data, func(ctx context.Context) error {
		c := data.Client(ctx, r.data)
		row, err := c.InstalledPlugin.Query().Where(installedplugin.PluginID(op.Command.PluginID), installedplugin.DesiredGeneration(int64(op.TargetGeneration)), installedplugin.CurrentOperationID(op.Command.OperationID)).Only(ctx)
		if err != nil {
			return err
		}
		reasons := []string{}
		for _, reason := range row.BlockReasons {
			if reason != string(port.BlockMissing) && reason != string(port.BlockRuntime) && reason != string(port.BlockIncompatible) {
				reasons = append(reasons, reason)
			}
		}
		if err = c.InstalledPlugin.UpdateOneID(row.ID).SetObservedGeneration(int64(op.TargetGeneration)).SetObservedDigest(row.DesiredDigest).SetBlockReasons(reasons).Exec(ctx); err != nil {
			return err
		}
		if _, err = c.PluginOperation.Update().Where(pluginoperation.OperationID(op.Command.OperationID)).SetPhase(string(port.PhaseReconciled)).SetFailureCode("").Save(ctx); err != nil {
			return err
		}
		r.auditRule(ctx, op.Command.Actor, "plugin."+op.Command.Action, port.RuleKey{PluginID: op.Command.PluginID}, op.TargetGeneration)
		return nil
	})
}
func (r *Repo) block(ctx context.Context, id, reason string) error {
	c := data.Client(ctx, r.data)
	row, err := c.InstalledPlugin.Query().Where(installedplugin.PluginID(id)).Only(ctx)
	if err != nil {
		return err
	}
	for _, old := range row.BlockReasons {
		if old == reason {
			return nil
		}
	}
	return c.InstalledPlugin.UpdateOneID(row.ID).SetBlockReasons(append(row.BlockReasons, reason)).Exec(ctx)
}
