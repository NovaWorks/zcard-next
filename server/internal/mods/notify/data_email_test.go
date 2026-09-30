package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/notificationlog"
	"github.com/NovaWorks/zcard-next/server/internal/mods/identity"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

// A real TCP SMTP fixture verifies commands, authentication and DATA, rather
// than substituting the channel that registration and the test endpoint use.
type smtpCapture struct {
	commands  []string
	body, err string
}

func smtpFixture(t *testing.T, rejectAuth bool) (int, <-chan smtpCapture) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan smtpCapture, 1)
	go func() {
		capture := smtpCapture{}
		defer func() { done <- capture }()
		conn, err := ln.Accept()
		if err != nil {
			capture.err = err.Error()
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := textproto.NewReader(bufio.NewReader(conn))
		fmt.Fprint(conn, "220 fixture ESMTP ready\r\n")
		for {
			line, err := reader.ReadLine()
			if err != nil {
				capture.err = err.Error()
				return
			}
			capture.commands = append(capture.commands, line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-fixture\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH PLAIN "):
				if rejectAuth {
					fmt.Fprint(conn, "535 rejected fixture-secret\r\n")
				} else {
					fmt.Fprint(conn, "235 authenticated\r\n")
				}
			case line == "DATA":
				fmt.Fprint(conn, "354 send body\r\n")
				body, err := reader.ReadDotBytes()
				if err != nil {
					capture.err = err.Error()
					return
				}
				capture.body = string(body)
				fmt.Fprint(conn, "250 accepted\r\n")
			case line == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 ok\r\n")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, done
}

func TestEmailSavedAdminConfigAndTestSend(t *testing.T) {
	ctx := context.Background()
	repo := newNotifyRepo(t)
	settingsRepo := settings.NewRepoImpl(repo.data)
	svc := settings.NewAdminSettingsService(settings.NewSettingsUsecase(settingsRepo))
	reader := ProvideSettingsReader(settingsRepo)
	email := NewEmailChannel(reader)
	if email.Ready(ctx) {
		t.Fatal("unconfigured email is ready")
	}
	port, done := smtpFixture(t, false)
	values := map[string]any{"smtp_host": "127.0.0.1", "smtp_port": port, "smtp_user": "sender@example.com", "smtp_password": "fixture-secret", "smtp_name": "中文发件人"}
	request := &adminv1.UpdateSettingsRequest{}
	for key, value := range values {
		raw, _ := json.Marshal(value)
		request.Items = append(request.Items, &adminv1.SettingUpdate{Group: "notify", Key: key, ValueJson: string(raw)})
	}
	if _, err := svc.UpdateSettings(ctx, request); err != nil {
		t.Fatal(err)
	}
	if !email.Ready(ctx) {
		t.Fatal("saved form config did not make email ready without restart")
	}
	// The masked password sent back by the form must retain the SMTP secret.
	if _, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "notify", Key: "smtp_password", ValueJson: `"****"`}); err != nil {
		t.Fatal(err)
	}
	admin := NewAdminNotifyService(repo, NewDispatcher(repo, email), nil)
	req := &adminv1.TestEmailRequest{Recipient: "recipient@example.com", Subject: "中文标题", Content: "第一行\n<script>测试</script>"}
	if _, err := admin.TestEmail(ctx, req); err != nil {
		t.Fatal(err)
	}
	capture := <-done
	if capture.err != "" {
		t.Fatal(capture.err)
	}
	commands := strings.Join(capture.commands, "\n")
	if !strings.Contains(commands, "MAIL FROM:<sender@example.com>") {
		t.Fatal("display name entered SMTP envelope", commands)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("\x00sender@example.com\x00fixture-secret"))
	if !strings.Contains(commands, "AUTH PLAIN "+auth) {
		t.Fatal("saved password not used", commands)
	}
	message, err := mail.ReadMessage(strings.NewReader(capture.body))
	if err != nil {
		t.Fatal(err)
	}
	from, err := mail.ParseAddress(message.Header.Get("From"))
	if err != nil || from.Name != "中文发件人" {
		t.Fatal(from, err)
	}
	if !strings.Contains(capture.body, "&lt;script&gt;") || strings.Contains(capture.body, "<script>") {
		t.Fatal("test text treated as HTML", capture.body)
	}
	if message.Header.Get("Subject") != mimeEncode(req.Subject) {
		t.Fatal("subject encoding", message.Header)
	}
	row := repo.data.Client.NotificationLog.Query().Where(notificationlog.EventType("email.test")).OnlyX(ctx)
	if row.Status != notificationlog.StatusSent || row.Recipient != req.Recipient {
		t.Fatal("test delivery not logged", row)
	}
	// A later save immediately changes config and supports login names which are
	// separate from the envelope sender.
	if _, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "notify", Key: "smtp_from", ValueJson: `"other@example.com"`}); err != nil {
		t.Fatal(err)
	}
	cfg, err := email.smtpConfig(ctx)
	if err != nil || cfg.From != "other@example.com" {
		t.Fatal(cfg, err)
	}
	// Registration uses the same persisted settings and must no longer fail its
	// ChannelReady preflight after the admin form has saved SMTP fields.
	registerPort, registerDone := smtpFixture(t, false)
	if _, err := svc.UpdateSetting(ctx, &adminv1.UpdateSettingRequest{Group: "notify", Key: "smtp_port", ValueJson: fmt.Sprint(registerPort)}); err != nil {
		t.Fatal(err)
	}
	registration := identity.NewRegisterCodeService(repo.data, NewDispatcher(repo, email))
	if err := registration.SendRegisterCode(ctx, "register@example.com", "email"); err != nil {
		t.Fatal(err)
	}
	registered := <-registerDone
	if registered.err != "" || !strings.Contains(registered.body, "注册验证码") {
		t.Fatal("registration mail not submitted", registered.err)
	}

}

