package order

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"math/big"
	"strings"
	"time"

	storefrontv1 "github.com/NovaWorks/zcard-next/server/api/storefront/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/emailverification"
	entorder "github.com/NovaWorks/zcard-next/server/internal/data/ent/order"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/i18n"
	"github.com/NovaWorks/zcard-next/server/internal/platform/orderaccess"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
	"github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

const orderAccessCodeTTL = 10 * time.Minute
const orderAccessCodeCooldown = time.Minute
const orderAccessCodeMaxTries = 5

func recoveryScope(site uint64, no, email string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("order-access-recovery:v1:%d:%s:%s", site, no, email)))
	return "order-access:" + hex.EncodeToString(h[:])
}

func recoveryInvalid(ctx context.Context) error {
	return errors.BadRequest("order.ACCESS_CODE_INVALID", i18n.Message(ctx, "order.access_code_invalid", "订单或邮箱不匹配，或验证码无效、已使用或已过期"))
}

func recoveryOrderMatches(o *ent.Order, email string) bool {
	return o != nil && o.UserID == 0 && o.OrderAccessTokenHash != "" && strings.EqualFold(strings.TrimSpace(o.Contact), email)
}

func (s *StoreOrderService) recoveryCodeHash(scope, code string) string {
	return s.uc.AccessCipher.ContentHash("order-access-code:v1:" + scope + ":" + code)
}

func (s *StoreOrderService) SendOrderAccessCode(ctx context.Context, req *storefrontv1.OrderAccessCodeRequest) (*emptypb.Empty, error) {
	email, err := orderaccess.EmailAddress(req.Email)
	if err != nil || req.OrderNo == "" || len(req.OrderNo) > 100 {
		return nil, errors.BadRequest("order.CONTACT_INVALID", i18n.Message(ctx, "order.access_contact_invalid", "请填写订单号和下单邮箱"))
	}
	// Readiness is checked before looking up the order so a configuration failure cannot expose its existence.
	if s.recoverySender == nil || s.uc.AccessCipher == nil {
		return nil, errors.InternalServer("order.ACCESS_RECOVERY_UNAVAILABLE", i18n.Message(ctx, "order.access_recovery_unavailable", "订单邮件验证暂时不可用，请稍后重试"))
	}
	if ready, ok := s.recoverySender.(interface {
		ChannelReady(context.Context, string) bool
	}); ok && !ready.ChannelReady(ctx, "email") {
		return nil, errors.BadRequest("order.ACCESS_EMAIL_UNAVAILABLE", i18n.Message(ctx, "order.access_email_unavailable", "邮箱验证通道尚未配置，请联系客服"))
	}
	site := tenancy.FromContext(ctx).SubsiteID
	scope := recoveryScope(site, req.OrderNo, email)
	var verification *ent.EmailVerification
	var code string
	err = data.Tx(ctx, s.uc.Data, func(txCtx context.Context) error {
		c := data.Client(txCtx, s.uc.Data)
		o, e := c.Order.Query().Where(entorder.OrderNo(req.OrderNo), entorder.SubsiteID(site)).Only(txCtx)
		if ent.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !recoveryOrderMatches(o, email) {
			return nil
		}
		latest, e := c.EmailVerification.Query().Where(emailverification.Email(scope), emailverification.PurposeEQ(emailverification.PurposeOrderAccess)).Order(ent.Desc(emailverification.FieldCreatedAt), ent.Desc(emailverification.FieldID)).First(txCtx)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if latest != nil && time.Since(latest.CreatedAt) < orderAccessCodeCooldown {
			return nil
		}
		// Only actionable attempts lock the order; repeated cooldown requests do not change its version.
		n, e := c.Order.Update().Where(entorder.ID(o.ID), entorder.Version(o.Version)).AddVersion(1).Save(txCtx)
		if e != nil {
			return e
		}
		if n != 1 {
			return nil
		}
		number, e := rand.Int(rand.Reader, big.NewInt(1000000))
		if e != nil {
			return e
		}
		code = fmt.Sprintf("%06d", number.Int64())
		verification, e = c.EmailVerification.Create().SetEmail(scope).SetPurpose(emailverification.PurposeOrderAccess).SetCodeHash(s.recoveryCodeHash(scope, code)).SetExpiresAt(time.Now().Add(orderAccessCodeTTL)).Save(txCtx)
		return e
	})
	if err != nil {
		return nil, errors.InternalServer("order.ACCESS_RECOVERY_UNAVAILABLE", i18n.Message(ctx, "order.access_recovery_unavailable", "订单邮件验证暂时不可用，请稍后重试"))
	}
	if verification == nil {
		return &emptypb.Empty{}, nil
	}
	locale := i18n.FromContext(ctx)
	subject := "订单查询邮箱验证码"
	body := fmt.Sprintf("<p>您正在查询订单 <b>%s</b>。</p><p>验证码：<b>%s</b>，10 分钟内有效。</p><p>若非本人操作，请忽略本邮件。</p>", html.EscapeString(req.OrderNo), code)
	if locale == i18n.En {
		subject = "Order lookup verification code"
		body = fmt.Sprintf("<p>You requested access to order <b>%s</b>.</p><p>Verification code: <b>%s</b>. This code expires in 10 minutes.</p><p>If you did not request this, please ignore this email.</p>", html.EscapeString(req.OrderNo), code)
	}
	err = s.recoverySender.Send(ctx, notifyport.Message{
		EventType: "order.access_code", Channel: "email", Recipient: email, Locale: string(locale),
		Subject: subject, Body: body, BizType: "order_access_recovery",
	})
	if err != nil {
		_ = data.Client(ctx, s.uc.Data).EmailVerification.UpdateOneID(verification.ID).SetExpiresAt(time.Now()).Exec(ctx)
		return nil, errors.BadRequest("order.ACCESS_EMAIL_FAILED", i18n.Message(ctx, "order.access_email_failed", "验证码邮件暂未送达，请稍后重试或联系客服"))
	}
	return &emptypb.Empty{}, nil
}

