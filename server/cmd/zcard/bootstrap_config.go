package main

// 零配置首启（单文件部署）：configs/config.yaml 缺失时自动生成 SQLite 引导配置。
//
// 生成的配置只是安装向导（/install）的运行载体——Web 里选 PG/MySQL 后向导会
// 改写本文件并自重启切换到目标库；选 SQLite 则直接在引导库上完成安装。
// 四把安全密钥落盘真随机值（env ZCARD_* 仍可覆盖；卡密密钥绝不能留空——
// 空值每次启动随机，重启后已存卡密不可解）。

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
)

const bootstrapConfigTemplate = `# ZCard 零配置首启自动生成（%s）
# 引导载体：浏览器打开 http://<本机IP>:8000/install 在向导中选择数据库完成安装。
# 选 PG/MySQL 时向导会改写本文件并自重启；手工调整参考 config.example.yaml。

server:
  http:
    addr: 0.0.0.0:8000
    timeout: 30s
  grpc:
    addr: 127.0.0.1:9000
    timeout: 30s
  migrate_on_start: true
  admin_base_path: ""

data:
  database:
    driver: sqlite
    source: file:data/zcard.db
    max_open_conns: 20
    max_idle_conns: 5
  # Redis 留空：队列同步降级（零依赖起步）；向导选 PG/MySQL 时填写并落盘
  redis:
    addr: ""
    read_timeout: 0.2s
    write_timeout: 0.2s
  worker_concurrency: 5

security:
  jwt_admin_key: "%s"
  jwt_user_key: "%s"
  card_key: "%s"
  data_key: "%s"

tenancy:
  mode: row
  main_domain: ""

log:
  level: info
  format: text
`

// randomHex n 字节加密随机数的 hex（卡密/数据密钥 32 字节 = 64 字符）。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand 失败: %v", err))
	}
	return hex.EncodeToString(buf)
}

// ensureBootstrapConfig 配置缺失时生成引导配置（已存在则不动）。
// 返回是否新生成；目录不可写等失败给出可行动的错误信息。
func ensureBootstrapConfig(confDir string) (bool, error) {
	path := filepath.Join(confDir, "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return false, fmt.Errorf("创建配置目录 %s 失败（当前目录不可写？可改用 -conf 指定）: %w", confDir, err)
	}
	content := fmt.Sprintf(bootstrapConfigTemplate,
		time.Now().Format("2006-01-02 15:04:05"),
		randomHex(32), randomHex(32), randomHex(32), randomHex(32),
	)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("写入引导配置 %s 失败: %w", path, err)
	}
	return true, nil
}

// ensureSQLiteDir SQLite 文件库的父目录自动创建（驱动不建目录；对自定义
// file: 路径的既有部署同样生效，目录已存在时幂等）。
func ensureSQLiteDir(bc *conf.Bootstrap) {
	if bc == nil || bc.Data == nil || bc.Data.Database == nil {
		return
	}
	db := bc.Data.Database
	if !strings.EqualFold(db.Driver, "sqlite") || !strings.HasPrefix(db.Source, "file:") {
		return
	}
	p := strings.TrimPrefix(db.Source, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 { // 去 ?params 后缀
		p = p[:i]
	}
	if dir := filepath.Dir(p); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
}
