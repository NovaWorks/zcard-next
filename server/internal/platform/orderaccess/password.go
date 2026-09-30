// Package orderaccess shares the password gate across order details and delivery.
package orderaccess

import (
	"context"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"github.com/go-kratos/kratos/v3/errors"
)

const MaxFailures = 5
const LockTTL = 30 * time.Minute

type State struct {
	Failures  int
	ExpiresAt time.Time
}

func (s State) Locked() bool { return s.Failures >= MaxFailures && s.ExpiresAt.After(time.Now()) }

type Gate interface {
	FetchPasswordState(context.Context, string) (State, error)
	RecordFetchFailure(context.Context, string) (State, error)
	ResetFetchFailures(context.Context, string) error
}

func passwordError(s State) error {
	if s.Locked() {
		seconds := max(1, int(math.Ceil(time.Until(s.ExpiresAt).Seconds())))
		minutes := (seconds + 59) / 60
		return errors.New(429, "order.FETCH_LOCKED", fmt.Sprintf("查询失败已达 5 次，此订单在当前网络下已暂时锁定，请约 %d 分钟后重试", minutes)).WithMetadata(map[string]string{
			"retry_after_seconds": strconv.Itoa(seconds), "remaining_attempts": "0",
		})
	}
	remaining := max(0, MaxFailures-s.Failures)
	return errors.NotFound("order.NOT_FOUND", fmt.Sprintf("订单不存在或查询密码错误，还可尝试 %d 次；连续失败 5 次将锁定 30 分钟", remaining)).WithMetadata(map[string]string{"remaining_attempts": strconv.Itoa(remaining)})
}

// PublicError preserves actionable password feedback without leaking storage errors.
func PublicError(err error) error {
	e := errors.FromError(err)
	if e.Reason == "order.NOT_FOUND" || e.Reason == "order.FETCH_LOCKED" || e.Reason == "order.QUERY_PASSWORD_REQUIRED" {
		return e
	}
	return errors.InternalServer("order.QUERY_FAILED", "订单查询暂时失败，请稍后重试")
}

func Verify(ctx context.Context, gate Gate, no, hash, password, ip string) error {
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			ip = v4.String()
		} else {
			ip = parsed.Mask(net.CIDRMask(64, 128)).String()
		}
	}
	key := "fetch:" + ip + ":" + no
	if gate != nil {
		state, err := gate.FetchPasswordState(ctx, key)
		if err != nil {
			return err
		}
		if state.Locked() {
			return passwordError(state)
		}
	}
	// Opening a payment return URL without a saved password is not a failed guess.
	if password == "" {
		return errors.BadRequest("order.QUERY_PASSWORD_REQUIRED", "请输入下单时设置的查询密码")
	}
	if hash == "" || !crypto.VerifyPassword(hash, password) {
		state := State{Failures: 1}
		if gate != nil {
			var err error
			state, err = gate.RecordFetchFailure(ctx, key)
			if err != nil {
				return err
			}
		}
		return passwordError(state)
	}
	if gate != nil {
		if err := gate.ResetFetchFailures(ctx, key); err != nil {
			return err
		}
		// A concurrent fifth failure must not be cleared by this successful attempt.
		state, err := gate.FetchPasswordState(ctx, key)
		if err != nil {
			return err
		}
		if state.Locked() {
			return passwordError(state)
		}
	}
	return nil
}
