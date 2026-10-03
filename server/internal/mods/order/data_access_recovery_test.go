package order

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	"github.com/NovaWorks/zcard-next/server/internal/mods/inventory"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
)

type accessRecoveryMail struct {
	messages []notifyport.Message
	ready    bool
	fail     bool
}

func (m *accessRecoveryMail) ChannelReady(context.Context, string) bool { return m.ready }
func (m *accessRecoveryMail) Send(_ context.Context, message notifyport.Message) error {
	if m.fail {
		return fmt.Errorf("delivery failed")
	}
	m.messages = append(m.messages, message)
	return nil
}

func guestPhysicalAccess(t *testing.T) (*data.Data, *OrderUsecase, CreateOrderInput, *CreateOrderResult) {
	t.Helper()
	d, uc := seedPhysical(t)
	cipher, err := inventory.NewCardCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	uc.AccessCipher = cipher
	in := physicalInput()
	in.UserID, in.QueryPassword, in.Contact, in.IdempotencyKey = 0, "", "buyer@example.test", "12345678-1234-4234-8234-123456789abc"
	q := in
	q.QuoteOnly = true
	quote, err := uc.CreateOrder(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if quote.OrderAccessToken != "" || d.Client.Order.Query().CountX(context.Background()) != 0 {
		t.Fatal("quote issued access or persisted an order")
	}
	in.QuoteKey = quote.QuoteKey
	r, err := uc.CreateOrder(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return d, uc, in, r
}

func TestGuestPhysicalCredentialAndReplay(t *testing.T) {
	d, uc, in, r := guestPhysicalAccess(t)
	ctx := context.Background()
	o := d.Client.Order.Query().OnlyX(ctx)
	if r.OrderAccessToken == "" || o.QueryPasswordHash != "" || !orderaccess.VerifyToken(o.OrderAccessTokenHash, o.SubsiteID, o.OrderNo, r.OrderAccessToken) {
		t.Fatal("guest physical credential missing")
	}
	if string(o.OrderAccessTokenSecret) == r.OrderAccessToken {
		t.Fatal("raw access credential persisted")
	}
	again, err := uc.CreateOrder(ctx, in)
	if err != nil || again.OrderAccessToken != r.OrderAccessToken || d.Client.Order.Query().CountX(ctx) != 1 {
		t.Fatalf("replay lost credential: %v", err)
	}
	changed := in
	changed.Items = append([]OrderItemInput(nil), in.Items...)
	changed.Items[0].Quantity++
	if _, err = uc.CreateOrder(ctx, changed); err == nil {
		t.Fatal("changed request recovered credential")
	}
	svc := NewStoreOrderService(uc, nil)
	if _, err = svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: r.OrderAccessToken}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong"} {
		if _, err = svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: token}); err == nil {
			t.Fatal("missing/invalid credential read address")
		}
	}
	if _, err = svc.GetOrder(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 99}), &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: r.OrderAccessToken}); err == nil {
		t.Fatal("cross-tenant address read")
	}
	other := d.Client.Order.Create().SetOrderNo("OTHER-ACCESS-ORDER").SetCommerceVersion(1).SaveX(ctx)
	if _, err = svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: other.OrderNo, OrderAccessToken: r.OrderAccessToken}); err == nil {
		t.Fatal("cross-order address read")
	}
}

func recoveryMailCode(t *testing.T, m *accessRecoveryMail) string {
	t.Helper()
	if len(m.messages) == 0 {
		t.Fatal("verification mail missing")
	}
	match := regexp.MustCompile(`<b>([0-9]{6})</b>`).FindStringSubmatch(m.messages[len(m.messages)-1].Body)
	if len(match) != 2 {
		t.Fatal("verification mail has no code")
	}
	if m.messages[len(m.messages)-1].BizType != "order_access_recovery" || len(m.messages[len(m.messages)-1].Variables) != 0 {
		t.Fatal("verification log masking contract missing")
	}
	return match[1]
}

