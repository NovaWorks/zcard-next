package memberlevel

import (
	"context"
	"fmt"
	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/memberlevel"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/rolepermission"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
	"strings"
)

type LevelSettings struct{ AcquireMode, DisplayMode string }

func (v LevelSettings) validate() error {
	if v.AcquireMode != "" && v.AcquireMode != "auto" && v.AcquireMode != "manual" {
		return fmt.Errorf("获得方式无效")
	}
	if v.DisplayMode != "" && v.DisplayMode != "public" && v.DisplayMode != "contact" && v.DisplayMode != "hidden" {
		return fmt.Errorf("展示方式无效")
	}
	return nil
}
func (r *MemberLevelRepoImpl) assigned(ctx context.Context, uid uint64) (*ent.MemberLevel, error) {
	c := data.Client(ctx, r.data)
	u, err := c.User.Get(ctx, uid)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if u.ManualLevelID == 0 {
		return nil, nil
	}
	lv, err := c.MemberLevel.Get(ctx, u.ManualLevelID)
	if err != nil {
		return nil, err
	}
	if !lv.Enabled {
		return nil, fmt.Errorf("指定等级已停用，请联系管理员")
	}
	return lv, nil
}
func (r *MemberLevelRepoImpl) ensureUnassigned(ctx context.Context, id uint64) error {
	exists, err := data.Client(ctx, r.data).User.Query().Where(user.Or(user.ManualLevelID(id), user.ReferralLevelID(id), user.InviteLevelID(id))).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("该等级仍被客户或推荐赠送配置引用，请先调整关联用户")
	}
	return nil
}
func (s *AdminMemberLevelService) AssignUserLevel(ctx context.Context, req *adminv1.AssignUserLevelRequest) (*emptypb.Empty, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请登录")
	}
	if req.UserId == 0 || len(strings.TrimSpace(req.Reason)) == 0 || len(req.Reason) > 500 || (req.ClearReferral && req.LevelId != 0) {
		return nil, errors.BadRequest("memberlevel.INVALID_INPUT", "请选择用户并填写调整原因")
	}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if req.LevelId > 0 {
			if e := s.repo.lockLevel(ctx, req.LevelId); e != nil {
				return e
			}
			_, e := c.MemberLevel.Query().Where(memberlevel.ID(req.LevelId), memberlevel.Enabled(true)).Only(ctx)
			if e != nil {
				return fmt.Errorf("等级不存在或已停用")
			}
		}
		u, e := s.repo.lockUser(ctx, req.UserId)
		if e != nil {
			return e
		}
		update := c.User.Update().Where(user.ID(u.ID), user.ManualLevelID(u.ManualLevelID)).SetManualLevelID(req.LevelId)
		if req.ClearReferral {
			update.SetReferralLevelID(0)
		}
		n, e := update.Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 && (u.ManualLevelID != req.LevelId || (req.ClearReferral && u.ReferralLevelID != 0)) {
			return fmt.Errorf("用户等级已变化，请刷新")
		}
		return c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(claims.Subject).SetPermissionPoint("memberlevel:assign").SetAction("PUT").SetRoute("/api/v1/admin/users/member-level").SetBefore(map[string]any{"user_id": u.ID, "level_id": u.ManualLevelID, "referral_level_id": u.ReferralLevelID}).SetAfter(map[string]any{"user_id": u.ID, "level_id": req.LevelId, "reason": req.Reason, "clear_referral": req.ClearReferral}).Exec(ctx)
	})
	if err != nil {
		return nil, errors.BadRequest("memberlevel.ASSIGN_FAILED", err.Error())
	}
	return &emptypb.Empty{}, nil
}
func (s *AdminMemberLevelService) canViewDiscount(ctx context.Context) bool {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return false
	}
	ok, err := data.Client(ctx, s.repo.data).RolePermission.Query().Where(rolepermission.RoleID(claims.RoleID), rolepermission.PermissionCodeIn("*", "memberlevel:view_discount")).Exist(ctx)
	return err == nil && ok
}

func (r *MemberLevelRepoImpl) lockLevel(ctx context.Context, id uint64) error {
	c := data.Client(ctx, r.data)
	if r.data.Dialect == db.SQLite {
		if _, err := c.MemberLevel.Update().Where(memberlevel.ID(id)).AddSort(0).Save(ctx); err != nil {
			return err
		}
	}
	q := c.MemberLevel.Query().Where(memberlevel.ID(id))
	if r.data.Dialect != db.SQLite {
		q = q.ForUpdate()
	}
	_, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("等级不存在")
	}
	return err
}

// Lock after locking the target level, shared with registration's lock order.
func (r *MemberLevelRepoImpl) lockUser(ctx context.Context, id uint64) (*ent.User, error) {
	c := data.Client(ctx, r.data)
	if r.data.Dialect == db.SQLite {
		if _, err := c.User.UpdateOneID(id).AddInviteLevelID(0).Save(ctx); err != nil {
			return nil, err
		}
	}
	q := c.User.Query().Where(user.ID(id))
	if r.data.Dialect != db.SQLite {
		q = q.ForUpdate()
	}
	return q.Only(ctx)
}

func (s *AdminMemberLevelService) ConfigureInviteLevel(ctx context.Context, req *adminv1.ConfigureInviteLevelRequest) (*emptypb.Empty, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请登录")
	}
	if req.UserId == 0 || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 500 {
		return nil, errors.BadRequest("memberlevel.INVALID_INPUT", "请选择用户并填写调整原因")
	}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if req.LevelId > 0 {
			if err := s.repo.lockLevel(ctx, req.LevelId); err != nil {
				return err
			}
			if _, err := c.MemberLevel.Query().Where(memberlevel.ID(req.LevelId), memberlevel.Enabled(true)).Only(ctx); err != nil {
				return fmt.Errorf("等级不存在或已停用")
			}
		}
		u, err := s.repo.lockUser(ctx, req.UserId)
		if err != nil {
			return err
		}
		if req.LevelId > 0 && u.Status != user.StatusActive {
			return fmt.Errorf("请先恢复该用户的正常状态")
		}
		if _, err = c.User.UpdateOneID(u.ID).SetInviteLevelID(req.LevelId).Save(ctx); err != nil {
			return err
		}
		return c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(claims.Subject).
			SetPermissionPoint("memberlevel:invite").SetAction("PUT").SetRoute("/api/v1/admin/users/invite-level").
			SetBefore(map[string]any{"user_id": u.ID, "invite_level_id": u.InviteLevelID}).
			SetAfter(map[string]any{"user_id": u.ID, "invite_level_id": req.LevelId, "reason": req.Reason}).Exec(ctx)
	})
	if err != nil {
		return nil, errors.BadRequest("memberlevel.INVITE_CONFIG_FAILED", err.Error())
	}
	return &emptypb.Empty{}, nil
}
