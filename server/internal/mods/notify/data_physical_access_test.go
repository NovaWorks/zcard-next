package notify

import (
	"context"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"strings"
	"testing"
)

func TestPhysicalReceiptDoesNotUseCardTemplate(t *testing.T) {
	r := newNotifyRepo(t)
	ctx := context.Background()
	if _, err := r.UpsertTemplate(ctx, "order.delivered", "inbox", "zh_CN", "卡密已发货", "请取货 {{.fetch_url}}", true); err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(r, NewInboxChannel(r))
	if err := d.HandleEvent(ctx, testEnvelope("order.delivered", []byte(`{"order_no":"PHYSICAL","user_id":7,"order_id":1,"goods_type":"physical"}`))); err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.ListLogs(ctx, "sent", "order.delivered", 1, 10)
	if err != nil || len(rows) != 1 {
		t.Fatal("receipt notification missing", err)
	}
	if strings.Contains(rows[0].Body, "取货") || !strings.Contains(rows[0].Subject, "确认收货") {
		t.Fatal("physical receipt used virtual delivery wording")
	}
	if err := d.HandleEvent(ctx, testEnvelope("order.delivered", []byte(`{"order_no":"DIGITAL","user_id":7,"order_id":2}`))); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = r.ListLogs(ctx, "sent", "order.delivered", 1, 10)
	found := false
	for _, row := range rows {
		if strings.Contains(row.Subject, "卡密") {
			found = true
		}
	}
	if !found {
		t.Fatal("virtual card template was altered")
	}
}

func TestOrderRecoveryCodeIsAbsentFromNotificationLogs(t *testing.T) {
	for _, kind := range []string{"password_reset", "register_code", "order_access_recovery"} {
		log := logOf(notifyport.Message{BizType: kind, Body: "secret 012345", Variables: map[string]string{"code": "012345"}}, "sent", "")
		if strings.Contains(log.Body, "012345") || log.Variables["code"] != "" {
			t.Fatalf("verification code logged for %s", kind)
		}
	}
}
