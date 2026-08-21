package procurement

// P2-10 E：上游回调接收端点（采购三通道之回调通道的接收侧）。
//
// POST /api/v1/procurement/callback?connection_id=N
//   - 不挂 JWT（架构测试规则 9）；connection_id 经 query 定位连接 → 按驱动验签
//     （zcard 四头 / dujiao 三头；acg 无回调）
//   - 验签/查单失败统一 401 防枚举（1.x 纪律：不区分原因防探测）
//   - 回填走 confirmResult（与轮询/巡检幂等汇聚——状态机 CAS，先到先终态）
//   - zcard 回调不携带卡密（确认后转 PollOne 查单获取）；dujiao 事件带 payload

import (
	"context"
	"io"
	"net/http"
	"strconv"

	khttp "github.com/go-kratos/kratos/v3/transport/http"

	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
)

// UpstreamCallbackPath 回调接收路径（Gateway.Submit 登记给上游的是连接配置的
// callback_url——运营应填 <本站公网地址>/api/v1/procurement/callback）。
const UpstreamCallbackPath = "/api/v1/procurement/callback"

// RegisterUpstreamCallback 挂载回调接收路由（须在 SPA 兜底前注册）。
func RegisterUpstreamCallback(srv *khttp.Server, svc *ProcureService, gw supplyport.UpstreamGateway) {
	srv.HandleFunc(UpstreamCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		connID, err := strconv.ParseUint(r.URL.Query().Get("connection_id"), 10, 64)
		if err != nil || connID == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		headers := map[string]string{
			"X-Supply-Key":         r.Header.Get("X-Supply-Key"),
			"X-Supply-Timestamp":   r.Header.Get("X-Supply-Timestamp"),
			"X-Supply-Nonce":       r.Header.Get("X-Supply-Nonce"),
			"X-Supply-Signature":   r.Header.Get("X-Supply-Signature"),
			"Dujiao-Next-Api-Key":  r.Header.Get("Dujiao-Next-Api-Key"),
			"Dujiao-Next-Timestamp": r.Header.Get("Dujiao-Next-Timestamp"),
			"Dujiao-Next-Signature": r.Header.Get("Dujiao-Next-Signature"),
		}
		result, err := gw.VerifyUpstreamCallback(r.Context(), connID, &supplyport.UpstreamCallbackAuth{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Headers: headers, Body: body,
		})
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized) // 防枚举：不区分验签失败原因
			return
		}
		po, err := svc.repo.GetByDownstreamOrderNo(r.Context(), result.DownstreamOrderNo)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := svc.HandleUpstreamCallback(r.Context(), po.ID, result); err != nil {
			svc.log.Warn("procurement.callback_handle_failed", "po_id", po.ID, "err", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// HandleUpstreamCallback 回调结果回填：delivered 且带卡密 → 直接终态；
// 其余（确认型回调 / 无卡密）→ 转查单通道获取。
func (s *ProcureService) HandleUpstreamCallback(ctx context.Context, poID uint64, result *supplyport.UpstreamCallbackResult) error {
	if result.Status == "delivered" && len(result.Cards) > 0 {
		return s.confirmResult(ctx, poID, "delivered", result.Cards, result.Amount)
	}
	return s.PollOne(ctx, poID)
}
