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
	exists, err := data.Client(ctx, r.data).User.Query().Where(user.ManualLevelID(id)).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("该等级仍分配给用户，请先调整关联用户")
	}
	return nil
}
func (s *AdminMemberLevelService) AssignUserLevel(ctx context.Context, req *adminv1.AssignUserLevelRequest) (*emptypb.Empty, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "请登录")
	}
	if req.UserId == 0 || len(strings.TrimSpace(req.Reason)) == 0 || len(req.Reason) > 500 {
		return nil, errors.BadRequest("memberlevel.INVALID_INPUT", "请选择用户并填写调整原因")
	}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if req.LevelId > 0 {
			if e := s.repo.lockLevel(ctx, req.LevelId); e != nil {
				return e
			}
			lv, e := c.MemberLevel.Query().Where(memberlevel.ID(req.LevelId), memberlevel.Enabled(true)).Only(ctx)
			if e != nil {
				return fmt.Errorf("等级不存在或已停用")
			}
			if lv.AcquireMode != "manual" {
				return fmt.Errorf("只能指定后台授予等级")
			}
		}
		u, e := c.User.Get(ctx, req.UserId)
		if e != nil {
			return e
		}
		n, e := c.User.Update().Where(user.ID(u.ID), user.ManualLevelID(u.ManualLevelID)).SetManualLevelID(req.LevelId).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("用户等级已变化，请刷新")
		}
		return c.AuditLog.Create().SetOperatorType("admin").SetOperatorID(claims.Subject).SetPermissionPoint("memberlevel:assign").SetAction("PUT").SetRoute("/api/v1/admin/users/member-level").SetBefore(map[string]any{"user_id": u.ID, "level_id": u.ManualLevelID}).SetAfter(map[string]any{"user_id": u.ID, "level_id": req.LevelId, "reason": req.Reason}).Exec(ctx)
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
	n, err := data.Client(ctx, r.data).MemberLevel.Update().Where(memberlevel.ID(id)).AddSort(0).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("等级不存在")
	}
	return nil
}
