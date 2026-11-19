package supply

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/supply/adapter"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

func TestDujiaoRefundCallbackDoesNotReplayOldDelivery(t *testing.T) {
	for _, status := range []string{"refunded", "partially_refunded", "refunding", "canceled"} {
		body := []byte(fmt.Sprintf(`{"downstream_order_no":"callback-%s","status":%q,"fulfillment":{"status":"delivered","payload":"old-private-delivery"}}`, status, status))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		auth := &supplyport.UpstreamCallbackAuth{Body: body, Headers: map[string]string{"Dujiao-Next-Api-Key": "fixture", "Dujiao-Next-Timestamp": ts, "Dujiao-Next-Signature": adapter.DujiaoSign("fixture-secret", "POST", "/api/v1/upstream/callback", ts, body)}}
		got, err := verifyDujiaoCallback("fixture", "fixture-secret", auth)
		if err != nil || got.Status != status || len(got.Cards) != 0 {
			t.Fatalf("callback %s changed outcome: %+v %v", status, got, err)
		}
	}
}
