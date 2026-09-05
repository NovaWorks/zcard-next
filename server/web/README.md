# web/ —— fullstack 嵌入产物目录（不入库）

`make build-fullstack` 前自动执行 `make web-dist`：从 monorepo 前端构建并拷贝
`storefront/dist`、`admin/dist` 到本目录。go:embed 无法引用模块外路径，
故以本目录为 embed 锚点（产物由构建链生成，禁止手改/提交）。

无产物时 `-tags fullstack` 编译失败（友商纪律：dist 缺失即编译失败）。