func (s *StoreOrderService) RecoverOrderAccess(ctx context.Context, req *storefrontv1.OrderAccessRecoveryRequest) (*storefrontv1.OrderAccessReply, error) {
	email, err := orderaccess.EmailAddress(req.Email)
	if err != nil || req.OrderNo == "" || len(req.OrderNo) > 100 || len(req.Code) != 6 || s.uc.AccessCipher == nil {
		return nil, recoveryInvalid(ctx)
	}
	site := tenancy.FromContext(ctx).SubsiteID
	scope := recoveryScope(site, req.OrderNo, email)
	var token string
	err = data.Tx(ctx, s.uc.Data, func(txCtx context.Context) error {
		c := data.Client(txCtx, s.uc.Data)
		o, e := c.Order.Query().Where(entorder.OrderNo(req.OrderNo), entorder.SubsiteID(site)).Only(txCtx)
		if ent.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !recoveryOrderMatches(o, email) {
			return nil
		}
		v, e := c.EmailVerification.Query().Where(emailverification.Email(scope), emailverification.PurposeEQ(emailverification.PurposeOrderAccess)).Order(ent.Desc(emailverification.FieldCreatedAt), ent.Desc(emailverification.FieldID)).First(txCtx)
		if ent.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !v.VerifiedAt.IsZero() || !v.ExpiresAt.After(time.Now()) || v.AttemptCount >= orderAccessCodeMaxTries {
			return nil
		}
		n, e := c.Order.Update().Where(entorder.ID(o.ID), entorder.Version(o.Version)).AddVersion(1).Save(txCtx)
		if e != nil {
			return e
		}
		if n != 1 {
			return nil
		}
		if subtle.ConstantTimeCompare([]byte(v.CodeHash), []byte(s.recoveryCodeHash(scope, req.Code))) != 1 {
			return c.EmailVerification.UpdateOneID(v.ID).AddAttemptCount(1).Exec(txCtx)
		}
		n, e = c.EmailVerification.Update().Where(emailverification.ID(v.ID), emailverification.VerifiedAtIsNil(), emailverification.AttemptCountLT(orderAccessCodeMaxTries), emailverification.ExpiresAtGT(time.Now())).SetVerifiedAt(time.Now()).Save(txCtx)
		if e != nil {
			return e
		}
		if n != 1 {
			return nil
		}
		token, e = orderaccess.NewToken()
		if e != nil {
			return e
		}
		// Initial checkout replay cannot re-issue a newly recovered token to an old session.
		return c.Order.UpdateOneID(o.ID).SetOrderAccessTokenHash(orderaccess.TokenHash(site, o.OrderNo, token)).SetOrderAccessTokenSecret([]byte{}).Exec(txCtx)
	})
	if err != nil {
		return nil, errors.InternalServer("order.ACCESS_RECOVERY_UNAVAILABLE", i18n.Message(ctx, "order.access_recovery_unavailable", "订单邮件验证暂时不可用，请稍后重试"))
	}
	if token == "" {
		return nil, recoveryInvalid(ctx)
	}
	return &storefrontv1.OrderAccessReply{OrderAccessToken: token}, nil
}
