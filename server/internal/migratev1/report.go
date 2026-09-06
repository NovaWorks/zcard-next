package migratev1

// 迁移报告输出：report.md（人读）+ report.json（脚本比对）+ errors.jsonl（失败行流水）。
// P0 承载 preflight 报告；P1 起各阶段向同一目录追加章节。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReportWriter 报告目录写入器。
type ReportWriter struct {
	dir      string
	errFile  *os.File
	errCount int
}

// NewReportWriter 创建报告目录（默认名 migrate-report-<ts> 由调用方决定）。
func NewReportWriter(dir string) (*ReportWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建报告目录失败: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "errors.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &ReportWriter{dir: dir, errFile: f}, nil
}

// Dir 报告目录。
func (w *ReportWriter) Dir() string { return w.dir }

// Close 关闭文件句柄。
func (w *ReportWriter) Close() error { return w.errFile.Close() }

// ErrorCount 累计失败行数。
func (w *ReportWriter) ErrorCount() int { return w.errCount }

// AddError 记录一条失败行（表名 + 1.x 旧 ID + 原因；P1 起数据阶段使用）。
func (w *ReportWriter) AddError(table string, oldID uint64, msg string) {
	w.errCount++
	rec := map[string]any{
		"time":    time.Now().Format(time.RFC3339),
		"table":   table,
		"old_id":  oldID,
		"message": msg,
	}
	b, _ := json.Marshal(rec)
	_, _ = w.errFile.Write(append(b, '\n'))
}

// WriteJSON 通用 JSON 附件输出。
func (w *ReportWriter) WriteJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, name), append(b, '\n'), 0o644)
}

// WritePreflight 输出 preflight 的 report.md + report.json。
func (w *ReportWriter) WritePreflight(p *PreflightReport, meta PreflightMeta) error {
	if err := w.WriteJSON("report.json", map[string]any{"meta": meta, "preflight": p}); err != nil {
		return err
	}
	var md strings.Builder
	ver := ""
	if p.ServerVersion != "" {
		ver = "（MySQL " + p.ServerVersion + "）"
	}
	fmt.Fprintf(&md, "# ZCard 1.x → 2.0 迁移预检报告\n\n")
	fmt.Fprintf(&md, "- 生成时间：%s\n", p.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&md, "- 源库：%s%s\n", meta.Source, ver)
	fmt.Fprintf(&md, "- 目标库：%s\n", meta.Target)
	fmt.Fprintf(&md, "- 模式：%s\n", meta.Mode)
	fmt.Fprintf(&md, "- 阶段计划：%s\n\n", meta.Phases)

	fmt.Fprintf(&md, "## 巡检结论：%s\n\n", verdict(p))
	fmt.Fprintf(&md, "| 状态 | 检查项 | 说明 |\n|---|---|---|\n")
	for _, c := range p.Checks {
		fmt.Fprintf(&md, "| %s | %s | %s |\n", icon(c.Status), c.Name, c.Message)
	}

	fmt.Fprintf(&md, "\n## 卡密抽样\n\n")
	fmt.Fprintf(&md, "抽样 %d 条：密文 %d（解密失败 %d），明文 %d。\n",
		p.CardSampled, p.CardEncrypted, p.CardDecryptFailed, p.CardPlaintext)

	fmt.Fprintf(&md, "\n## 密钥\n\n")
	fmt.Fprintf(&md, "- APP_KEY：%s\n", keyState(p.AppKey != nil))
	fmt.Fprintf(&md, "- 卡密钥匙：%s\n", keyState(p.CardKey != nil))

	fmt.Fprintf(&md, "\n## 规模概览\n\n")
	fmt.Fprintf(&md, "| 表 | 行数 |\n|---|---|\n")
	for _, t := range []string{"users", "products", "cards", "orders", "payments", "bills"} {
		if n, ok := p.TableCounts[t]; ok {
			fmt.Fprintf(&md, "| %s | %d |\n", t, n)
		}
	}

	if p.Timezone != "" {
		fmt.Fprintf(&md, "\n> 时区口径：APP_TIMEZONE=%s（时间列将按此转 UTC）\n", p.Timezone)
	}
	return os.WriteFile(filepath.Join(w.dir, "report.md"), []byte(md.String()), 0o644)
}

// PreflightMeta 报告元信息。
type PreflightMeta struct {
	Source string `json:"source"` // 脱敏 DSN
	Target string `json:"target"` // 目标库描述（方言 + 库名）
	Mode   string `json:"mode"`   // dry-run / preflight
	Phases string `json:"phases"`
}

// WriteStats 输出迁移统计（report.json 内嵌 + report.md 追加章节）。
func (w *ReportWriter) WriteStats(st *Stats, meta PreflightMeta) error {
	if err := w.WriteJSON("stats.json", map[string]any{"meta": meta, "tables": st.Tables}); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "\n## 迁移统计（%s）\n\n", meta.Mode)
	fmt.Fprintf(&md, "| 表 | 迁移 | 幂等跳过 | 失败 | 源行数 |\n|---|---|---|---|---|\n")
	for _, name := range sortedTableNames(st) {
		t := st.Tables[name]
		planned := "-"
		if t.Planned > 0 {
			planned = fmt.Sprintf("%d", t.Planned)
		}
		fmt.Fprintf(&md, "| %s | %d | %d | %d | %s |\n", name, t.Migrated, t.SkippedExists, t.Failed, planned)
	}
	f, err := os.OpenFile(filepath.Join(w.dir, "report.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(md.String())
	return err
}

func sortedTableNames(st *Stats) []string {
	names := make([]string, 0, len(st.Tables))
	for n := range st.Tables {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func verdict(p *PreflightReport) string {
	if p.HasFail() {
		return "❌ 未通过（存在致命项，拒绝迁移）"
	}
	return "✅ 通过（可进入迁移）"
}

func icon(s CheckStatus) string {
	switch s {
	case StatusOK:
		return "✅"
	case StatusWarn:
		return "⚠️"
	case StatusFail:
		return "❌"
	default:
		return "ℹ️"
	}
}

func keyState(ok bool) string {
	if ok {
		return "✅ 已解析"
	}
	return "⚠️ 未提供"
}
