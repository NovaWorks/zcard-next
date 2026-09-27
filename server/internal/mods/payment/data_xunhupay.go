package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/mods/payment/port"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// Keep both gateway URLs cached. Select the presentation on every request so a
// payment resumed on another device doesn't reuse the first device's UI mode.
func paymentForDevice(ctx context.Context, driver string, info *port.RedirectInfo) (*port.RedirectInfo, error) {
	if driver != "xunhupay" || info == nil {
		return info, nil
	}
	if !info.Deadline.IsZero() && !time.Now().Before(info.Deadline) {
		return nil, fmt.Errorf("xunhupay: 支付二维码已过期，请更换支付方式或重新下单；已付款请等待到账通知")
	}
	if req, ok := khttp.RequestFromServerContext(ctx); ok {
		ua := strings.ToLower(req.UserAgent())
		if strings.Contains(ua, "mobile") || strings.Contains(ua, "android") || strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") {
			var payload struct {
				MobileURL string `json:"mobile_url"`
			}
			if err := json.Unmarshal(info.Payload, &payload); err != nil || payload.MobileURL == "" {
				return nil, fmt.Errorf("xunhupay: 缺少手机支付地址")
			}
			result := *info
			result.Type = "redirect"
			result.Payload, _ = json.Marshal(map[string]string{"url": payload.MobileURL})
			return &result, nil
		}
	}
	return info, nil
}
