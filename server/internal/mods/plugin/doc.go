// Package plugin 承载实例侧插件业务。P0 已定义 port 与 platform/plugincontract；
// 持久化/安装由 P1 实现，可信 goja 运行时与购买门禁由 P2 实现。
// 市场发行服务位于独立 zcard-market 工程；不采用 Go 原生 plugin (.so)。
package plugin
