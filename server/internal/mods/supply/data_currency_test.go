package supply

import (
	"context"
	"encoding/json"
	"testing"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

func TestSMSBrowseRejectsUnsupportedBaseCurrencyBeforeUpstream(t *testing.T) {
	repo, d := newTestRepo(t)
	ctx := context.Background()
	d.Client.Setting.Create().SetGroup("i18n").SetKey("base_currency").SetValue(json.RawMessage(`"AUD"`)).SaveX(ctx)
	s := NewStoreSMSChannelService(NewGateway(repo, nil, nil))
	if _, err := s.Options(ctx, &storefrontv1.SMSChannelBrowseRequest{ProductId: 1}); kerrors.FromError(err).Reason != "sms.CURRENCY_UNSUPPORTED" {
		t.Fatalf("AUD browse reached CNY-only catalog: %v", err)
	}
	if _, err := s.Offers(ctx, &storefrontv1.SMSChannelBrowseRequest{ProductId: 1}); kerrors.FromError(err).Reason != "sms.CURRENCY_UNSUPPORTED" {
		t.Fatalf("AUD offers reached CNY-only catalog: %v", err)
	}
}
