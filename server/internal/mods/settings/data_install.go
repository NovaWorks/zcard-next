package settings

// 安装向导（P0-04 T2）：CLI 交互式 + 事务原子（初始 settings + 内置角色种子 + 管理员）。
// 幂等：ops.installed_at 已存在即拒绝。Web /install 表单入口 M1b 前端补齐（后端逻辑同源）。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/authz"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// ErrAlreadyInstalled 已安装（幂等拒绝）。
var ErrAlreadyInstalled = errors.New("settings.ALREADY_INSTALLED")

// Installed 是否已安装（ops.installed_at 存在判定）。
func Installed(ctx context.Context, d *data.Data) bool {
	_, err := data.Client(ctx, d).Setting.Query().
		Where(setting.Group("ops"), setting.Key("installed_at")).Only(ctx)
	return err == nil
}

// InstallInput 安装参数（CLI 交互收集或 Web 表单）。
type InstallInput struct {
	AdminUsername string
	AdminPassword string
	SiteName      string
	SiteURL       string
}

// Install 执行安装（单事务：写关键默认 settings + installed_at + 角色/权限种子 + 管理员）。
func Install(ctx context.Context, d *data.Data, in InstallInput) error {
	if Installed(ctx, d) {
		return ErrAlreadyInstalled
	}
	if len(in.AdminPassword) < 8 {
		return errors.New("settings.WEAK_PASSWORD：管理员密码至少 8 位")
	}
	client := d.Client
	return data.Tx(ctx, d, func(ctx context.Context) error {
		// 1) 关键业务默认值（站点名/网址 + 安装时间戳）
		writes := []struct {
			group, key string
			value      any
		}{
			{"site", "name", in.SiteName},
			{"site", "url", in.SiteURL},
			{"ops", "installed_at", time.Now().UTC().Format(time.RFC3339)},
		}
		for _, w := range writes {
			v, _ := json.Marshal(w.value)
			if err := client.Setting.Create().
				SetGroup(w.group).SetKey(w.key).SetValue(v).
				OnConflict().UpdateValue().Exec(ctx); err != nil {
				return err
			}
		}
		// 2) 内置角色与权限种子（幂等）
		if err := authz.EnsureBuiltinRoles(ctx, client); err != nil {
			return err
		}
		// 3) 管理员（super_admin）
		role, err := client.AdminRole.Query().Where().First(ctx)
		if err != nil {
			return err
		}
		hash, err := crypto.HashPassword(in.AdminPassword)
		if err != nil {
			return err
		}
		if _, err := client.AdminUser.Create().
			SetUsername(in.AdminUsername).
			SetPasswordHash(hash).
			SetNickname(in.AdminUsername).
			SetRoleID(role.ID).
			SetEnabled(true).
			Save(ctx); err != nil {
			return err
		}
		return nil
	})
}

// RunInstallCLI CLI 交互式入口（zcard install；stdin 三问）。
func RunInstallCLI(ctx context.Context, d *data.Data) error {
	if Installed(ctx, d) {
		return ErrAlreadyInstalled
	}
	r := bufio.NewReader(os.Stdin)
	in := InstallInput{}
	in.AdminUsername = prompt(r, "管理员用户名")
	for {
		in.AdminPassword = prompt(r, "管理员密码（≥8 位）")
		if len(in.AdminPassword) >= 8 {
			break
		}
		fmt.Println("  ✗ 密码至少 8 位，请重输")
	}
	in.SiteName = promptDefault(r, "站点名称", "ZCard 演示站")
	in.SiteURL = promptDefault(r, "站点网址", "")
	return Install(ctx, d, in)
}

func prompt(r *bufio.Reader, label string) string {
	for {
		fmt.Printf("%s: ", label)
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
}

func promptDefault(r *bufio.Reader, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}
