//go:build fullstack

// Package web 前端产物嵌入锚点（fullstack 形态，规划 §10.1）。
//
// go:embed 只能引用包目录内文件，故锚定本目录（产物由 make web-dist 从
// monorepo 前端构建拷贝生成，不入库）。dist 缺失时本 embed 编译失败——
// 「dist 缺失即编译失败」友商纪律的落地形态。SPA 服务语义见 internal/web。
package web

import "embed"

// DistFS 前端产物（storefront/ 与 admin/ 两子树）。
//
//go:embed all:storefront all:admin
var DistFS embed.FS
