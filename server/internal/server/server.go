// Package server transport 层装配（wire）：HTTP/gRPC 双协议 + 后台（relay/cron）+ worker。
//
// transport 只做参数校验与装配（铁律 2）；模式分流（api/all/worker）见 cmd/zcard。
package server

import "github.com/google/wire"

// ProviderSet server providers（wire）。
var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer, NewBackgroundServer, NewWorkerServer)
