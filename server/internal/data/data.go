// Package data 数据层装配：Ent client、三方言驱动选择（platform/db 收口）、
// 事务工作单元（通道 B：biz 层不见 *ent.Tx，repo 经 data.Client(ctx) 自动加入事务）。
package data

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/NovaWorks/zcard-next/server/internal/platform/events"

	// 三方言驱动注册（同一二进制，运行时按 DSN 切换，ADR-D20）：
	// 纯 Go 无 CGO（SQLite 用 modernc，保单二进制交叉编译，ADR-D19）
	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/google/wire"
)

// atlas 的 sqlclient 按驱动名 "postgres" 打开连接（迁移执行器 PG 路径）；
// pgx stdlib 注册名是 "pgx"，此处注册别名（幂等）。
func init() {
	if !slices.Contains(sql.Drivers(), "postgres") {
		sql.Register("postgres", stdlib.GetDefaultDriver())
	}
}

// ProviderSet data providers（wire）。
var ProviderSet = wire.NewSet(
	NewData,
	NewOutboxWriter,
	NewFailedTaskWriter,
	// 接口绑定：OutboxWriter → events.Writer（模块发布事件经接口，通道 A）
	wire.Bind(new(events.Writer), new(*OutboxWriter)),
)

// Data 数据句柄（业务模块 repo 经构造函数持有；禁止绕过 data.Client 持有全局单例，
// 未来 per-tenant client 由 TenantStore 注入，铁律 14）。
type Data struct {
	Client  *ent.Client
	DB      *sql.DB
	Dialect db.Dialect
}

// entDialect platform/db 方言 → ent 方言名。
func entDialect(d db.Dialect) string {
	switch d {
	case db.MySQL:
		return dialect.MySQL
	case db.Postgres:
		return dialect.Postgres
	case db.SQLite:
		return dialect.SQLite
	}
	return string(d)
}

// NewData 打开数据库并装配 ent client（wire provider）。
func NewData(c *conf.Data) (*Data, func(), error) {
	if c == nil || c.Database == nil || c.Database.Driver == "" {
		return nil, func() {}, fmt.Errorf("data: 缺少数据库配置（data.database.driver/source）")
	}
	d, err := db.Detect(c.Database.Driver)
	if err != nil {
		return nil, func() {}, err
	}
	handle, err := d.Open(c.Database.Source)
	if err != nil {
		return nil, func() {}, err
	}
	if n := c.Database.MaxOpenConns; n > 0 {
		handle.SetMaxOpenConns(int(n))
	}
	if n := c.Database.MaxIdleConns; n > 0 {
		handle.SetMaxIdleConns(int(n))
	}
	drv := entsql.OpenDB(entDialect(d), handle)
	client := ent.NewClient(ent.Driver(drv))

	cleanup := func() {
		_ = client.Close()
		_ = handle.Close()
	}
	return &Data{Client: client, DB: handle, Dialect: d}, cleanup, nil
}

// Ping 连接健康检查（/health 与启动自检）。
func (d *Data) Ping(ctx context.Context) error {
	return d.DB.PingContext(ctx)
}

type txKey struct{}

// Tx 事务工作单元：fn 内所有经 data.Client(ctx, d) 取句柄的仓储操作加入同一事务。
// biz 层只见 TxFunc，不见 *ent.Tx（规划 §4.7 通道 B）。
func Tx(ctx context.Context, d *Data, fn func(ctx context.Context) error) error {
	// 嵌套事务：复用外层（SAVEPOINT 分级 M1 按需引入）
	if _, ok := ctx.Value(txKey{}).(*ent.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.Client.Tx(ctx)
	if err != nil {
		return err
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Client 取当前上下文的 ent 句柄：事务内返回 *ent.Tx 包装的 client，否则返回基础 client。
// 所有 repo 的查询入口（ Mods 数据层唯一取句柄方式）。
func Client(ctx context.Context, d *Data) *ent.Client {
	if tx, ok := ctx.Value(txKey{}).(*ent.Tx); ok {
		return tx.Client()
	}
	return d.Client
}
