package wallet

// 礼品卡服务面（P1-05 T4）：admin 批次创建/列表 + storefront 兑换。

import (
	"context"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"

	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// 礼品卡方法挂在 AdminWalletService（批次管理）与 StoreWalletService（兑换）上，
// 构造注入 *GiftcardRepo（wire）。// CreateGiftcardBatch 批次创建（面额/数量服务端边界校验——铁律 16）。
// 明文码仅本响应一次性返回（此后不可取——库内无明文，铁律 11）。
func (s *AdminWalletService) CreateGiftcardBatch(ctx context.Context, req *adminv1.CreateGiftcardBatchRequest) (*adminv1.CreateGiftcardBatchReply, error) {
	if !money.ValidCents(req.GetAmountCents()) || req.GetAmountCents() <= 0 {
		return nil, errors.BadRequest("wallet.GIFTCARD_INVALID", "面额须为正且不超上限")
	}
	batch, codes, err := s.giftcards.CreateBatch(ctx, BatchInput{
		BatchNo: req.GetBatchNo(), Name: req.GetName(),
		Amount: req.GetAmountCents(), Quantity: req.GetQuantity(),
		Operator: adminWalletUID(ctx),
	})
	if err != nil {
		msg := err.Error()
		if containsStr(msg, "BATCH_EXISTS") {
			return nil, errors.BadRequest("wallet.BATCH_EXISTS", "批次号已存在")
		}
		if containsStr(msg, "QUANTITY") {
			return nil, errors.BadRequest("wallet.QUANTITY_INVALID", "数量须在 1-5000 之间")
		}
		return nil, errors.InternalServer("wallet.BATCH_FAILED", "批次创建失败")
	}
	return &adminv1.CreateGiftcardBatchReply{Batch: toGiftcardBatchPB(batch), Codes: codes}, nil
}

// ListGiftcardBatches 批次列表。
func (s *AdminWalletService) ListGiftcardBatches(ctx context.Context, req *adminv1.ListGiftcardBatchesRequest) (*adminv1.ListGiftcardBatchesReply, error) {
	page, size := withdrawPage(req.GetPage(), req.GetPageSize())
	rows, total, err := s.giftcards.ListBatches(ctx, page, size)
	if err != nil {
		return nil, errors.InternalServer("wallet.BATCH_LIST_FAILED", "读取批次失败")
	}
	reply := &adminv1.ListGiftcardBatchesReply{Total: total}
	for _, b := range rows {
		reply.Batches = append(reply.Batches, toGiftcardBatchPB(b))
	}
	return reply, nil
}

// RedeemGiftcard 兑换（登录用户；失败统一「卡密无效」防枚举）。
func (s *StoreWalletService) RedeemGiftcard(ctx context.Context, req *storefrontv1.RedeemGiftcardRequest) (*storefrontv1.RedeemGiftcardReply, error) {
	claims := identity.ClaimsFromContext(ctx)
	if claims == nil {
		return nil, errors.Unauthorized("identity.UNAUTHORIZED", "未登录")
	}
	if req.GetCode() == "" {
		return nil, errors.BadRequest("wallet.CODE_REQUIRED", "卡密必填")
	}
	amount, err := s.giftcards.Redeem(ctx, req.GetCode(), claims.Subject)
	if err != nil {
		msg := err.Error()
		if containsStr(msg, "LOCKED") {
			return nil, errors.Forbidden("wallet.GIFTCARD_LOCKED", "尝试次数过多，请稍后再试")
		}
		return nil, errors.BadRequest("wallet.GIFTCARD_INVALID", "卡密无效或已使用")
	}
	avail, _, _ := s.giftcards.wallet.GetBalance(ctx, claims.Subject)
	return &storefrontv1.RedeemGiftcardReply{
		AmountCents: amount, BalanceAfterCents: avail,
	}, nil
}

func toGiftcardBatchPB(b *ent.GiftcardBatch) *adminv1.GiftcardBatchItem {
	out := &adminv1.GiftcardBatchItem{
		Id: b.ID, BatchNo: b.BatchNo, Name: b.Name,
		AmountCents: b.Amount, Quantity: b.Quantity,
	}
	if !b.CreatedAt.IsZero() {
		out.CreatedAt = b.CreatedAt.Unix()
	}
	return out
}

var _ = emptypb.Empty{}
