package supplier

// 下游回调转发重试：
// 交付完成 → downstream_callbacks pending → POST 下游 notify_url（签名同四头口径，
// 下游验我们）→ success / 失败退避重试（间隔表耗尽入死信，可手动重发）。
// CallbackUrlGuard：HTTPS 强制 + 私网段拒绝（httpx.ValidateURL + 连接期 IP 复核）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/downstreamcallback"
	"github.com/NovaWorks/zcard-next/server/internal/platform/httpx"
	"github.com/NovaWorks/zcard-next/server/internal/platform/queue"
)

// CallbackTaskType 回调转发任务类型（default 队列）。
const CallbackTaskType = "supplier.callback"

// 回调退避间隔（秒）：30s, 1m, 5m, 30m, 2h——耗尽入死信（admin 手动重发）。
var callbackIntervals = []int{30, 60, 300, 1800, 7200}

// EnqueueCallback 登记回调任务（交付完成后调用；有 Redis 入 default 队列，否则进程内）。
func (s *SupplyAPIService) EnqueueCallback(ctx context.Context, supplyOrderID uint64) {
	payload, _ := json.Marshal(map[string]uint64{"supply_order_id": supplyOrderID})
	if s.enq == nil || !s.enq.Enabled() {
		go func() {
			ctx2, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			_ = s.DeliverCallback(ctx2, supplyOrderID)
		}()
		return
	}
	_ = s.enq.Enqueue(ctx, queue.Task{
		Type:      CallbackTaskType,
		Payload:   payload,
		Queue:     queue.QueueDefault,
		DedupeKey: CallbackTaskType + ":" + strconv.FormatUint(supplyOrderID, 10),
	})
}

// RunCallbackTask 队列任务入口（payload {"supply_order_id": N}）。
func (s *SupplyAPIService) RunCallbackTask(ctx context.Context, payload []byte) error {
	var req struct {
		SupplyOrderID uint64 `json:"supply_order_id"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("supplier.callback: 解析载荷失败: %w", err)
	}
	if req.SupplyOrderID == 0 {
		return nil
	}
	return s.DeliverCallback(ctx, req.SupplyOrderID)
}

// DeliverCallback 执行一次回调转发（成功 → success；失败 → 退避/死信）。
func (s *SupplyAPIService) DeliverCallback(ctx context.Context, supplyOrderID uint64) error {
	cb, err := s.repo.GetCallbackByOrder(ctx, supplyOrderID)
	if err != nil {
		return err
	}
	if cb.CallbackStatus == downstreamcallback.CallbackStatusSuccess {
		return nil
	}
	o, err := s.repo.GetSupplyOrder(ctx, supplyOrderID)
	if err != nil {
		return err
	}
	// CallbackUrlGuard：http/https 均放行（下游可按环境选择）+ 私网拒绝
	// （SSRF 校验不变；配置错误不入死信重试，提示重配）
	if !strings.HasPrefix(cb.CallbackURL, "https://") && !strings.HasPrefix(cb.CallbackURL, "http://") {
		_ = s.repo.MarkCallbackResult(ctx, cb.ID, false, "回调地址必须以 http:// 或 https:// 开头")
		return nil
	}
	if err := httpx.ValidateURL(cb.CallbackURL); err != nil {
		_ = s.repo.MarkCallbackResult(ctx, cb.ID, false, "SSRF 拦截: "+err.Error())
		return nil
	}

	// 回调签名按账号协议分支（ B：dujiao 兼容账号发 dujiao 事件格式 +
	// 3 头签名——签名 path 固定为协议常量 /api/v1/upstream/callback，非实际 URL）
	apiKey, secret, protocol, err := s.repo.CredentialsWithProtocolOf(ctx, o.AccountID)
	if err != nil {
		_ = s.repo.MarkCallbackResult(ctx, cb.ID, false, err.Error())
		return nil // 密钥问题不入死信（提示重置）
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	client := httpx.NewSafeClient(15 * time.Second)

	var body []byte
	var req *http.Request
	if protocol == "dujiao_next" {
		fulfillment := map[string]any{"type": "auto", "status": "delivered"}
		if cards, cerr := s.cardsPayloadOf(ctx, o); cerr == nil {
			fulfillment["payload"] = strings.Join(cards, "\n")
		}
		body, _ = json.Marshal(map[string]any{
			"event":               "order.fulfilled",
			"order_id":            strconv.FormatUint(o.ID, 10),
			"order_no":            o.DownstreamOrderNo,
			"downstream_order_no": strings.TrimPrefix(o.DownstreamOrderNo, ""),
			"status":              "delivered",
			"fulfillment":         fulfillment,
			"timestamp":           time.Now().Unix(),
		})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, cb.CallbackURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Dujiao-Next-Api-Key", apiKey)
		req.Header.Set("Dujiao-Next-Timestamp", ts)
		req.Header.Set("Dujiao-Next-Signature", dujiaoSign(secret, http.MethodPost, "/api/v1/upstream/callback", ts, body))
	} else {
		body, _ = json.Marshal(map[string]any{
			"supply_order_id":     o.ID,
			"downstream_order_no": o.DownstreamOrderNo,
			"status":              string(o.Status),
			"amount":              o.Amount,
		})
		nonce := fmt.Sprintf("cb_%d_%d", time.Now().UnixNano(), o.ID)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, cb.CallbackURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Supply-Key", apiKey)
		req.Header.Set("X-Supply-Timestamp", ts)
		req.Header.Set("X-Supply-Nonce", nonce)
		req.Header.Set("X-Supply-Signature", supplySign(secret, http.MethodPost, req.URL.Path, req.URL.RawQuery, ts, nonce, body))
	}
	resp, err := client.Do(req)
	if err != nil {
		_ = s.repo.MarkCallbackResult(ctx, cb.ID, false, err.Error())
		return s.scheduleCallbackRetry(ctx, cb)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = s.repo.MarkCallbackResult(ctx, cb.ID, true, "")
		return nil
	}
	_ = s.repo.MarkCallbackResult(ctx, cb.ID, false, fmt.Sprintf("下游返回 %d", resp.StatusCode))
	return s.scheduleCallbackRetry(ctx, cb)
}

// scheduleCallbackRetry 退避重试（间隔表耗尽 → 死信留档，admin 可手动重发）。
func (s *SupplyAPIService) scheduleCallbackRetry(ctx context.Context, cb *ent.DownstreamCallback) error {
	if int(cb.RetryCount) >= len(callbackIntervals) {
		s.log.Warn("supplier.callback.dead_letter", "callback_id", cb.ID, "retry", cb.RetryCount)
		return nil
	}
	delay := time.Duration(callbackIntervals[cb.RetryCount]) * time.Second
	s.log.Info("supplier.callback.retry", "callback_id", cb.ID, "delay", delay.String())
	// 进程内延时重试（多实例场景由 worker 队列重投；降级模式单进程足够）
	go func() {
		time.Sleep(delay)
		ctx2, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = s.DeliverCallback(ctx2, cb.SupplyOrderID)
	}()
	return nil
}
