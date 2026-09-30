package notify

import (
	"fmt"
	"net/smtp"
	"strings"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

func validSMTPMode(mode string) bool {
	return mode == "auto" || mode == "plain" || mode == "tls" || mode == "starttls"
}
func validSMTPAuth(auth string) bool {
	return auth == "auto" || auth == "plain" || auth == "login" || auth == "cram_md5" || auth == "none"
}

func smtpAuthentication(client *smtp.Client, cfg *notifyport.SMTPConfig) (smtp.Auth, error) {
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("SMTP 用户名或密码为空；免认证中继请选择无需认证")
	}
	ok, advertised := client.Extension("AUTH")
	if !ok {
		return nil, fmt.Errorf("服务器未宣告 AUTH 支持；请检查端口、连接方式，或确认是否为免认证中继")
	}
	mechanisms := strings.Fields(strings.ToUpper(advertised))
	supported := func(method string) bool {
		for _, value := range mechanisms {
			if value == method {
				return true
			}
		}
		return false
	}
	method := strings.ToUpper(cfg.Auth)
	if method == "CRAM_MD5" {
		method = "CRAM-MD5"
	}
	if cfg.Auth == "auto" {
		method = ""
		for _, candidate := range []string{"PLAIN", "LOGIN", "CRAM-MD5"} {
			if supported(candidate) {
				method = candidate
				break
			}
		}
	}
	if method == "" || !supported(method) {
		return nil, fmt.Errorf("所选认证方式不受支持；服务器提供：%s", advertised)
	}
	if method == "CRAM-MD5" {
		return smtp.CRAMMD5Auth(cfg.Username, cfg.Password), nil
	}
	return &smtpPasswordAuth{method: method, username: cfg.Username, password: cfg.Password, host: cfg.Host, allowPlaintext: cfg.Security == "plain"}, nil
}

// Plaintext credentials are allowed only by the explicit plain connection mode.
// Automatic mode retains the net/smtp policy for TLS or localhost connections.
type smtpPasswordAuth struct {
	method, username, password, host string
	allowPlaintext                   bool
	step                             int
}

func (a *smtpPasswordAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if server.Name != a.host {
		return "", nil, fmt.Errorf("SMTP 认证服务器名称不匹配")
	}
	local := server.Name == "localhost" || server.Name == "127.0.0.1" || server.Name == "::1"
	if !server.TLS && !local && !a.allowPlaintext {
		return "", nil, fmt.Errorf("服务器未提供加密连接，自动模式不会发送明文密码")
	}
	a.step = 0
	if a.method == "PLAIN" {
		return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
	}
	return "LOGIN", nil, nil
}
func (a *smtpPasswordAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if a.method == "LOGIN" {
		a.step++
		if a.step == 1 {
			return []byte(a.username), nil
		}
		if a.step == 2 {
			return []byte(a.password), nil
		}
	}
	return nil, fmt.Errorf("SMTP 认证返回了非预期的挑战")
}
