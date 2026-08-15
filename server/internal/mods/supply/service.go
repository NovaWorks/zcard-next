package supply

// 对外供货 API（本站作上游，ZCard Supply v2 协议，§5.8）——M2 完整落地。
//
// 鉴权：HMAC 四头（X-Supply-Key/Timestamp/Nonce/Signature），±300s 时间窗，
// Nonce Redis/DB 双实现防重放，按 key 限流；回调路由不挂 JWT（架构测试规则 9）。
// M0 骨架仅提供 Ping 连通性端点；HMAC 中间件挂载点见 internal/server/http.go。

import (
	"context"

	supplyv1 "github.com/NovaWorks/zcard-next/server/api/supply/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// ServerVersion 由构建注入（-ldflags）。
var ServerVersion = "dev"

// SupplyService 对外供货服务（实现 supplyv1.SupplyService）。
type SupplyService struct {
	supplyv1.UnimplementedSupplyServiceServer
}

// NewSupplyService 构造。
func NewSupplyService() *SupplyService { return &SupplyService{} }

// Ping 连通性与协议版本探测。
func (*SupplyService) Ping(context.Context, *emptypb.Empty) (*supplyv1.PingReply, error) {
	return &supplyv1.PingReply{
		Protocol:   "zcard-supply-v2",
		Version:    ServerVersion,
		ServerTime: nowUnix(),
	}, nil
}

func nowUnix() int64 { return timeNow().Unix() }
