package adapter

// 虎皮椒 API 1.1: https://www.xunhupay.com/doc/api/pay.html
// APPID 决定微信/支付宝渠道；plugins 只是对接程序标识，不是支付类型。
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/currencyunit"
	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

type xunhupayConfig struct {
	AppID     string `json:"appid"`
	AppSecret string `json:"appsecret"`
	APIURL    string `json:"api_url"`
}

type XunhupayAdapter struct{ client *http.Client }

func NewXunhupay() *XunhupayAdapter {
	return &XunhupayAdapter{client: httpx.NewSafeClient(30 * time.Second)}
}

func (*XunhupayAdapter) Type() string       { return "xunhupay" }
func (*XunhupayAdapter) SuccessAck() string { return "success" }
func (*XunhupayAdapter) Meta() port.DriverMeta {
	return port.DriverMeta{Name: "虎皮椒支付", Icon: "xunhupay", Description: "虎皮椒微信 / 支付宝收款；不同 APPID 分别添加渠道，电脑扫码、手机跳转支付"}
}
func (*XunhupayAdapter) ConfigFields() []port.ConfigField {
	return []port.ConfigField{
		{Key: "appid", Label: "APPID", Type: "text", Required: true, Help: "虎皮椒后台的支付渠道 APPID。微信和支付宝请分别添加渠道并填写各自凭据。"},
		{Key: "appsecret", Label: "APPSECRET", Type: "password", Required: true, Sensitive: true, Help: "对应 APPID 的密钥，用于下单签名及回调验签"},
		{Key: "api_url", Label: "支付网关", Type: "text", Default: "https://api.xunhupay.com/payment/do.html", Placeholder: "留空使用官方网关；也可填写虎皮椒提供的完整下单地址"},
	}
}

func xunhupayHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func parseXunhupayConfig(raw json.RawMessage) (xunhupayConfig, error) {
	var c xunhupayConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("xunhupay: 凭据格式错误")
	}
	c.AppID, c.AppSecret, c.APIURL = strings.TrimSpace(c.AppID), strings.TrimSpace(c.AppSecret), strings.TrimSpace(c.APIURL)
	if c.AppID == "" || c.AppSecret == "" {
		return c, fmt.Errorf("xunhupay: appid/appsecret 必填")
	}
	if c.APIURL == "" {
		c.APIURL = "https://api.xunhupay.com/payment/do.html"
	}
	if !xunhupayHTTPURL(c.APIURL) {
		return c, fmt.Errorf("xunhupay: 支付网关须为完整 HTTP/HTTPS 地址")
	}
	u, _ := url.Parse(c.APIURL)
	if u.RawQuery != "" {
		return c, fmt.Errorf("xunhupay: 支付网关不能包含查询参数")
	}
	if u.Path == "" || u.Path == "/" {
		c.APIURL = strings.TrimRight(c.APIURL, "/") + "/payment/do.html"
	}
	return c, nil
}

func (*XunhupayAdapter) ValidateConfig(raw json.RawMessage) error {
	_, err := parseXunhupayConfig(raw)
	return err
}

var xunhupayOrderPattern = regexp.MustCompile(`^[a-zA-Z0-9_*-]{1,32}$`)