func TestGuestPhysicalEmailRecoveryRotatesAndConsumes(t *testing.T) {
	d, uc, in, r := guestPhysicalAccess(t)
	svc := NewStoreOrderService(uc, nil)
	m := &accessRecoveryMail{ready: true}
	svc.SetRecoverySender(m)
	ctx := context.Background()
	for _, req := range []*storefrontv1.OrderAccessCodeRequest{{OrderNo: "MISSING", Email: in.Contact}, {OrderNo: r.OrderNo, Email: "wrong@example.test"}} {
		if _, err := svc.SendOrderAccessCode(ctx, req); err != nil {
			t.Fatal("enumeration response differs", err)
		}
	}
	if len(m.messages) != 0 || d.Client.EmailVerification.Query().CountX(ctx) != 0 {
		t.Fatal("mismatch sent verification")
	}
	req := &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: "BUYER@example.test"}
	if _, err := svc.SendOrderAccessCode(ctx, req); err != nil {
		t.Fatal(err)
	}
	code := recoveryMailCode(t, m)
	if _, err := svc.SendOrderAccessCode(ctx, req); err != nil || len(m.messages) != 1 {
		t.Fatal("resend bypassed cooldown", err)
	}
	row := d.Client.EmailVerification.Query().OnlyX(ctx)
	if row.Purpose != emailverification.PurposeOrderAccess || row.CodeHash == code || row.Email == in.Contact {
		t.Fatal("recovery is not scoped or hashed")
	}
	verify := &storefrontv1.OrderAccessRecoveryRequest{OrderNo: r.OrderNo, Email: in.Contact, Code: code}
	if _, err := svc.RecoverOrderAccess(tenancy.WithContext(ctx, tenancy.Context{SubsiteID: 1}), verify); err == nil {
		t.Fatal("cross-tenant recovery allowed")
	}
	wrong := &storefrontv1.OrderAccessRecoveryRequest{OrderNo: verify.OrderNo, Email: "wrong@example.test", Code: verify.Code}
	if _, err := svc.RecoverOrderAccess(ctx, wrong); err == nil {
		t.Fatal("wrong email recovered credential")
	}
	result, err := svc.RecoverOrderAccess(ctx, verify)
	if err != nil {
		t.Fatal(err)
	}
	if result.OrderAccessToken == "" || result.OrderAccessToken == r.OrderAccessToken {
		t.Fatal("recovery did not rotate access")
	}
	if _, err := svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: r.OrderAccessToken}); err == nil {
		t.Fatal("old credential remained valid")
	}
	if _, err := svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: result.OrderAccessToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecoverOrderAccess(ctx, verify); err == nil {
		t.Fatal("verification reused")
	}
	if _, err := uc.CreateOrder(ctx, in); err == nil {
		t.Fatal("old checkout key issued recovered credential")
	}
	o := d.Client.Order.Query().OnlyX(ctx)
	if len(o.OrderAccessTokenSecret) != 0 {
		t.Fatal("checkout replay secret remained after recovery")
	}
}

func TestGuestPhysicalRecoveryAttemptExpiryAndPurpose(t *testing.T) {
	d, uc, in, r := guestPhysicalAccess(t)
	svc := NewStoreOrderService(uc, nil)
	m := &accessRecoveryMail{ready: true}
	svc.SetRecoverySender(m)
	ctx := context.Background()
	if _, err := svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: in.Contact}); err != nil {
		t.Fatal(err)
	}
	code := recoveryMailCode(t, m)
	wrongCode := "000000"
	if wrongCode == code {
		wrongCode = "000001"
	}
	request := &storefrontv1.OrderAccessRecoveryRequest{OrderNo: r.OrderNo, Email: in.Contact, Code: wrongCode}
	for i := 0; i < orderAccessCodeMaxTries; i++ {
		if _, err := svc.RecoverOrderAccess(ctx, request); err == nil {
			t.Fatal("incorrect verification passed")
		}
	}
	row := d.Client.EmailVerification.Query().OnlyX(ctx)
	if row.AttemptCount != orderAccessCodeMaxTries {
		t.Fatalf("attempts were not committed: %d", row.AttemptCount)
	}
	request.Code = code
	if _, err := svc.RecoverOrderAccess(ctx, request); err == nil {
		t.Fatal("exhausted verification passed")
	}
	d.Client.EmailVerification.UpdateOneID(row.ID).SetAttemptCount(0).SetExpiresAt(time.Now().Add(-time.Second)).ExecX(ctx)
	if _, err := svc.RecoverOrderAccess(ctx, request); err == nil {
		t.Fatal("expired verification passed")
	}
	d.Client.EmailVerification.UpdateOneID(row.ID).SetExpiresAt(time.Now().Add(time.Minute)).SetPurpose(emailverification.PurposeRegister).ExecX(ctx)
	if _, err := svc.RecoverOrderAccess(ctx, request); err == nil {
		t.Fatal("registration purpose recovered order")
	}
}

