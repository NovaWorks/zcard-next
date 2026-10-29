package server

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	"log/slog"
	"time"
)

// Log request metadata only. Login, supplier configuration and service forms
// carry credentials or buyer content; formatting a protobuf request exposes it.
func requestLogging(logger *slog.Logger) middleware.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			started := time.Now()
			reply, err := next(ctx, req)
			operation := ""
			if tr, ok := transport.FromServerContext(ctx); ok {
				operation = tr.Operation()
			}
			code, reason := 200, ""
			if err != nil {
				code = 500
				if e := errors.FromError(err); e != nil {
					code, reason = int(e.Code), e.Reason
				}
			}
			logger.InfoContext(ctx, "server request", "operation", operation, "code", code, "reason", reason, "latency", time.Since(started).Seconds())
			return reply, err
		}
	}
}
