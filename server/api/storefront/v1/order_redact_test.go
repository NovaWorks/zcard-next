package storefrontv1

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v3/middleware/logging"
)

func TestOrderAccessCredentialsStayOutOfRequestLogs(t *testing.T) {
	for _, request := range []any{
		&CreateOrderRequest{QueryPassword: "private-password", Contact: "private-email@example.test"},
		&GetOrderRequest{OrderNo: "ORDER-1", OrderAccessToken: "private-token"},
		&ReceiveShipmentRequest{OrderNo: "ORDER-1", OrderAccessToken: "private-token", QueryPassword: "private-password"},
		&OrderAccessCodeRequest{OrderNo: "ORDER-1", Email: "private-email@example.test"},
		&OrderAccessRecoveryRequest{OrderNo: "ORDER-1", Email: "private-email@example.test", Code: "934782"},
		&CreatePaymentRequest{OrderNo: "ORDER-1", OrderAccessToken: "private-token", QueryPassword: "private-password"},
		&PaymentQuoteRequest{OrderNo: "ORDER-1", OrderAccessToken: "private-token", QueryPassword: "private-password"},
	} {
		var output bytes.Buffer
		handler := logging.Server(slog.New(slog.NewJSONHandler(&output, nil)))(func(context.Context, any) (any, error) { return nil, nil })
		if _, err := handler(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-token", "private-password", "private-email@example.test", "934782"} {
			if strings.Contains(output.String(), secret) {
				t.Fatalf("%T exposed a credential in request logs", request)
			}
		}
	}
}
