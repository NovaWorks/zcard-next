// Package server transport 层装配（wire）：HTTP/gRPC 双协议 + 统一中间件栈。
//
// transport 只做参数校验与装配（铁律 2）；路由注册顺序由 proto 注解控制，
// 静态路由先于参数路由（铁律 4），CI 路由冲突测试 M1 补齐。
package server

import "github.com/google/wire"

// ProviderSet server providers（wire）。
var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer)
