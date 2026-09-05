package affiliate

// 前台分销 API（）：推广码/团队/流水。管理侧列表经 dashboard 模块
// （AdminDashboardService.ListCommissions 消费 port.CommissionReader，通道 A）。

import (
	"context"
	"fmt"
	"strings"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"

	
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

// StoreAffiliateService 前台分销服务。
type StoreAffiliateService struct {
	storefrontv1.UnimplementedStoreAffiliateServiceServer
	repo   *CommissionRepo
	users  *identity.UserRepo // 推广码懒生成（通道 A）
}

// NewStoreAffiliateService 构造。
func NewStoreAffiliateService(repo *CommissionRepo, users *identity.UserRepo) *StoreAffiliateService {
	return &StoreAffiliateService{repo: repo, users: users}
}

// MyAffiliate 推广码 + 统计（invite_url 用请求 Host 拼绝对 URL；推广码懒生成）。
func (s *StoreAffiliateService) MyAffiliate(ctx context.Context, _ *emptypb.Empty) (*storefrontv1.MyAffiliateReply, error) {
	userID := currentUID(ctx)
	if userID == 0 {
		return nil, fmt.Errorf("affiliate.UNAUTHORIZED: 请先登录")
	}
	stats, err := s.repo.StatsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	l1, l2, l3, err := s.repo.TeamCounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	// 推广码（懒生成——存量用户首次访问推广中心即补）
	promoCode := s.users.EnsurePromoCode(ctx, userID)
	// 推广链接：请求 Host 拼绝对 URL（复制即可分享；无 Host 回退相对路径）。
	// scheme 判定：本地开发 host 回退 http，其余一律 https（生产标准）。
	inviteURL := fmt.Sprintf("/?ref=%s", promoCode)
	if host := requestHost(ctx); host != "" {
		scheme := "https"
		if isLocalHost(host) {
			scheme = "http"
		}
		inviteURL = fmt.Sprintf("%s://%s/?ref=%s", scheme, host, promoCode)
	}
	return &storefrontv1.MyAffiliateReply{
		UserId:         userID,
		InviteUrl:      inviteURL,
		PromoCode:      promoCode,
		PendingCents:   stats.PendingCents,
		AvailableCents: stats.AvailableCents,
		WithdrawnCents: stats.WithdrawnCents,
		TotalCents:     stats.TotalCents,
		DebtCents:      stats.DebtCents,
		TeamL1:         l1, TeamL2: l2, TeamL3: l3,
	}, nil
}

// requestHost 从 kratos transport 取请求 Host（拼绝对推广链接）。
// 用 RequestFromServerContext（*Transport 实现 Transporter 小接口；直接断言
// khttp.Context 巨型接口会失败——同 Payment 埋点中间件教训）。
func requestHost(ctx context.Context) string {
	if r, ok := khttp.RequestFromServerContext(ctx); ok {
		h := r.Host
		if h == "" {
			h = r.Header.Get("X-Forwarded-Host")
		}
		return h
	}
	return ""
}

// isLocalHost 本地开发 host 判定（推广链接回退 http scheme）。
func isLocalHost(host string) bool {
	host = strings.Split(host, ":")[0]
	return host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" ||
		len(host) > 6 && host[len(host)-6:] == ".local"
}

// ListTeam 下级列表（用户名脱敏）。
func (s *StoreAffiliateService) ListTeam(ctx context.Context, req *storefrontv1.ListTeamRequest) (*storefrontv1.ListTeamReply, error) {
	userID := currentUID(ctx)
	if userID == 0 {
		return nil, fmt.Errorf("affiliate.UNAUTHORIZED")
	}
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListTeam(ctx, userID, int(req.GetTier()), page, size)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListTeamReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, u := range rows {
		tier := 1
		if u.InviteL1 != userID && u.InviteL2 == userID {
			tier = 2
		}
		if u.InviteL1 != userID && u.InviteL2 != userID && u.InviteL3 == userID {
			tier = 3
		}
		reply.Members = append(reply.Members, &storefrontv1.TeamMember{
			UserId: u.ID, UsernameMasked: maskName(u.Username), Tier: int32(tier),
			JoinedAt: u.CreatedAt.Unix(),
		})
	}
	return reply, nil
}

// ListCommissions 佣金流水。
func (s *StoreAffiliateService) ListCommissions(ctx context.Context, req *storefrontv1.ListMyCommissionsRequest) (*storefrontv1.ListMyCommissionsReply, error) {
	userID := currentUID(ctx)
	if userID == 0 {
		return nil, fmt.Errorf("affiliate.UNAUTHORIZED")
	}
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListByUser(ctx, userID, page, size)
	if err != nil {
		return nil, err
	}
	reply := &storefrontv1.ListMyCommissionsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, c := range rows {
		item := &storefrontv1.CommissionItem{
			Id: c.ID, OrderId: c.OrderID, Tier: int32(c.Tier),
			BaseAmount: c.BaseAmount, Amount: c.Amount,
			Status: string(c.Status), CreatedAt: c.CreatedAt.Unix(),
		}
		if !c.AvailableAt.IsZero() {
			item.AvailableAt = c.AvailableAt.Unix()
		}
		reply.Commissions = append(reply.Commissions, item)
	}
	return reply, nil
}

// maskName 用户名脱敏（首尾保留，中间 ***；≤2 位全掩）。
func maskName(name string) string {
	runes := []rune(name)
	if len(runes) <= 2 {
		return "***"
	}
	return string(runes[0]) + "***" + string(runes[len(runes)-1])
}

func currentUID(ctx context.Context) uint64 {
	if claims := identity.ClaimsFromContext(ctx); claims != nil {
		return claims.Subject
	}
	return 0
}

func pageParams(page, pageSize int32) (int, int) {
	p := int(page)
	if p < 1 {
		p = 1
	}
	ps := int(pageSize)
	if ps < 1 {
		ps = 20
	}
	if ps > 100 {
		ps = 100
	}
	return p, ps
}
