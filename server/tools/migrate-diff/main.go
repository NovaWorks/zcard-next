// migrate-diff 迁移生成工具（Atlas 版本化迁移，禁止手写 DDL）。
//
// 依据 ent schema（唯一真理源）与 dev-db 比对，生成 migrations/<dialect>/ 下的
// 版本化迁移文件 + atlas.sum。经 Makefile 调用：
//
//	make migrate-diff DIALECT=sqlite  NAME=init
//	make migrate-diff DIALECT=mysql   NAME=add_xxx   （docker dev-db）
//	make migrate-diff DIALECT=postgres NAME=add_xxx  （docker dev-db）
package main

import (
	atlasschema "ariga.io/atlas/sql/schema"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	entdialect "entgo.io/ent/dialect"
	entschema "entgo.io/ent/dialect/sql/schema"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent/migrate"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"
)

// atlas URL 模式按 URL scheme 查找 database/sql 驱动：sqlite→"sqlite3"、
// postgres→"postgres"。modernc.org/sqlite 注册名是 "sqlite"、pgx stdlib 注册
// "pgx"，此处注册别名（均为纯 Go 无 CGO，ADR-D19）。
func init() {
	if !slices.Contains(sql.Drivers(), "sqlite3") {
		sql.Register("sqlite3", &sqlite.Driver{})
	}
	if !slices.Contains(sql.Drivers(), "postgres") {
		sql.Register("postgres", stdlib.GetDefaultDriver())
	}
}

// devURLFor 各方言默认 dev-db URL（SQLite 内存零依赖；MySQL/PG 由 Makefile 起 docker）。
func devURLFor(d string) string {
	switch d {
	case entdialect.MySQL:
		return "mysql://root:pass@tcp(localhost:13306)/dev"
	case entdialect.Postgres:
		return "postgres://postgres:pass@localhost:15432/dev?sslmode=disable"
	default:
		// SQLite 内存 dev-db（atlas 按需建表比对）
		return "sqlite://file?mode=memory&_fk=1"
	}
}

func main() {
	var (
		dialectName    = flag.String("dialect", "", "sqlite | mysql | postgres（必填）")
		devURL         = flag.String("url", "", "dev-db URL（空则用方言默认）")
		additiveTables = flag.String("additive-tables", "", "只生成指定表的新增列/索引/表（逗号分隔；排除无关 schema 漂移）")
		name           = flag.String("name", "changes", "迁移名（kebab/snake）")
	)
	flag.Parse()

	if *dialectName == "" {
		flag.Usage()
		os.Exit(2)
	}
	var d string
	switch *dialectName {
	case "sqlite", "sqlite3":
		d = entdialect.SQLite
	case "mysql", "mariadb":
		d = entdialect.MySQL
	case "postgres", "postgresql":
		d = entdialect.Postgres
	default:
		log.Fatalf("未知方言 %q", *dialectName)
	}
	url := *devURL
	if url == "" {
		url = devURLFor(d)
	}

	dirPath := filepath.Join("migrations", *dialectName)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		log.Fatalf("创建迁移目录失败: %v", err)
	}
	dir, err := atlasmigrate.NewLocalDir(dirPath)
	if err != nil {
		log.Fatalf("打开迁移目录失败: %v", err)
	}
	options := []entschema.MigrateOption{
		entschema.WithDir(dir), entschema.WithMigrationMode(entschema.ModeReplay), entschema.WithDialect(d), entschema.WithFormatter(atlasmigrate.DefaultFormatter),
	}
	if *additiveTables != "" {
		tables := strings.Split(*additiveTables, ",")
		options = append(options, entschema.WithDiffHook(func(next entschema.Differ) entschema.Differ {
			return entschema.DiffFunc(func(current, desired *atlasschema.Schema) ([]atlasschema.Change, error) {
				changes, err := next.Diff(current, desired)
				if err != nil {
					return nil, err
				}
				selected := []atlasschema.Change{}
				for _, change := range changes {
					switch c := change.(type) {
					case *atlasschema.AddTable:
						if slices.Contains(tables, c.T.Name) {
							selected = append(selected, c)
						}
					case *atlasschema.ModifyTable:
						if !slices.Contains(tables, c.T.Name) {
							continue
						}
						for _, child := range c.Changes {
							switch v := child.(type) {
							case *atlasschema.AddColumn:
								// Ent represents constant defaults as RawExpr. Atlas otherwise
								// rebuilds SQLite tables; constants can safely use ADD COLUMN.
								if d == entdialect.SQLite {
									if raw, ok := v.C.Default.(*atlasschema.RawExpr); ok {
										literal := strings.Trim(raw.X, "() ")
										_, number := strconv.ParseFloat(literal, 64)
										if literal == "true" || literal == "false" || number == nil {
											v.C.Default = &atlasschema.Literal{V: literal}
										}
									}
								}
							case *atlasschema.ModifyColumn:
								// Extending an enum is additive: existing values retain their order.
								before, bok := v.From.Type.Type.(*atlasschema.EnumType)
								after, aok := v.To.Type.Type.(*atlasschema.EnumType)
								if !bok || !aok || len(after.Values) < len(before.Values) || !slices.Equal(before.Values, after.Values[:len(before.Values)]) || v.Change != atlasschema.ChangeType {
									return nil, fmt.Errorf("additive migration refuses modifying %s.%s", c.T.Name, v.To.Name)
								}
							case *atlasschema.AddIndex:
							default:
								return nil, fmt.Errorf("additive migration refuses %T on %s", child, c.T.Name)
							}
						}
						selected = append(selected, c)
					case *atlasschema.DropTable:
						if slices.Contains(tables, c.T.Name) {
							return nil, fmt.Errorf("additive migration refuses dropping %s", c.T.Name)
						}
					}
				}
				return selected, nil
			})
		}))
	}
	if err := migrate.NamedDiff(
		context.Background(),
		url,
		*name,
		options...,
	); err != nil {
		log.Fatalf("生成迁移失败: %v", err)
	}
	fmt.Printf("迁移已生成：%s（方言 %s）\n", dirPath, d)
}
