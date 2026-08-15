//go:build wireinject
// +build wireinject

// wire 注入桩（build tag 保证不进最终构建；wire 命令生成 wire_gen.go）。

package main

import (
	"log/slog"

	"github.com/NovaWorks/zcard-next/server/internal/bootstrap"
	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/server"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp init kratos application。
func wireApp(
	serverConf *conf.Server,
	dataConf *conf.Data,
	securityConf *conf.Security,
	logger *slog.Logger,
) (*kratos.App, func(), error) {
	panic(wire.Build(bootstrap.ProviderSet, server.ProviderSet, provideRunMode, newApp))
}
