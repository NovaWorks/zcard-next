package notify

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http/httptest"
	"net/smtp"
	"net/textproto"
	"strings"
	"testing"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type smtpFixtureOptions struct {
	security, auth, reject string
	advertiseTLS           bool
}
type smtpExchange struct {
	commands                      []string
	tls                           bool
	username, password, body, err string
}

func smtpModeFixture(t *testing.T, options smtpFixtureOptions) (int, <-chan smtpExchange) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan smtpExchange, 1)
	go func() {
		capture := smtpExchange{}
		defer func() { done <- capture }()
		conn, err := ln.Accept()
		if err != nil {
			capture.err = err.Error()
			return
		}
		raw := conn
		defer raw.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		upgrade := func() bool {
			secured := tls.Server(conn, tlsConfig)
			if err := secured.Handshake(); err != nil {
				capture.err = err.Error()
				return false
			}
			conn = secured
			capture.tls = true
			return true
		}
		if options.security == "tls" && !upgrade() {
			return
		}
		reader := textproto.NewReader(bufio.NewReader(conn))
		fmt.Fprint(conn, "220 fixture ESMTP ready\r\n")
		loginStep := 0
		cramPending := false
		for {
			line, err := reader.ReadLine()
			if err != nil {
				capture.err = err.Error()
				return
			}
			capture.commands = append(capture.commands, line)
			if loginStep > 0 {
				decoded, err := base64.StdEncoding.DecodeString(line)
				if err != nil {
					capture.err = err.Error()
					return
				}
				if loginStep == 1 {
					capture.username = string(decoded)
					loginStep = 2
					fmt.Fprint(conn, "334 UGFzc3dvcmQ6\r\n")
				} else {
					capture.password = string(decoded)
					loginStep = 0
					fmt.Fprint(conn, "235 authenticated\r\n")
				}
				continue
			}
			if cramPending {
				decoded, _ := base64.StdEncoding.DecodeString(line)
				capture.password = string(decoded)
				cramPending = false
				fmt.Fprint(conn, "235 authenticated\r\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(conn, "250-fixture\r\n")
				if options.advertiseTLS && !capture.tls {
					fmt.Fprint(conn, "250-STARTTLS\r\n")
				}
				if options.auth != "" {
					fmt.Fprintf(conn, "250-AUTH %s\r\n", options.auth)
				}
				fmt.Fprint(conn, "250 SIZE 100000\r\n")
			case line == "STARTTLS":
				fmt.Fprint(conn, "220 upgrade\r\n")
				if !upgrade() {
					return
				}
				reader = textproto.NewReader(bufio.NewReader(conn))
			case line == "AUTH LOGIN":
				loginStep = 1
				fmt.Fprint(conn, "334 VXNlcm5hbWU6\r\n")
			case line == "AUTH CRAM-MD5":
				cramPending = true
				fmt.Fprintf(conn, "334 %s\r\n", base64.StdEncoding.EncodeToString([]byte("fixture-challenge")))
			case strings.HasPrefix(line, "AUTH PLAIN "):
				decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				parts := strings.Split(string(decoded), "\x00")
				if len(parts) == 3 {
					capture.username, capture.password = parts[1], parts[2]
				}
				if options.reject == "auth" {
					fmt.Fprint(conn, "535 login denied fixture-secret\r\n")
				} else {
					fmt.Fprint(conn, "235 authenticated\r\n")
				}
			case strings.HasPrefix(line, "MAIL FROM:") && options.reject == "from":
				fmt.Fprint(conn, "550 sender not allowed\r\n")
			case strings.HasPrefix(line, "RCPT TO:") && options.reject == "recipient":
				fmt.Fprint(conn, "550 recipient rejected\r\n")
			case line == "DATA":
				if options.reject == "data" {
					fmt.Fprint(conn, "451 temporarily unavailable\r\n")
					continue
				}
				fmt.Fprint(conn, "354 send body\r\n")
				body, err := reader.ReadDotBytes()
				if err != nil {
					capture.err = err.Error()
					return
				}
				capture.body = string(body)
				if options.reject == "accept" {
					fmt.Fprint(conn, "552 message rejected\r\n")
				} else {
					fmt.Fprint(conn, "250 accepted\r\n")
				}
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

func smtpModeSettings(port int, mode, auth string, verify bool) fakeSettings {
	values := map[string]any{"smtp_host": "127.0.0.1", "smtp_port": port, "smtp_security": mode, "smtp_auth": auth, "smtp_tls_verify": verify, "smtp_user": "sender@example.com", "smtp_password": "fixture-secret"}
	settings := fakeSettings{values: map[string]string{}}
	for key, value := range values {
		raw, _ := json.Marshal(value)
		settings.values["notify."+key] = string(raw)
	}
	return settings
}

func TestSMTPConnectionAndAuthenticationModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode, auth, serverSecurity, serverAuth string
		advertiseTLS, expectTLS                      bool
	}{
		{"plain login", "plain", "login", "plain", "LOGIN", true, false},
		{"plain auto chooses login", "plain", "auto", "plain", "LOGIN", false, false},
		{"plain plain auth", "plain", "plain", "plain", "PLAIN", false, false},
		{"tls custom port", "tls", "auto", "tls", "PLAIN LOGIN", false, true},
		{"starttls login", "starttls", "auto", "plain", "LOGIN", true, true},
		{"auto upgrades", "auto", "auto", "plain", "PLAIN", true, true},
		{"no auth relay", "plain", "none", "plain", "", false, false},
		{"cram md5", "plain", "cram_md5", "plain", "CRAM-MD5", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, done := smtpModeFixture(t, smtpFixtureOptions{security: tc.serverSecurity, auth: tc.serverAuth, advertiseTLS: tc.advertiseTLS})
			ch := NewEmailChannel(smtpModeSettings(port, tc.mode, tc.auth, false))
			if err := ch.Deliver(context.Background(), notifyport.Message{Recipient: "recipient@example.com", Subject: "test", Body: "body"}); err != nil {
				t.Fatal(err)
			}
			exchange := <-done
			if exchange.err != "" || exchange.tls != tc.expectTLS || !strings.Contains(exchange.body, "body") {
				t.Fatal(exchange)
			}
			if tc.auth == "none" {
				if strings.Contains(strings.Join(exchange.commands, "\n"), "AUTH") {
					t.Fatal("relay unexpectedly authenticated")
				}
			} else if tc.auth == "cram_md5" {
				digest := hmac.New(md5.New, []byte("fixture-secret"))
				digest.Write([]byte("fixture-challenge"))
				expected := "sender@example.com " + hex.EncodeToString(digest.Sum(nil))
				if exchange.password != expected {
					t.Fatal("CRAM-MD5 response incorrect")
				}
			} else if exchange.username != "sender@example.com" || exchange.password != "fixture-secret" {
				t.Fatal("incorrect credentials")
			}
		})
	}
}

func TestSMTPDiagnosticStagesAndHTTPMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, mode, serverSecurity, reject, stage string
		advertiseTLS, verify                      bool
		code                                      int
	}{
		{"tls certificate", "tls", "tls", "", "tls", false, true, 0},
		{"starttls certificate", "starttls", "plain", "", "starttls", true, true, 0},
		{"starttls required", "starttls", "plain", "", "starttls", false, false, 0},
		{"login rejected", "plain", "plain", "auth", "auth", false, false, 535},
		{"sender rejected", "plain", "plain", "from", "from", false, false, 550},
		{"recipient rejected", "plain", "plain", "recipient", "recipient", false, false, 550},
		{"data rejected", "plain", "plain", "data", "data", false, false, 451},
		{"submission rejected", "plain", "plain", "accept", "accept", false, false, 552},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, done := smtpModeFixture(t, smtpFixtureOptions{security: tc.serverSecurity, auth: "PLAIN", reject: tc.reject, advertiseTLS: tc.advertiseTLS})
			repo := newNotifyRepo(t)
			ch := NewEmailChannel(smtpModeSettings(port, tc.mode, "auto", tc.verify))
			svc := NewAdminNotifyService(repo, NewDispatcher(repo, ch), nil)
			_, err := svc.TestEmail(context.Background(), &adminv1.TestEmailRequest{Recipient: "recipient@example.com", Subject: "test", Content: "body"})
			if err == nil {
				t.Fatal("failure reported success")
			}
			<-done
			diagnostic := kerrors.FromError(err)
			if diagnostic.Metadata["stage"] != tc.stage || diagnostic.Metadata["smtp_code"] != fmt.Sprint(tc.code) || diagnostic.Metadata["hint"] == "" {
				t.Fatal(diagnostic)
			}
			if tc.verify && !strings.Contains(diagnostic.Metadata["hint"], "证书") {
				t.Fatal("certificate advice missing")
			}
			// Use the real HTTP error encoder: metadata must survive on the wire.
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/api/v1/admin/notify/email/test", nil)
			khttp.DefaultErrorEncoder(response, request, err)
			var payload struct {
				Message  string            `json:"message"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Metadata["stage"] != tc.stage || strings.Contains(response.Body.String(), "fixture-secret") {
				t.Fatal("HTTP diagnostics missing or secret leaked", response.Body.String())
			}
		})
	}
}

func TestSMTPAutoDoesNotExposePasswordOnPlaintextRemote(t *testing.T) {
	auth := &smtpPasswordAuth{method: "LOGIN", username: "sender", password: "secret", host: "remote.example.com"}
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "remote.example.com"}); err == nil {
		t.Fatal("automatic mode allowed remote plaintext login")
	}
	auth.allowPlaintext = true
	if _, _, err := auth.Start(&smtp.ServerInfo{Name: "remote.example.com"}); err != nil {
		t.Fatal("explicit plain mode rejected", err)
	}
}
