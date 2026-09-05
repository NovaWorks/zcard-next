package catalog

// 评价流数据层（ ）：真实评价（一单一评 + 审核流）+ 虚拟评价 + 前台合并展示。
// ent import 收口：data 前缀文件（架构测试规则 3b）。

import (
	"context"
	"fmt"
	"sort"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/review"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/virtualreview"
	"github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ErrReviewDuplicate 一单一评约束（UNIQUE(order_id) 兜底；此处先查给出业务语义）。
var ErrReviewDuplicate = fmt.Errorf("catalog.REVIEW_DUPLICATE")

// ── 真实评价（admin 审核）────────────────────────────────────

// ListReviews 评价列表（按 status 过滤；空=全部）。
func (r *ProductRepoImpl) ListReviews(ctx context.Context, status string, page, pageSize int32) ([]*ent.Review, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	q := data.Client(ctx, r.data).Review.Query().Order(ent.Desc(review.FieldCreatedAt))
	if status != "" {
		q = q.Where(review.StatusEQ(review.Status(status)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Clone().
		Offset((int(page) - 1) * int(pageSize)).
		Limit(int(pageSize)).
		All(ctx)
	return rows, int64(total), err
}

// ApproveReview 审核通过（pending/approved/rejected → approved）。
func (r *ProductRepoImpl) ApproveReview(ctx context.Context, id uint64) (*ent.Review, error) {
	return data.Client(ctx, r.data).Review.UpdateOneID(id).
		SetStatus(review.StatusApproved).
		Save(ctx)
}

// RejectReview 审核拒绝（pending/approved/rejected → rejected）。
func (r *ProductRepoImpl) RejectReview(ctx context.Context, id uint64) (*ent.Review, error) {
	return data.Client(ctx, r.data).Review.UpdateOneID(id).
		SetStatus(review.StatusRejected).
		Save(ctx)
}

// CreateReview 创建真实评价（一单一评校验；调用方：storefront 评价提交， 预留）。
func (r *ProductRepoImpl) CreateReview(ctx context.Context, productID, userID, orderID uint64, rating int8, content string) (*ent.Review, error) {
	tc := tenancy.FromContext(ctx)
	exists, err := data.Client(ctx, r.data).Review.Query().
		Where(review.OrderID(orderID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrReviewDuplicate
	}
	return data.Client(ctx, r.data).Review.Create().
		SetSubsiteID(tc.SubsiteID).
		SetProductID(productID).
		SetUserID(userID).
		SetOrderID(orderID).
		SetRating(rating).
		SetContent(content).
		SetStatus(review.StatusPending).
		Save(ctx)
}

// ── 虚拟评价 ────────────────────────────────────────────────

// CreateVirtualReview 创建虚拟评价。
func (r *ProductRepoImpl) CreateVirtualReview(ctx context.Context, productID uint64, nickname, content string, rating int8, sort int32) (*ent.VirtualReview, error) {
	return data.Client(ctx, r.data).VirtualReview.Create().
		SetProductID(productID).
		SetNickname(nickname).
		SetContent(content).
		SetRating(rating).
		SetSort(sort).
		Save(ctx)
}

// ListVirtualReviews 商品虚拟评价（按 sort 升序，sort 相同按时间倒序）。
func (r *ProductRepoImpl) ListVirtualReviews(ctx context.Context, productID uint64) ([]*ent.VirtualReview, error) {
	return data.Client(ctx, r.data).VirtualReview.Query().
		Where(virtualreview.ProductID(productID)).
		Order(ent.Asc(virtualreview.FieldSort), ent.Desc(virtualreview.FieldCreatedAt)).
		All(ctx)
}

// ListProductReviews 前台评价合并：真实 approved（按时间倒序）+ 虚拟（按 sort/时间）。
// 合并策略：真实评价优先按时间倒序，虚拟评价按 sort 穿插在后（规划 ）。
func (r *ProductRepoImpl) ListProductReviews(ctx context.Context, productID uint64) ([]port.ReviewItem, error) {
	client := data.Client(ctx, r.data)
	realRows, err := client.Review.Query().
		Where(review.ProductID(productID), review.StatusEQ(review.StatusApproved)).
		Order(ent.Desc(review.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	virtualRows, err := client.VirtualReview.Query().
		Where(virtualreview.ProductID(productID)).
		Order(ent.Asc(virtualreview.FieldSort), ent.Desc(virtualreview.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]port.ReviewItem, 0, len(realRows)+len(virtualRows))
	for _, v := range realRows {
		out = append(out, port.ReviewItem{
			ID:        v.ID,
			Nickname:  "", // 真实评价无昵称列；前台用「匿名用户」兜底
			Content:   v.Content,
			Rating:    int32(v.Rating),
			IsVirtual: false,
			CreatedAt: v.CreatedAt,
		})
	}
	for _, v := range virtualRows {
		out = append(out, port.ReviewItem{
			ID:        v.ID,
			Nickname:  v.Nickname,
			Content:   v.Content,
			Rating:    int32(v.Rating),
			IsVirtual: true,
			Sort:      v.Sort,
			CreatedAt: v.CreatedAt,
		})
	}
	// 稳定排序：真实（时间倒序）在前，虚拟（sort 升序 → 时间倒序）在后。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsVirtual != out[j].IsVirtual {
			return !out[i].IsVirtual // 真实在前
		}
		if out[i].IsVirtual {
			if out[i].Sort != out[j].Sort {
				return out[i].Sort < out[j].Sort
			}
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
