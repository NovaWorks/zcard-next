//go:build ignore

// entc 代码生成入口（由 ent/generate.go 的 go:generate 驱动，运行于 internal/data/ent）。
//
// 与 ent CLI 等价，仅集中声明 feature flags：
//   - sql/versioned-migration：ent/migrate 的 Diff/NamedDiff（atlas 生成迁移用）
//   - sql/upsert：跨方言 upsert（settings 等幂等写入）
//
// 注意：MySQL 时间列统一 datetime(3) 由 schema 包的 timeCol() 助手在字段声明处落地
// （《数据库架构设计.md》§0 类型映射钉死 MySQL `DATETIME(3)`；ent 对 MySQL 的默认
// time 类型是 `timestamp`——秒精度且有 2038 上限，与文档偏差，故所有 time 字段必须
// 经 timeCol() 声明，架构上以「新增 time 字段必须用 timeCol」为 code review 检查项）。
package main

import (
	"log"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	cfg := &gen.Config{
		Target:  ".",
		Package: "github.com/NovaWorks/zcard-next/server/internal/data/ent",
		Features: []gen.Feature{
			gen.FeatureVersionedMigration,
			gen.FeatureUpsert,
			gen.FeatureLock, // 查询级行锁（outbox relay FOR UPDATE SKIP LOCKED 等）
		},
	}
	if err := entc.Generate("./schema", cfg); err != nil {
		log.Fatalf("ent 代码生成失败: %v", err)
	}
}
