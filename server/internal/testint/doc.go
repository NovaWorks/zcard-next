// Package testint CI 集成测试骨架（， 方言三线矩阵的 MySQL/PG 线）。
//
// 全部用例挂 `-tags=integration`（make test 单元线零影响）；DSN 环境变量
// ZCARD_TEST_MYSQL_DSN / ZCARD_TEST_PG_DSN 未配置时自动 Skip。骨架细节见
// testint.go（同目录，tag 内）。本文件无 tag——保证包在不带 tag 时仍可编译
// （go test ./... 不因「全文件被排除」报 setup failed）。
package testint
