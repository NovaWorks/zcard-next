//go:build !fullstack

// Package web 非 fullstack 形态：web/DistFS 不可用（前端外部部署）。
package web

import "io/fs"

// DistFS 空 FS（internal/web 在非 fullstack 下不构造 Handler，不会读取）。
var DistFS fs.FS = fs.FS(nil)
