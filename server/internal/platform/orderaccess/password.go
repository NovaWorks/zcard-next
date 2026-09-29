// Package orderaccess shares the password gate across order details and delivery.
package orderaccess

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
	"net"
	"strings"
)

type Gate interface {
	IsLocked(context.Context, string) (bool, error)
	LockFetchFailure(context.Context, string) error
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
		locked, err := gate.IsLocked(ctx, key)
		if err != nil {
			return err
		}
		if locked {
			return fmt.Errorf("order.NOT_FOUND")
		}
	}
	if hash == "" || !crypto.VerifyPassword(hash, password) {
		if gate != nil {
			if err := gate.LockFetchFailure(ctx, key); err != nil {
				return err
			}
		}
		return fmt.Errorf("order.NOT_FOUND")
	}
	return nil
}
