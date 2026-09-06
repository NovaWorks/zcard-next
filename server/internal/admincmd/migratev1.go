package admincmd

// migrate-from-v1 子命令入口（《数据迁移工具开发计划》P0：骨架 + preflight）。
//
//	zcard migrate-from-v1 --old-env <1.x 部署目录> [--conf configs] [--dry-run]
//	zcard migrate-from-v1 --dsn-old 'user:pass@tcp(host:3306)/zcard' ...
//
// 密钥优先级：--old-app-key/--old-card-key > 环境变量 ZCARD_V1_APP_KEY/ZCARD_V1_CARD_KEY
// > --old-env 的 .env > 源库 settings（卡密钥匙）。
// 实跑（非 dry-run/verify-only）需交互确认或 --yes——这是唯一批量写库的子命令。

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/conf"
	"github.com/NovaWorks/zcard-next/server/internal/migratev1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/crypto"
)

// RunMigrateV1 migrate-from-v1 子命令入口（main 分发调用）。
func RunMigrateV1(args []string) error {
	fs := flag.NewFlagSet("migrate-from-v1", flag.ExitOnError)
	confDir := fs.String("conf", "configs", "2.0 配置目录（决定目标库与密钥）")
	dsnOld := fs.String("dsn-old", "", "1.x 源库 DSN（mysql；与 --old-env 二选一）")
	oldEnv := fs.String("old-env", "", "1.x 部署目录（读 .env 自动装配 DSN/APP_KEY/CARD_ENCRYPTION_KEY/APP_TIMEZONE）")
	appKey := fs.String("old-app-key", "", "1.x APP_KEY（base64: 前缀形态；优先级最高）")
	cardKey := fs.String("old-card-key", "", "1.x 卡密钥匙（覆盖 .env 与 settings 解析）")
	phase := fs.String("phase", "all", "迁移阶段：0-6 区间 / 逗号列表 / all")
	batch := fs.Int("batch", 1000, "每批行数")
	dryRun := fs.Bool("dry-run", false, "只巡检与产出报告，不写目标库")
	verifyOnly := fs.Bool("verify-only", false, "仅对已迁移数据跑校验（P6 交付）")
	reportDir := fs.String("report-dir", "", "报告目录（默认 ./migrate-report-<时间戳>）")
	onError := fs.String("on-error", "continue", "行级错误策略：continue | abort")
	visitDays := fs.Int("visit-days", 90, "visit_logs 聚合保留天数（0=不迁）")
	auditMonths := fs.Int("audit-months", 12, "security_audit_logs 保留月数（0=全量，-1=不迁）")
	sample := fs.Int("sample", 100, "卡密抽样比对数量")
	skipMAC := fs.Bool("skip-mac", false, "跳过载荷 MAC 校验（仅限密钥疑似损坏的抢救场景）")
	yes := fs.Bool("yes", false, "跳过交互确认（脚本化运行）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsnOld == "" && *oldEnv == "" {
		return fmt.Errorf("缺少源库：--dsn-old 或 --old-env 二选一")
	}
	if *onError != "continue" && *onError != "abort" {
		return fmt.Errorf("--on-error 仅支持 continue|abort")
	}
	phases, err := migratev1.ParsePhaseSpec(*phase)
	if err != nil {
		return err
	}

	// 目标配置（确认提示用；open() 内部再次装载）
	bc := &conf.Bootstrap{}
	if err := scanConf(*confDir, bc); err != nil {
		return err
	}
	targetDesc := "zcard 2.0"
	if bc.Data != nil && bc.Data.Database != nil {
		source := bc.Data.Database.Source
		if i := strings.Index(source, "@"); i > 0 {
			source = source[i:] // 粗脱敏：截掉用户名密码段
		}
		if len(source) > 60 {
			source = source[:60] + "…"
		}
		targetDesc = fmt.Sprintf("zcard 2.0（%s %s）", bc.Data.Database.Driver, source)
	}

	// 源 DSN 展示（脱敏）
	srcDesc := migratev1.MaskDSN(*dsnOld)
	if *oldEnv != "" && *dsnOld == "" {
		env, perr := migratev1.ParseDotEnv(migratev1.OldEnvPath(*oldEnv))
		if perr != nil {
			return perr
		}
		srcDesc = migratev1.MaskDSN(migratev1.MySQLDSN(env))
	}

	// 交互确认：实跑前展示双侧库与阶段计划（唯一批量写库的子命令，对齐 reencrypt 的安全基线）
	if !*dryRun && !*verifyOnly && !*yes {
		fmt.Printf("即将执行 1.x → 2.0 数据迁移\n  源库  ：%s\n  目标库：%s\n  阶段  ：%s\n", srcDesc, targetDesc, migratev1.PhaseSummary(phases))
		fmt.Printf("确认请输入 yes：")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			return fmt.Errorf("已取消")
		}
	}

	client, cleanup, err := open(*confDir)
	if err != nil {
		return err
	}
	defer cleanup()

	// 2.0 业务数据密钥（凭据重加密 DataBox）：env > conf.security.data_key（同 reencrypt 装配模式）
	dataKeyRaw := os.Getenv("ZCARD_DATA_KEY")
	if dataKeyRaw == "" && bc.Security != nil {
		dataKeyRaw = bc.Security.DataKey
	}
	dataKey, kerr := crypto.ParseHexKey(dataKeyRaw)
	if kerr != nil {
		return fmt.Errorf("ZCARD_DATA_KEY 不可用（supply 凭据重加密必需）: %w", kerr)
	}

	return migratev1.Run(context.Background(), client, migratev1.Options{
		DSNOld:      *dsnOld,
		OldEnvDir:   *oldEnv,
		AppKeyRaw:   *appKey,
		CardKeyRaw:  *cardKey,
		Phases:      phases,
		Batch:       *batch,
		DryRun:      *dryRun,
		VerifyOnly:  *verifyOnly,
		ReportDir:   *reportDir,
		OnError:     *onError,
		VisitDays:   *visitDays,
		AuditMonths: *auditMonths,
		Sample:      *sample,
		SkipMAC:     *skipMAC,
		Yes:         *yes,
		TargetDesc:  targetDesc,
		DataKey:     dataKey,
	})
}
