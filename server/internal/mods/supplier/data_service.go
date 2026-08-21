package supplier

// T6 管理面 API：下游账户（申请/审核/启停/重置密钥）、定价、账本、回调管理。
// secret 纪律：仅创建/重置时回显一次，其余响应零泄漏。

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminSupplierService 管理面供货服务。
type AdminSupplierService struct {
	adminv1.UnimplementedAdminSupplierServiceServer
	repo *SupplierRepoImpl
	api  *SupplyAPIService
}

// NewAdminSupplierService 构造。
func NewAdminSupplierService(repo *SupplierRepoImpl, api *SupplyAPIService) *AdminSupplierService {
	return &AdminSupplierService{repo: repo, api: api}
}

// CreateAccount 申请（secret 明文返回一次）。
func (s *AdminSupplierService) CreateAccount(ctx context.Context, req *adminv1.CreateSupplierAccountRequest) (*adminv1.SupplierAccountReply, error) {
	if req.GetApiKey() == "" || req.GetApiSecret() == "" {
		return nil, errors.New("supplier.KEY_REQUIRED: api_key/api_secret 必填")
	}
	protocol := req.GetProtocol()
	if protocol == "" {
		protocol = "zcard"
	}
	if protocol != "zcard" && protocol != "dujiao_next" && protocol != "acg_faka" {
		return nil, errors.New("supplier.INVALID_PROTOCOL: protocol 必须为 zcard|dujiao_next|acg_faka")
	}
	acc, err := s.repo.CreateAccount(ctx, req.GetName(), req.GetApiKey(), req.GetApiSecret(), req.GetContact(), protocol, req.GetDisplayName())
	if err != nil {
		return nil, err
	}
	return toAccountPB(acc, req.GetApiSecret()), nil
}

