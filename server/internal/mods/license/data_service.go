package license

// 管理面 API（M3）：许可证安装/查询/清除（license:read 查询 / license:write 超管专属）。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/license"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminLicenseService 许可证管理。
type AdminLicenseService struct {
	adminv1.UnimplementedAdminLicenseServiceServer
	repo *LicenseRepo
}

// NewAdminLicenseService 构造。
func NewAdminLicenseService(repo *LicenseRepo) *AdminLicenseService {
	return &AdminLicenseService{repo: repo}
}

// GetLicenseStatus 状态查询。
func (s *AdminLicenseService) GetLicenseStatus(ctx context.Context, _ *emptypb.Empty) (*adminv1.LicenseStatus, error) {
	return toStatusPB(s.repo.Status(ctx)), nil
}

// InstallLicense 安装（校验失败不落库）。
func (s *AdminLicenseService) InstallLicense(ctx context.Context, req *adminv1.InstallLicenseRequest) (*adminv1.LicenseStatus, error) {
	if req.GetContent() == "" {
		return nil, errors.BadRequest("license.CONTENT_REQUIRED", "许可证内容必填")
	}
	if err := s.repo.Install(ctx, req.GetContent()); err != nil {
		return nil, errors.BadRequest("license.INVALID", "许可证无效（签名/绑定/到期任一失败）")
	}
	return toStatusPB(s.repo.Status(ctx)), nil
}

// ClearLicense 清除（回到社区版）。
func (s *AdminLicenseService) ClearLicense(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	if err := s.repo.Clear(ctx); err != nil {
		return nil, errors.InternalServer("license.CLEAR_FAILED", "清除失败")
	}
	return &emptypb.Empty{}, nil
}

func toStatusPB(st Status) *adminv1.LicenseStatus {
	return &adminv1.LicenseStatus{
		Installed: st.Installed, Valid: st.Valid,
		InstanceId: st.InstanceID, Domain: st.Domain,
		Features: st.Features, ExpiresAt: st.ExpiresAt, Error: st.Error,
	}
}

var _ = license.ErrInvalidLicense
