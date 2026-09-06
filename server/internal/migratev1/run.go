// Package migratev1 1.x→2.0 数据迁移内核（《数据迁移工具开发计划》P0 交付）。
//
// P0 范围：子命令骨架 + preflight 巡检（连通性/表完整性/schema 版本探测/密钥解析
// 与抽样自检）+ 报告框架 + v1id_maps 读写 + 1.x 加密载荷解密原语（laracrypt 子包，
// 由真实 Laravel 加密器生成的 golden vectors 钉死）。
// 数据搬迁执行器自 P1（身份域）起逐阶段交付；本包只 import data/ent 与 platform/*
// （架构测试规则 3b 白名单内），卡密重加密经注入的 Sealer 接口复用 inventory 实现。
package migratev1

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
)

// Options 子命令选项（admincmd 解析后传入）。
type Options struct {
	DSNOld     string // 1.x 源库 DSN（与 OldEnvDir 二选一）
	OldEnvDir  string // 1.x 部署目录（读 .env 自动装配 DSN 与密钥）
	AppKeyRaw  string // --old-app-key（优先级最高）
	CardKeyRaw string // --old-card-key

	Phases      []int
	Batch       int
	DryRun      bool
	VerifyOnly  bool
	ReportDir   string
	OnError     string // continue | abort
	VisitDays   int
	AuditMonths int
	Sample      int
	SkipMAC     bool
	Yes         bool
	// TargetDesc 目标库描述（admincmd 从配置装配传入：方言 + 库地址），用于确认提示与报告。
	TargetDesc string
	// DataKey 2.0 业务数据密钥（ZCARD_DATA_KEY 原始 32 字节；凭据重加密必需）。
	DataKey []byte
}

// Run 迁移入口内核（client = 2.0 目标库 ent 客户端）。
func Run(ctx context.Context, client *ent.Client, opts Options) error {
	if opts.Batch <= 0 {
		opts.Batch = 1000
	}
	if opts.Sample <= 0 {
		opts.Sample = 100
	}
	if len(opts.Phases) == 0 {
		opts.Phases = ParsePhaseSpecDefault()
	}

	// 源装配：--old-env 或 --dsn-old
	var env map[string]string
	dsn := opts.DSNOld
	if opts.OldEnvDir != "" {
		parsed, err := ParseDotEnv(OldEnvPath(opts.OldEnvDir))
		if err != nil {
			return err
		}
		env = parsed
		if dsn == "" {
			dsn = MySQLDSN(parsed)
		}
	}
	if dsn == "" {
		return fmt.Errorf("缺少源库：--dsn-old <mysql-dsn> 或 --old-env <1.x 部署目录> 二选一")
	}
	if env == nil {
		env = map[string]string{}
	}

	src, err := OpenSource(dsn)
	if err != nil {
		return err
	}
	defer src.Close()

	// preflight（dry-run 与实跑共用；verify-only 同样先巡检再校验）
	report, err := RunPreflight(ctx, src, PreflightOptions{
		Env:         env,
		AppKeyRaw:   opts.AppKeyRaw,
		CardKeyRaw:  opts.CardKeyRaw,
		SampleLimit: 10,
		SkipMAC:     opts.SkipMAC,
	})
	if err != nil {
		return err
	}

	reportDir := opts.ReportDir
	if reportDir == "" {
		reportDir = fmt.Sprintf("migrate-report-%s", time.Now().Format("20060102-150405"))
	}
	rw, err := NewReportWriter(reportDir)
	if err != nil {
		return err
	}
	defer rw.Close()

	mode := "preflight"
	if opts.DryRun {
		mode = "dry-run"
	}
	meta := PreflightMeta{
		Source: MaskDSN(dsn),
		Target: opts.TargetDesc,
		Mode:   mode,
		Phases: PhaseSummary(opts.Phases),
	}
	if err := rw.WritePreflight(report, meta); err != nil {
		return err
	}

	printPreflight(report, meta, reportDir)
	if report.HasFail() {
		return fmt.Errorf("%w：详见 %s", ErrPreflightFailed, filepath.Join(reportDir, "report.md"))
	}

	// 数据搬迁：按 opts.Phases 编排（已交付：P0 系统域 / P1 身份域；P2+ 逐阶段交付）
	m := NewMigrator(src, client, NewIDMapper(client), rw, opts, report.Timezone)
	m.AppKey = report.AppKey
	m.CardKey = report.CardKey
	m.DataKey = opts.DataKey

	notDelivered := []string{}
	for _, id := range opts.Phases {
		var err error
		switch id {
		case 0:
			err = m.MigrateSystem(ctx)
		case 1:
			err = m.MigrateIdentity(ctx)
		default:
			if p, ok := PhaseByID(id); ok {
				notDelivered = append(notDelivered, fmt.Sprintf("P%d %s", p.ID, p.Name))
			}
		}
		if err != nil {
			_ = rw.WriteStats(m.Stats(), meta)
			return fmt.Errorf("P%d 执行失败: %w（已迁移进度可重跑续接，报告：%s）", id, err, reportDir)
		}
	}

	if err := rw.WriteStats(m.Stats(), meta); err != nil {
		return err
	}
	fmt.Printf("\n── 迁移统计 ──\n")
	for name, t := range m.Stats().Tables {
		fmt.Printf("  %-18s 迁移 %d / 跳过 %d / 失败 %d%s\n", name, t.Migrated, t.SkippedExists, t.Failed, plannedSuffix(t.Planned))
	}
	if len(notDelivered) > 0 {
		fmt.Printf("\n注意：%s 尚未交付（按计划后续阶段实现），本次未执行。\n", strings.Join(notDelivered, "、"))
	}
	fmt.Printf("\n%s 完成。报告目录：%s\n", map[bool]string{true: "dry-run", false: "迁移"}[opts.DryRun], reportDir)
	return nil
}

func plannedSuffix(p int64) string {
	if p > 0 {
		return fmt.Sprintf("（源共 %d 行）", p)
	}
	return ""
}

func printPreflight(r *PreflightReport, meta PreflightMeta, dir string) {
	fmt.Printf("── ZCard 1.x → 2.0 迁移预检 ──\n")
	src := meta.Source
	if r.ServerVersion != "" {
		src += "（MySQL " + r.ServerVersion + "）"
	}
	fmt.Printf("源库：%s\n", src)
	for _, c := range r.Checks {
		fmt.Printf("  [%s] %s：%s\n", c.Status, c.Name, c.Message)
	}
	fmt.Printf("报告：%s\n", dir)
}

// ParsePhaseSpecDefault 空 --phase 时的全阶段。
func ParsePhaseSpecDefault() []int {
	ids, _ := ParsePhaseSpec("all")
	return ids
}

// MaskDSN 脱敏 DSN（隐去密码，用于日志/确认提示/报告）。
func MaskDSN(dsn string) string {
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		return "mysql:<无法解析的 DSN>"
	}
	if cfg.Passwd != "" {
		cfg.Passwd = "****"
	}
	return cfg.FormatDSN()
}