func TestPhysicalOwnerNoPasswordAndGuestEmailRequired(t *testing.T) {
	_, uc := seedPhysical(t)
	in := physicalInput()
	in.QueryPassword = ""
	in = quotePhysical(t, uc, in)
	if _, err := uc.CreateOrder(context.Background(), in); err != nil {
		t.Fatal("signed-in physical buyer requires password", err)
	}
	for _, email := range []string{"", "+14155550100", "Name <buyer@example.test>"} {
		in.UserID, in.Contact = 0, email
		if _, err := uc.CreateOrder(context.Background(), in); err == nil {
			t.Fatal("guest physical order missing recovery email")
		}
	}
}

func TestGuestPhysicalRecoveryMailUsesRequestLocale(t *testing.T) {
	_, uc, in, r := guestPhysicalAccess(t)
	svc := NewStoreOrderService(uc, nil)
	mail := &accessRecoveryMail{ready: true}
	svc.SetRecoverySender(mail)
	ctx := i18n.WithLocale(context.Background(), i18n.En)
	if _, err := svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: in.Contact}); err != nil {
		t.Fatal(err)
	}
	if len(mail.messages) != 1 || mail.messages[0].Locale != "en" || mail.messages[0].Subject != "Order lookup verification code" || !strings.Contains(mail.messages[0].Body, "Verification code:") || strings.Contains(mail.messages[0].Body, "验证码") {
		t.Fatal("English lookup sent a Chinese verification email")
	}
	if recoveryMailCode(t, mail) == "" || mail.messages[0].Variables != nil {
		t.Fatal("verification code missing or included in template variables")
	}
	for _, token := range []string{"", "invalid"} {
		_, err := svc.GetOrder(ctx, &storefrontv1.GetOrderRequest{OrderNo: r.OrderNo, OrderAccessToken: token})
		problem := errors.FromError(err)
		if problem.Reason != "order.ACCESS_REQUIRED" || !strings.Contains(problem.Message, "email used at checkout") || strings.Contains(problem.Message, "password") {
			t.Fatal("physical access error requires an English email verification message", err)
		}
	}
	_, err := svc.RecoverOrderAccess(ctx, &storefrontv1.OrderAccessRecoveryRequest{OrderNo: r.OrderNo, Email: in.Contact, Code: "bad"})
	if !strings.Contains(errors.FromError(err).Message, "verification code") {
		t.Fatal("invalid recovery code was not localized", err)
	}
	_, err = svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{Email: in.Contact})
	if !strings.Contains(errors.FromError(err).Message, "Enter the order number") {
		t.Fatal("recovery form error was not localized", err)
	}
	mail.ready = false
	_, err = svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: in.Contact})
	if !strings.Contains(errors.FromError(err).Message, "not configured") {
		t.Fatal("email readiness error was not localized", err)
	}
	svc.SetRecoverySender(nil)
	_, err = svc.SendOrderAccessCode(ctx, &storefrontv1.OrderAccessCodeRequest{OrderNo: r.OrderNo, Email: in.Contact})
	if !strings.Contains(errors.FromError(err).Message, "temporarily unavailable") {
		t.Fatal("recovery availability error was not localized", err)
	}
}
