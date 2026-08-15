// Package ent 为 Ent 生成代码根（生成物提交仓库，CI 校验 go generate 无 diff）。
package ent

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/versioned-migration,sql/upsert ./schema
