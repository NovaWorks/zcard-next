package procurement

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

type smsCodecFailure struct{ smsFault }

func (smsCodecFailure) Error() string {
	return "provider body: private-token private-phone private-sms"
}

type smsCodecClient struct{ normalizedSMSClient }

func (f *smsCodecClient) CreateSMS(_ context.Context, _ supplyport.SMSPurchase) (*supplyport.SMSOrder, error) {
	f.purchaseCalls++
	return nil, fmt.Errorf("purchase failed: %w", smsCodecFailure{smsFault{status: 400, code: "CODEC"}})
}

func TestSMSCodecFailureStopsOriginalPurchase(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	d.Client.SupplyConnection.Create().SetID(row.ConnectionID).SetName("original").SetDriver("zcard").SetBaseURL("https://example.invalid").SetCredentials([]byte("sealed")).SaveX(ctx)
	d.Client.SMSIntent.UpdateOneID(row.ID).SetPhase("purchase").ClearUpstreamOrderID().SetChargedAmount(0).SetAttempts(11).SetLeaseUntil(0).SetNextRunAt(0).ExecX(ctx)
	original := d.Client.SMSIntent.GetX(ctx, row.ID)
	client := &smsCodecClient{}
	s.gw = &smsWorkerGateway{client: client}
	var logs bytes.Buffer
	s.log = slog.New(slog.NewJSONHandler(&logs, nil))

	s.RunSMS(ctx)
	saved := d.Client.SMSIntent.GetX(ctx, row.ID)
	if saved.Phase != "review" || saved.LastError != "upstream_codec_error" || saved.Attempts != 11 || saved.LeaseUntil != 0 {
		t.Fatalf("codec failure did not stop purchase: phase=%s diagnostic=%s attempts=%d lease=%d", saved.Phase, saved.LastError, saved.Attempts, saved.LeaseUntil)
	}
	if saved.RequestJSON != original.RequestJSON || saved.RequestHash != original.RequestHash || saved.RequestNo != original.RequestNo || saved.ConnectionIdentity != original.ConnectionIdentity {
		t.Fatal("original purchase intent changed")
	}
	if saved.UpstreamOrderID != nil || saved.ChargedAmount != 0 || saved.RejectedReceipt || saved.RetailRefundState != "none" || d.Client.Order.GetX(ctx, row.OrderID).Status != "paid" {
		t.Fatal("codec failure inferred a receipt or changed payment/refund state")
	}
	if !strings.Contains(logs.String(), "upstream_codec_error") || !strings.Contains(logs.String(), `"http_status":400`) {
		t.Fatal("safe codec diagnostic missing from log")
	}
	for _, private := range []string{"private-token", "private-phone", "private-sms", original.RequestJSON} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private upstream payload leaked to log")
		}
	}
	s.RunSMS(ctx)
	if client.purchaseCalls != 1 {
		t.Fatal("reviewed purchase retried automatically")
	}
}

func TestSMSCodecClassificationPreservesUncertainRetries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"other_bad_request", 400, "supply.INVALID_ORDER"},
		{"server_error", 500, "CODEC"},
		{"rate_limit", 429, "CODEC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d, row := smsEnv(t)
			ctx := context.Background()
			s.smsFailure(ctx, row, smsFault{status: tc.status, code: tc.code, delay: time.Minute})
			saved := d.Client.SMSIntent.GetX(ctx, row.ID)
			if saved.Phase != row.Phase || saved.LastError != "upstream_retrying" || saved.Attempts != 1 || saved.NextRunAt < time.Now().Add(59*time.Second).Unix() || saved.RetailRefundState != "none" {
				t.Fatalf("unrelated failure disposition changed: %+v", saved)
			}
		})
	}
}

func TestSMSCodecFailureRetainsDiagnosticAtRetryLimit(t *testing.T) {
	s, d, row := smsEnv(t)
	ctx := context.Background()
	row = d.Client.SMSIntent.UpdateOneID(row.ID).SetAttempts(12).SaveX(ctx)
	s.smsFailure(ctx, row, smsFault{status: 400, code: "CODEC"})
	saved := d.Client.SMSIntent.GetX(ctx, row.ID)
	if saved.Phase != "review" || saved.LastError != "upstream_codec_error" || saved.Attempts != 12 || saved.RetailRefundState != "none" {
		t.Fatal("retry exhaustion hid the codec diagnostic or changed settlement")
	}
}
