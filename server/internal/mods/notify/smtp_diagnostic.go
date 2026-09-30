package notify

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"syscall"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	kerrors "github.com/go-kratos/kratos/v3/errors"
)

var smtpStageLabels = map[string]string{
	"configuration": "SMTP 配置检查失败", "connect": "SMTP 连接失败", "greeting": "SMTP 握手失败",
	"tls": "SMTP TLS 连接失败", "starttls": "SMTP STARTTLS 失败", "auth": "SMTP 认证失败",
	"from": "SMTP 发件地址被拒绝", "recipient": "SMTP 收件地址被拒绝",
	"data": "SMTP DATA 失败", "body": "SMTP 正文发送失败", "accept": "SMTP 服务器拒绝邮件",
}
var smtpStageHints = map[string]string{
	"configuration": "核对服务器地址（不含协议和端口）、端口、发件邮箱、连接与认证方式，然后保存配置。",
	"connect":       "检查服务器域名、端口、防火墙和服务器出站网络；确认邮件服务正在监听此端口。",
	"greeting":      "检查该端口是否为 SMTP 服务；SSL/TLS 通常使用 465，STARTTLS 通常使用 587，普通 SMTP 通常使用 25。",
	"tls":           "核对 SSL/TLS 方式和端口，并检查邮件服务器证书及其完整证书链。",
	"starttls":      "确认服务器及端口支持 STARTTLS；此方式要求升级成功，请勿使用只支持隐式 TLS 的端口。",
	"auth":          "核对 SMTP 用户名、密码或授权码、认证方式，以及服务器是否已开启 SMTP 登录。",
	"from":          "核对发件邮箱是否属于当前账号、发件域名是否获准，以及中继发信权限。",
	"recipient":     "核对收件地址、服务器中继策略和收件限制；部分服务商要求测试收件人已验证。",
	"data":          "检查服务器发信权限、限额和邮件大小限制，并查看邮件服务日志。",
	"body":          "检查网络是否中断或超时，再查看邮件服务器日志。",
	"accept":        "按服务器返回的状态码检查内容策略、发信限额、域名验证和邮件服务日志。",
}

// SMTPDiagnostic carries the failure stage and sanitized provider response.
// The test endpoint exposes these as Kratos error metadata for inline guidance.
type SMTPDiagnostic struct {
	Stage, Detail, Hint            string
	Code                           int
	Host, Security, Authentication string
	Port                           int
	TLSVerify                      bool
}

func (e *SMTPDiagnostic) Error() string { return smtpStageLabels[e.Stage] + ": " + e.Detail }

func smtpFailure(stage string, err error, cfg *notifyport.SMTPConfig) *SMTPDiagnostic {
	d := &SMTPDiagnostic{Stage: stage, Detail: err.Error(), Hint: smtpStageHints[stage]}
	var response *textproto.Error
	if errors.As(err, &response) {
		d.Code = response.Code
	}
	var dns *net.DNSError
	var network net.Error
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		d.Hint = "服务器域名无法解析；请检查 SMTP 主机名和服务器 DNS 配置。"
	case errors.As(err, &network) && network.Timeout():
		d.Hint = "连接或服务器响应超时；检查防火墙、出站端口，以及连接方式是否与端口匹配。"
	case errors.Is(err, syscall.ECONNREFUSED):
		d.Hint = "连接被拒绝；确认端口正确、邮件服务已启动，并允许服务器访问此端口。"
	case errors.As(err, &unknown):
		d.Hint = "证书不受信任（可能是自签名或缺少证书链）；为邮件服务安装可信证书及完整链。自建可信服务可自行选择是否校验证书。"
	case errors.As(err, &hostname):
		d.Hint = "证书中的域名与 SMTP 服务器地址不匹配；使用证书覆盖的主机名或更新证书。"
	case errors.As(err, &certificate):
		d.Hint = "证书已过期、尚未生效或用途不正确；检查服务器时间及邮件服务证书。"
	case errors.As(err, &record):
		d.Hint = "此端口返回的不是 TLS 握手；核对连接方式，隐式 SSL/TLS 与 STARTTLS 不能混用。"
	case d.Code == 530 || d.Code == 538:
		d.Hint = "服务器要求认证或加密；检查用户名和密码，并选择 STARTTLS 或 SSL/TLS。"
	case stage == "auth" && d.Code == 535:
		d.Hint = "服务器拒绝登录；检查账号、密码或 SMTP 专用授权码，以及是否启用了 SMTP 登录。"
	case d.Code == 421 || d.Code == 450 || d.Code == 451 || d.Code == 452:
		d.Hint = "服务器暂时不可用、限流或资源不足；查看服务日志与限额，稍后再试。"
	}
	if cfg != nil {
		d.Host, d.Port, d.Security, d.Authentication, d.TLSVerify = cfg.Host, cfg.Port, cfg.Security, cfg.Auth, cfg.TLSVerify
		if cfg.Password != "" {
			for _, secret := range []string{base64.StdEncoding.EncodeToString([]byte("\x00" + cfg.Username + "\x00" + cfg.Password)), base64.StdEncoding.EncodeToString([]byte(cfg.Password)), cfg.Password} {
				d.Detail = strings.ReplaceAll(d.Detail, secret, "[已隐藏]")
			}
		}
	}
	return d
}

func smtpTestError(err error) *kerrors.Error {
	if errors.Is(err, ErrSkipped) {
		return kerrors.BadRequest("notify.EMAIL_NOT_READY", "邮件通道未配置").WithMetadata(map[string]string{
			"stage": "configuration", "stage_label": smtpStageLabels["configuration"], "hint": "请先保存 SMTP 服务器、端口、连接方式和发件账号。",
		})
	}
	reply := kerrors.BadRequest("notify.EMAIL_TEST_FAILED", err.Error())
	var d *SMTPDiagnostic
	if errors.As(err, &d) {
		reply = reply.WithMetadata(map[string]string{
			"stage": d.Stage, "stage_label": smtpStageLabels[d.Stage], "hint": d.Hint, "smtp_code": fmt.Sprint(d.Code),
			"host": d.Host, "port": fmt.Sprint(d.Port), "security": d.Security, "authentication": d.Authentication, "tls_verify": fmt.Sprint(d.TLSVerify),
		})
	}
	return reply
}
