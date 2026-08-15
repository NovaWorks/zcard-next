// Package migrations 内嵌 Atlas 版本化迁移（go:embed 编译期嵌入，单二进制交付）。
//
// 每方言独立迁移线（ADR-D18）：migrations/{sqlite,mysql,postgres}/。
// 生成：make migrate-diff DIALECT=<dialect> NAME=<变更名>（经 docker dev-db + ent NamedDiff）。
// 启动时由 internal/data.ApplyMigrations 应用（加锁串行，失败拒绝启动，§10.4）。
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed sqlite mysql postgres
var files embed.FS

// FS 返回指定方言的迁移文件系统（目录不存在报错——迁移线缺失即构建期问题）。
func FS(dialect string) (fs.FS, error) {
	dir := fmt.Sprintf("%s", dialect)
	if _, err := fs.ReadDir(files, dir); err != nil {
		return nil, fmt.Errorf("migrations: 方言 %q 迁移目录缺失: %w", dialect, err)
	}
	return fs.Sub(files, dir)
}
