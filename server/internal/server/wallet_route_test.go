package server

// 钱包管理路由分发回归测试（wallet CODEC 400 修复）：
// GET /api/v1/admin/wallet/withdrawals 与 /giftcard-batches 必须命中各自的列表
// handler，而不是被 /wallet/{user_id} 通配路由吞掉后在 BindVars 抛
// `parsing "withdrawals": invalid syntax`。
// 防护点：protoc-gen-go-http 按 proto 声明顺序注册 + gorilla/mux 先注册先匹配。

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	_ "github.com/go-kratos/kratos/v3/encoding/json" // 注册 application/json codec（POST body 解码）
	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// walletRouteStub 每个方法返回携带方法名的错误，响应体即命中证据。
type walletRouteStub struct{}

func (walletRouteStub) GetBalance(context.Context, *adminv1.GetBalanceRequest) (*adminv1.Balance, error) {
	return nil, errors.BadRequest("stub", "GetBalance")
}
func (walletRouteStub) Adjust(context.Context, *adminv1.AdjustRequest) (*adminv1.Balance, error) {
	return nil, errors.BadRequest("stub", "Adjust")
}
func (walletRouteStub) ListTransactions(context.Context, *adminv1.ListWalletTxRequest) (*adminv1.ListWalletTxReply, error) {
	return nil, errors.BadRequest("stub", "ListTransactions")
}
func (walletRouteStub) ListWithdrawals(context.Context, *adminv1.ListWithdrawalsRequest) (*adminv1.ListWithdrawalsReply, error) {
	return nil, errors.BadRequest("stub", "ListWithdrawals")
}
func (walletRouteStub) ReviewWithdrawal(context.Context, *adminv1.ReviewWithdrawalRequest) (*adminv1.WithdrawalItem, error) {
	return nil, errors.BadRequest("stub", "ReviewWithdrawal")
}
func (walletRouteStub) PayWithdrawal(context.Context, *adminv1.PayWithdrawalRequest) (*adminv1.WithdrawalItem, error) {
	return nil, errors.BadRequest("stub", "PayWithdrawal")
}
func (walletRouteStub) CreateGiftcardBatch(context.Context, *adminv1.CreateGiftcardBatchRequest) (*adminv1.CreateGiftcardBatchReply, error) {
	return nil, errors.BadRequest("stub", "CreateGiftcardBatch")
}
func (walletRouteStub) ListGiftcardBatches(context.Context, *adminv1.ListGiftcardBatchesRequest) (*adminv1.ListGiftcardBatchesReply, error) {
	return nil, errors.BadRequest("stub", "ListGiftcardBatches")
}

func TestAdminWalletRouteDispatch(t *testing.T) {
	srv := khttp.NewServer()
	adminv1.RegisterAdminWalletServiceHTTPServer(srv, walletRouteStub{})

	cases := []struct {
		name, method, path, body, want string
	}{
		{"提现列表不被通配路由吞掉", "GET", "/api/v1/admin/wallet/withdrawals?page=1&page_size=20", "", "ListWithdrawals"},
		{"礼品卡批次列表不被通配路由吞掉", "GET", "/api/v1/admin/wallet/giftcard-batches?page=1&page_size=20", "", "ListGiftcardBatches"},
		{"余额命中通配路由", "GET", "/api/v1/admin/wallet/42", "", "GetBalance"},
		{"调账", "POST", "/api/v1/admin/wallet/42/adjust", "{}", "Adjust"},
		{"流水", "GET", "/api/v1/admin/wallet/42/transactions", "", "ListTransactions"},
		{"提现审核", "POST", "/api/v1/admin/wallet/withdrawals/7/review", "{}", "ReviewWithdrawal"},
		{"提现打款", "POST", "/api/v1/admin/wallet/withdrawals/7/pay", "{}", "PayWithdrawal"},
		{"创建礼品卡批次", "POST", "/api/v1/admin/wallet/giftcard-batches", "{}", "CreateGiftcardBatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			srv.ServeHTTP(rec, req)
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("请求 %s %s 未命中 %s，实际 %d %s", tc.method, tc.path, tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}
