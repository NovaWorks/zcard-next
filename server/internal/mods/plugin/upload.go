package plugin

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/emptypb"
)

const ImportOperation = "/zcard.api.admin.v1.AdminPluginService/ImportPlugin"

func (s *AdminPluginService) RegisterImport(srv *khttp.Server) {
	for _, action := range []string{"import", "inspect"} {
		operation := ImportOperation
		if action == "inspect" {
			operation = "/zcard.api.admin.v1.AdminPluginService/InspectPlugin"
		}
		srv.Route("/").POST("/api/v1/admin/plugins/"+action, func(c khttp.Context) error {
			khttp.SetOperation(c, operation)
			handler := c.Middleware(func(ctx context.Context, _ any) (any, error) {
				in, err := s.readMultipart(ctx, c.Request(), c.Response())
				if err != nil {
					return nil, err
				}
				if action == "inspect" {
					return s.InspectPlugin(ctx, in)
				}
				return s.ImportPlugin(ctx, in)
			})
			out, err := handler(c, &emptypb.Empty{})
			if err != nil {
				return err
			}
			return c.Result(http.StatusOK, out)
		})
	}

}
func (s *AdminPluginService) readMultipart(ctx context.Context, r *http.Request, w http.ResponseWriter) (*adminv1.ImportPluginRequest, error) {
	if _, err := s.authorize(ctx, "plugin:manage", true, false); err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, pc.MaxArchiveBytes+3*pc.MaxJSONBytes)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(time.Minute))
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.BadRequest("plugin.MULTIPART_REQUIRED", "请上传签名插件包")
	}
	// Require fixed, bounded parts; the package store verifies before publication.
	data := make(map[string][]byte)
	for _, name := range []string{"command", "descriptor", "signature", "archive"} {
		part, err := mr.NextPart()
		if err != nil || part.FormName() != name {
			return nil, errors.BadRequest("plugin.INVALID_MULTIPART", "上传字段或顺序错误")
		}
		limit := pc.MaxJSONBytes
		if name == "signature" {
			limit = 64
		}
		if name == "archive" {
			limit = pc.MaxArchiveBytes
		}
		b, err := io.ReadAll(io.LimitReader(part, int64(limit+1)))
		_ = part.Close()
		if err != nil || len(b) > limit {
			return nil, errors.New(413, "plugin.IMPORT_LIMIT", "上传中断或超过大小限制")
		}
		data[name] = b
	}
	if _, err = mr.NextPart(); err != io.EOF {
		return nil, errors.BadRequest("plugin.INVALID_MULTIPART", "不接受额外上传字段")
	}
	cmd := &adminv1.OperatePluginRequest{}
	if err = protojson.Unmarshal(data["command"], cmd); err != nil {
		return nil, errors.BadRequest("plugin.INVALID_COMMAND", "操作参数不合法")
	}
	return &adminv1.ImportPluginRequest{Command: cmd, DescriptorJson: data["descriptor"], Signature: data["signature"], Archive: data["archive"]}, nil
}

// RequestBodyLimit bounds allocations before generated JSON handlers decode the
// body. Authentication is still enforced by the normal admin middleware.
func RequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/plugins") || (strings.HasPrefix(r.URL.Path, "/api/v1/admin/products/") && strings.Contains(r.URL.Path, "/plugin-rules/")) {
			limit := int64(pc.MaxJSONBytes)
			if r.URL.Path == "/api/v1/admin/plugins/import" || r.URL.Path == "/api/v1/admin/plugins/inspect" {
				limit = pc.MaxArchiveBytes + 3*pc.MaxJSONBytes
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