func (a *XunhupayAdapter) CreatePayment(ctx context.Context, req port.CreatePaymentRequest) (*port.RedirectInfo, error) {
	c, err := parseXunhupayConfig(req.Config)
	if err != nil {
		return nil, err
	}
	if req.Amount <= 0 || !money.ValidCents(int64(req.Amount)) || req.ChargedUnits != 0 || (req.ChargedCurrency != "" && req.ChargedCurrency != "CNY") {
		return nil, fmt.Errorf("xunhupay: 仅支持人民币正数金额")
	}
	ref := firstNonEmpty(req.GatewayOrderRef, req.OrderNo)
	if !xunhupayOrderPattern.MatchString(ref) {
		return nil, fmt.Errorf("xunhupay: 商户订单号格式错误")
	}
	if !xunhupayHTTPURL(req.NotifyBaseURL) || (req.ReturnURL != "" && !xunhupayHTTPURL(req.ReturnURL)) {
		return nil, fmt.Errorf("xunhupay: 请配置可公网访问的站点地址")
	}
	// 标题使用稳定支付单号，避免商品名称中的表情、百分号、长度触发网关限制。
	params := map[string]string{
		"version": "1.1", "appid": c.AppID, "trade_order_id": ref,
		"total_fee": centsToYuan(int64(req.Amount)), "title": "订单 " + ref,
		"time": strconv.FormatInt(time.Now().Unix(), 10), "nonce_str": nonceStr(32),
		"notify_url": req.NotifyBaseURL, "return_url": req.ReturnURL, "plugins": "zcard-next",
	}
	params["hash"] = md5Hex(sortParams(params, "hash") + c.AppSecret)
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := httpx.ValidateURL(c.APIURL); err != nil {
		return nil, fmt.Errorf("xunhupay: 网关地址不可用: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("xunhupay: 无法创建支付请求")
	}
	hreq.Header.Set("Content-Type", "application/json; charset=utf-8")
	hreq.Header.Set("User-Agent", httpx.UserAgent)
	site, _ := url.Parse(req.NotifyBaseURL)
	hreq.Header.Set("Referer", site.Scheme+"://"+site.Host+"/")
	resp, err := a.client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("xunhupay: 网关请求失败，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xunhupay: 网关 HTTP 状态 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("xunhupay: 网关响应读取失败")
	}
	result, err := xunhupayResponse(raw)
	if err != nil {
		return nil, err
	}
	if result["errcode"] == "" {
		return nil, fmt.Errorf("xunhupay: 网关响应缺少 errcode")
	}
	if result["errcode"] != "0" {
		return nil, fmt.Errorf("xunhupay: 下单失败（错误码 %s），请到虎皮椒后台核对配置及订单", result["errcode"])
	}
	if !constantTimeEq(result["hash"], md5Hex(sortParams(result, "hash")+c.AppSecret)) {
		return nil, fmt.Errorf("xunhupay: 下单响应验签失败")
	}
	// url_qrcode 已经是图片，不能把图片网址再次编码成二维码。
	if !xunhupayHTTPURL(result["url_qrcode"]) || !xunhupayHTTPURL(result["url"]) {
		return nil, fmt.Errorf("xunhupay: 网关未返回有效二维码图片或手机支付地址")
	}
	return &port.RedirectInfo{
		Type: "qrcode", Deadline: time.Now().Add(5 * time.Minute),
		Payload: jsonMust(map[string]string{"code_url": result["url_qrcode"], "mobile_url": result["url"]}),
	}, nil
}

// Preserve JSON number spelling, including errcode=0 (PHP does not skip zero).
// The documented payment response is flat; nested/non-scalar values are rejected.
func xunhupayResponse(raw []byte) (map[string]string, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("xunhupay: 网关响应格式错误")
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if string(value) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(value, &s) == nil {
			result[key] = s
			continue
		}
		var n json.Number
		if json.Unmarshal(value, &n) != nil {
			return nil, fmt.Errorf("xunhupay: 网关响应字段格式错误")
		}
		result[key] = n.String()
	}
	return result, nil
}

func (*XunhupayAdapter) VerifyCallback(form map[string]string, raw json.RawMessage) (*port.CallbackFact, error) {
	c, err := parseXunhupayConfig(raw)
	if err != nil {
		return nil, err
	}
	if form["hash"] == "" || !constantTimeEq(form["hash"], md5Hex(sortParams(form, "hash")+c.AppSecret)) {
		return nil, fmt.Errorf("xunhupay: 回调验签失败")
	}
	// Older official PHP demos omit appid. If supplied, it must match this channel.
	if form["appid"] != "" && form["appid"] != c.AppID {
		return nil, fmt.Errorf("xunhupay: 回调 APPID 不匹配")
	}
	if !xunhupayOrderPattern.MatchString(form["trade_order_id"]) || strings.TrimSpace(form["transaction_id"]) == "" {
		return nil, fmt.Errorf("xunhupay: 回调缺少有效订单号")
	}
	amount, err := currencyunit.ParseChargeAmount(form["total_fee"], currencyunit.ChargeUnit{Precision: 2, Step: 1})
	if err != nil || amount <= 0 || !money.ValidCents(amount) {
		return nil, fmt.Errorf("xunhupay: 回调金额无效")
	}
	return &port.CallbackFact{
		Provider: "xunhupay", OrderNo: form["trade_order_id"], GatewayOrderRef: form["trade_order_id"],
		ChannelOrderNo: form["transaction_id"], Amount: money.Cents(amount), Currency: "CNY",
		Success: form["status"] == "OD", Raw: jsonMust(form),
	}, nil
}
