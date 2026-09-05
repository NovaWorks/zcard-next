// 管理面 API（doc/在线更新方案.md §9；system:update 超管专属——权限点在
// authz/permissions.go 预登记段挂 Op，审计经现有 operation 中间件自动覆盖）。
package update

import (
	context "context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/updater"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminUpdateService 在线更新管理面。
type AdminUpdateService struct {
	adminv1.UnimplementedAdminUpdateServiceServer
	svc *Service
}

// NewAdminUpdateService 构造。
func NewAdminUpdateService(svc *Service) *AdminUpdateService {
	return &AdminUpdateService{svc: svc}
}

// GetUpdateStatus 状态轮询（重启窗口期连接失败=仍在重启，前端据此等待）。
func (s *AdminUpdateService) GetUpdateStatus(ctx context.Context, _ *emptypb.Empty) (*adminv1.UpdateStatus, error) {
	return toStatusPB(s.svc.Snapshot(ctx)), nil
}

// CheckUpdate 手动检查（源探测 + manifest 验签）。
func (s *AdminUpdateService) CheckUpdate(ctx context.Context, _ *emptypb.Empty) (*adminv1.UpdateCheckResult, error) {
	if err := s.svc.DisabledErr(); err != nil {
		return nil, errors.Forbidden("update.DISABLED", err.Error())
	}
	res, err := s.svc.Check(ctx)
	if err != nil {
		return nil, errors.InternalServer("update.CHECK_FAILED", err.Error())
	}
	return &adminv1.UpdateCheckResult{
		CurrentVersion: res.Current, LatestVersion: res.Latest,
		HasUpdate: res.HasUpdate, Notes: res.Notes, Channel: res.Channel, Source: res.Source,
		History: toHistoryPB(res.History),
	}, nil
}

// ApplyUpdate 触发更新（单飞；进行中重复调用返回当前态）。
func (s *AdminUpdateService) ApplyUpdate(ctx context.Context, _ *emptypb.Empty) (*adminv1.UpdateStatus, error) {
	if err := s.svc.DisabledErr(); err != nil {
		return nil, errors.Forbidden("update.DISABLED", err.Error())
	}
	if err := s.svc.Apply(ctx); err != nil {
		return toStatusPB(s.svc.Snapshot(ctx)), nil // ErrBusy 亦返回当前态（200 携带 busy）
	}
	return toStatusPB(s.svc.Snapshot(ctx)), nil
}

// RollbackUpdate 回滚上一版本并重启。
func (s *AdminUpdateService) RollbackUpdate(ctx context.Context, _ *emptypb.Empty) (*adminv1.UpdateStatus, error) {
	if err := s.svc.DisabledErr(); err != nil {
		return nil, errors.Forbidden("update.DISABLED", err.Error())
	}
	if err := s.svc.Rollback(ctx); err != nil {
		return nil, errors.InternalServer("update.ROLLBACK_FAILED", err.Error())
	}
	return toStatusPB(s.svc.Snapshot(ctx)), nil
}

func toStatusPB(st Status) *adminv1.UpdateStatus {
	checkedAt := ""
	if !st.CheckedAt.IsZero() {
		checkedAt = st.CheckedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return &adminv1.UpdateStatus{
		Phase: st.Phase, CurrentVersion: st.Current, TargetVersion: st.Target,
		ProgressPercent: st.Progress, ErrorMessage: st.Err, Source: st.Source,
		Mode: st.Mode, SupervisorKind: st.Supervisor, HasUpdate: st.HasUpdate,
		Notes: st.Notes, LatestVersion: st.Latest, CheckedAt: checkedAt,
		BackupDir: st.BackupDir, Busy: st.Busy,
		History: toHistoryPB(st.History),
	}
}

func toHistoryPB(h []updater.ReleaseNote) []*adminv1.ReleaseNoteEntry {
	if len(h) == 0 {
		return nil
	}
	out := make([]*adminv1.ReleaseNoteEntry, 0, len(h))
	for _, e := range h {
		out = append(out, &adminv1.ReleaseNoteEntry{
			Version: e.Version, Channel: e.Channel, Notes: e.Notes, IssuedAt: e.IssuedAt,
		})
	}
	return out
}
