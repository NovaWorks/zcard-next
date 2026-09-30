package fulfillment

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/audit"
	ordermod "github.com/NovaWorks/zcard-next/server/internal/mods/order"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestFetchPasswordFeedbackAcrossOrderAndDelivery(t *testing.T) {
	for _, exists := range []bool{true, false} {
		t.Run(strconv.FormatBool(exists), func(t *testing.T) {
			d, _, repo := newFulfillData(t)
			ctx := context.Background()
			repo.gate = audit.NewAuditRepo(d, slog.Default())
			if exists {
				hash, err := crypto.HashPassword("correct")
				if err != nil {
					t.Fatal(err)
				}
				d.Client.Order.Create().SetOrderNo("LOOKUP").SetQueryPasswordHash(hash).SaveX(ctx)
			}
			detail := ordermod.NewStoreOrderService(&ordermod.OrderUsecase{Data: d, Gate: repo.gate}, nil)
			delivery := NewStoreDeliveryService(repo)
			for i := 1; i <= 5; i++ {
				var err error
				if i%2 == 1 {
					_, err = detail.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: "LOOKUP", QueryPassword: "wrong"})
				} else {
					_, err = delivery.FetchDelivery(ctx, &storefrontv1.FetchDeliveryRequest{OrderNo: "LOOKUP", QueryPassword: "wrong"})
				}
				e := kerrors.FromError(err)
				if i < 5 {
					if e.Code != 404 || e.Metadata["remaining_attempts"] != strconv.Itoa(5-i) || !strings.Contains(e.Message, "还可尝试") {
						t.Fatalf("attempt %d: %v", i, e)
					}
				} else if e.Code != 429 || e.Reason != "order.FETCH_LOCKED" || e.Metadata["retry_after_seconds"] == "" {
					t.Fatal(e)
				}
			}
			_, err := delivery.FetchDelivery(ctx, &storefrontv1.FetchDeliveryRequest{OrderNo: "LOOKUP", QueryPassword: "correct"})
			if e := kerrors.FromError(err); e.Code != 429 || !strings.Contains(e.Message, "分钟后重试") {
				t.Fatal(e)
			}
		})
	}
}
