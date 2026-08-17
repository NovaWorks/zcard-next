//go:build !fullstack

// Package web 非 fullstack 形态（默认）：API-only 二进制。
//
// 前端经外部静态服务（vite dev / Nginx / CDN）部署。-tags fullstack 才嵌入
// dist 并提供 SPA 服务（§10.1 交付形态纪律：dist 缺失即编译失败）。
package web

import "net/http"

// Handler 非 fullstack 下为空壳（接线侧以 Available() 分流，不会构造）。
type Handler struct{}

// NewStorefrontHandler fullstack 专属——默认形态调用即 panic（编程错误）。
func NewStorefrontHandler() *Handler { panic("web: 非 fullstack 构建（-tags fullstack 才嵌入前端）") }

// NewAdminHandler 同上。
func NewAdminHandler() *Handler { panic("web: 非 fullstack 构建") }

// Available 是否 fullstack 形态（server 接线分流：true 才挂 SPA 路由）。
func Available() bool { return false }

// ServeHTTP 不可达（接线侧不注册）。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

var _ http.Handler = (*Handler)(nil)