// ListAccounts 列表（secret 零回显）。
func (s *AdminSupplierService) ListAccounts(ctx context.Context, req *adminv1.ListSupplierAccountsRequest) (*adminv1.ListSupplierAccountsReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListAccounts(ctx, page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListSupplierAccountsReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, r := range rows {
		reply.Accounts = append(reply.Accounts, toAccountPB(r, ""))
	}
	return reply, nil
}

// ReviewAccount 审核（通过/驳回；驳回置 rejected 状态并记录意见）。
func (s *AdminSupplierService) ReviewAccount(ctx context.Context, req *adminv1.ReviewSupplierAccountRequest) (*adminv1.SupplierAccountReply, error) {
	if !req.GetApprove() && strings.TrimSpace(req.GetReviewNote()) == "" {
		return nil, errors.New("supplier.REVIEW_NOTE_REQUIRED: 驳回时请填写审核意见")
	}
	acc, err := s.repo.ReviewAccount(ctx, req.GetId(), req.GetApprove(), strings.TrimSpace(req.GetReviewNote()))
	if err != nil {
		return nil, err
	}
	return toAccountPB(acc, ""), nil
}

// ToggleAccount 启停。
func (s *AdminSupplierService) ToggleAccount(ctx context.Context, req *adminv1.ToggleSupplierAccountRequest) (*adminv1.SupplierAccountReply, error) {
	acc, err := s.repo.ToggleAccount(ctx, req.GetId(), req.GetEnabled())
	if err != nil {
		return nil, err
	}
	return toAccountPB(acc, ""), nil
}

// ResetSecret 重置密钥（明文返回一次）。
func (s *AdminSupplierService) ResetSecret(ctx context.Context, req *adminv1.ResetSupplierSecretRequest) (*adminv1.ResetSupplierSecretReply, error) {
	if err := s.repo.ResetSecret(ctx, req.GetId(), req.GetNewSecret()); err != nil {
		return nil, err
	}
	return &adminv1.ResetSupplierSecretReply{ApiSecret: req.GetNewSecret()}, nil
}

// SetNotifyURL 配置回调地址（HTTPS 强制）。
func (s *AdminSupplierService) SetNotifyURL(ctx context.Context, req *adminv1.SetSupplierNotifyURLRequest) (*adminv1.SupplierAccountReply, error) {
	u, err := url.Parse(req.GetNotifyUrl())
	if err != nil || u.Scheme != "https" {
		return nil, errors.New("supplier.NOTIFY_URL_HTTPS_REQUIRED: 回调地址必须 HTTPS")
	}
	if err := s.repo.SetNotifyURL(ctx, req.GetId(), req.GetNotifyUrl()); err != nil {
		return nil, err
	}
	acc, _ := s.repo.GetAccount(ctx, req.GetId())
	return toAccountPB(acc, ""), nil
}

// Recharge 充值（账本入账；reference 幂等由调用方保证）。
func (s *AdminSupplierService) Recharge(ctx context.Context, req *adminv1.RechargeSupplierRequest) (*emptypb.Empty, error) {
	if req.GetAmountCents() <= 0 {
		return nil, errors.New("supplier.INVALID_AMOUNT")
	}
	ref := fmt.Sprintf("recharge:manual:%d:%d", req.GetId(), req.GetAmountCents())
	if err := s.repo.Recharge(ctx, req.GetId(), req.GetAmountCents(), ref, orEmpty(req.GetRemark(), "管理员充值")); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListLedger 账本流水。
func (s *AdminSupplierService) ListLedger(ctx context.Context, req *adminv1.ListSupplierLedgerRequest) (*adminv1.ListSupplierLedgerReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListLedger(ctx, req.GetAccountId(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListSupplierLedgerReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, r := range rows {
		reply.Entries = append(reply.Entries, &adminv1.SupplierLedgerEntry{
			Id: r.ID, AccountId: r.AccountID, SupplyOrderId: r.SupplyOrderID,
			Type: r.Type, Amount: r.Amount, Reference: r.Reference, Remark: r.Remark,
			CreatedAt: r.CreatedAt.Unix(),
		})
	}
	return reply, nil
}

// UpsertPrice 供货定价。
func (s *AdminSupplierService) UpsertPrice(ctx context.Context, req *adminv1.UpsertSupplierPriceRequest) (*emptypb.Empty, error) {
	if req.GetPrice() <= 0 {
		return nil, errors.New("supplier.INVALID_PRICE")
	}
	if _, err := s.repo.UpsertPrice(ctx, req.GetAccountId(), req.GetProductId(), req.GetSkuId(), req.GetPrice()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListCallbacks 回调记录。
func (s *AdminSupplierService) ListCallbacks(ctx context.Context, req *adminv1.ListSupplierCallbacksRequest) (*adminv1.ListSupplierCallbacksReply, error) {
	page, size := pageParams(req.GetPage(), req.GetPageSize())
	rows, total, err := s.repo.ListCallbacks(ctx, req.GetStatus(), page, size)
	if err != nil {
		return nil, err
	}
	reply := &adminv1.ListSupplierCallbacksReply{Total: int64(total), Page: int32(page), PageSize: int32(size)}
	for _, r := range rows {
		reply.Callbacks = append(reply.Callbacks, &adminv1.SupplierCallback{
			Id: r.ID, SupplyOrderId: r.SupplyOrderID, AccountId: r.AccountID,
			DownstreamOrderNo: r.DownstreamOrderNo, CallbackUrl: r.CallbackURL,
			TraceId: r.TraceID, CallbackStatus: string(r.CallbackStatus),
			RetryCount: r.RetryCount, LastError: r.LastError,
		})
		if !r.LastCallbackAt.IsZero() {
			reply.Callbacks[len(reply.Callbacks)-1].LastCallbackAt = r.LastCallbackAt.Unix()
		}
	}
	return reply, nil
}

// ResendCallback 手动重发（死信恢复）。
func (s *AdminSupplierService) ResendCallback(ctx context.Context, req *adminv1.ResendSupplierCallbackRequest) (*emptypb.Empty, error) {
	cb, err := s.repo.ResetCallback(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	s.api.EnqueueCallback(ctx, cb.SupplyOrderID)
	return &emptypb.Empty{}, nil
}

func toAccountPB(acc *ent.SupplierAccount, secretOnce string) *adminv1.SupplierAccountReply {
	p := &adminv1.SupplierAccountReply{
		Id: acc.ID, Name: acc.Name, ApiKey: acc.APIKey,
		Contact: acc.Contact, Status: string(acc.Status),
		BalanceCache: acc.BalanceCache, NotifyUrl: acc.NotifyURL,
		CreatedAt: acc.CreatedAt.Unix(),
		Protocol:  string(acc.Protocol), DisplayName: acc.DisplayName,
		OwnerUserId: acc.OwnerUserID, ApplyReason: acc.ApplyReason,
		ReviewNote: acc.ReviewNote,
	}
	if !acc.ReviewedAt.IsZero() {
		p.ReviewedAt = acc.ReviewedAt.Unix()
	}
	if secretOnce != "" {
		p.ApiSecret = secretOnce
	}
	return p
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

func orEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
