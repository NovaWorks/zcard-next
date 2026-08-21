package captcha

// HTTP transport（生成端点；免鉴权——注册/登录前可用）。

import (
	"context"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreCaptchaService HTTP 服务（实现 storefront proto）。
type StoreCaptchaService struct {
	storefrontv1.UnimplementedStoreCaptchaServiceServer
	svc *Service
}

// NewStoreCaptchaService 构造。
func NewStoreCaptchaService(svc *Service) *StoreCaptchaService {
	return &StoreCaptchaService{svc: svc}
}

// GetImage 生成验证码。
func (s *StoreCaptchaService) GetImage(_ context.Context, _ *emptypb.Empty) (*storefrontv1.CaptchaImage, error) {
	id, b64, err := s.svc.Get()
	if err != nil {
		return nil, err
	}
	return &storefrontv1.CaptchaImage{CaptchaId: id, ImageBase64: b64}, nil
}
