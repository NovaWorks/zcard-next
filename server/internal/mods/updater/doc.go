// Package updater 在线更新模块（M2）：ed25519 签名校验（公钥编译进二进制）、
// 备份 + 原子替换 + 健康检查失败自动回滚；DB 迁移与二进制更新解耦
// （启动时检测待执行 Atlas 迁移并加锁串行执行，失败拒绝启动）。
//
// 重写 1.x 最危险组件（以 PHP 进程身份执行 git reset --hard 的高危模式）。
package updater
