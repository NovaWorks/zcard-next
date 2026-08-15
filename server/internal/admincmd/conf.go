package admincmd

// 配置装载（与 main 的 loadBootstrap 同源逻辑，避免相互依赖）。

import (
	"fmt"

	"github.com/NovaWorks/zcard-next/server/internal/conf"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/file"
)

func scanConf(dir string, out *conf.Bootstrap) error {
	c := config.New(config.WithSource(file.NewSource(dir)))
	defer c.Close()
	if err := c.Load(); err != nil {
		return fmt.Errorf("读取配置失败（目录 %s）: %w", dir, err)
	}
	return c.Scan(out)
}