func TestEmailConfigCompatibility(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		ready  bool
		from   string
	}{
		{"legacy", map[string]string{"notify.smtp": `{"enabled":true,"host":"smtp.example.com","port":465,"from":"legacy@example.com"}`}, true, "legacy@example.com"},
		{"flat overrides legacy", map[string]string{"notify.smtp_host": `"smtp.example.com"`, "notify.smtp_user": `"new@example.com"`, "notify.smtp": `{"enabled":false,"host":"old","from":"old@example.com"}`}, true, "new@example.com"},
		{"cleared host disables legacy", map[string]string{"notify.smtp_host": `""`, "notify.smtp": `{"enabled":true,"host":"old","from":"old@example.com"}`}, false, ""},
		{"invalid sender", map[string]string{"notify.smtp_host": `"smtp.example.com"`, "notify.smtp_user": `"login"`}, false, "login"},
		{"separate sender", map[string]string{"notify.smtp_host": `"smtp.example.com"`, "notify.smtp_user": `"login"`, "notify.smtp_from": `"sender@example.com"`}, true, "sender@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := NewEmailChannel(fakeSettings{values: tc.values})
			if ch.Ready(context.Background()) != tc.ready {
				t.Fatal("readiness mismatch")
			}
			cfg, err := ch.smtpConfig(context.Background())
			if err != nil || cfg.From != tc.from {
				t.Fatal(cfg, err)
			}
		})
	}
}

func TestEmailTestRejectsSkippedInvalidAndSMTPFailure(t *testing.T) {
	ctx := context.Background()
	repo := newNotifyRepo(t)
	svc := NewAdminNotifyService(repo, NewDispatcher(repo, NewEmailChannel(fakeSettings{})), nil)
	req := &adminv1.TestEmailRequest{Recipient: "recipient@example.com", Subject: "test", Content: "body"}
	if _, err := svc.TestEmail(ctx, req); kerrors.Reason(err) != "notify.EMAIL_NOT_READY" {
		t.Fatal("skipped delivery reported success", err)
	}
	for _, invalid := range []*adminv1.TestEmailRequest{
		{Recipient: "a@example.com\r\nBcc: b@example.com", Subject: "test", Content: "body"},
		{Recipient: req.Recipient, Subject: "test\nBcc: b@example.com", Content: "body"},
		{Recipient: req.Recipient, Subject: "test", Content: ""},
	} {
		if _, err := svc.TestEmail(ctx, invalid); err == nil {
			t.Fatal("invalid test request accepted")
		}
	}
	port, done := smtpFixture(t, true)
	ch := NewEmailChannel(fakeSettings{values: map[string]string{"notify.smtp_host": `"127.0.0.1"`, "notify.smtp_port": fmt.Sprint(port), "notify.smtp_user": `"sender@example.com"`, "notify.smtp_password": `"fixture-secret"`}})
	svc.disp = NewDispatcher(repo, ch)
	_, err := svc.TestEmail(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "SMTP 认证失败") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("SMTP failure not reported/redacted", err)
	}
	<-done
	row := repo.data.Client.NotificationLog.Query().Where(notificationlog.StatusEQ(notificationlog.StatusFailed)).OnlyX(ctx)
	if strings.Contains(row.ErrorMessage, "fixture-secret") {
		t.Fatal("password leaked to logs")
	}
}

func TestEmailCancellationClosesSMTPConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	closed := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		closed <- err
	}()
	ch := NewEmailChannel(fakeSettings{values: map[string]string{"notify.smtp_host": `"127.0.0.1"`, "notify.smtp_port": fmt.Sprint(ln.Addr().(*net.TCPAddr).Port), "notify.smtp_user": `"sender@example.com"`}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := ch.Deliver(ctx, notifyport.Message{Recipient: "recipient@example.com"}); err == nil {
		t.Fatal("unresponsive server reported success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("context cancellation ignored")
	}
	if err := <-closed; err == nil {
		t.Fatal("connection remained open")
	}
}
