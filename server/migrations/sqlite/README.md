# SQLite 迁移线（最小化单体）

Atlas 版本化迁移，每方言独立版本线。文件由 `make migrate-diff DIALECT=sqlite NAME=xxx`
生成（ent NamedDiff + SQLite dev-db），**禁止手写**（DDL 只从 Ent schema + Atlas 出）。

目录内 `README.md` 仅为 go:embed 占位（加载器跳过非迁移文件）；`*.sql` 与 `atlas.sum`
为正式迁移产物，随仓库提交。
