// Package architecture 架构守护测试（规划 ，只有测试，无生产代码）。
//
// 照搬友商最有价值的资产：分层规则、模块边界、platform 纯净由测试强制，
// 而非口头约定。CI 中与单元测试同权重，红灯即阻断合并。
//
// 交付框架 + 前三条核心规则；后续规则按里程碑补齐（文件预算/RBAC 覆盖/
// 回调路由/金额纪律 AST 扫描等，见 全表）。
package architecture

import (
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

const modulePath = "github.com/NovaWorks/zcard-next/server"

// loadPackages 解析全仓包（Mode 需要类型与语法信息；一次装载全部规则复用）。
func loadPackages(t *testing.T) []*packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: mustRepoRoot(t),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("解析包失败: %v", err)
	}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if len(p.Errors) > 0 {
			t.Fatalf("包 %s 存在错误: %v", p.PkgPath, p.Errors)
		}
	})
	return pkgs
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/architecture → server 根（go.mod 所在目录）
	return filepath.Dir(filepath.Dir(wd))
}

// importViolation 一条违规记录。
type importViolation struct {
	pkg      string // 违规包
	file     string // 违规文件
	imported string // 违规 import
	rule     string // 命中规则
}

// walkImports 遍历全部包的全部文件 import，对每个 import 调用 check；
// check 返回非空 rule 表示违规。
func walkImports(t *testing.T, pkgs []*packages.Package, check func(pkgPath, file, imported string) string) []importViolation {
	t.Helper()
	var out []importViolation
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			file := p.Fset.Position(f.Pos()).Filename
			rel := relToServer(t, file)
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if rule := check(p.PkgPath, rel, path); rule != "" {
					out = append(out, importViolation{pkg: p.PkgPath, file: rel, imported: path, rule: rule})
				}
			}
		}
	}
	return out
}

func relToServer(t *testing.T, file string) string {
	t.Helper()
	root := mustRepoRoot(t)
	rel, err := filepath.Rel(root, file)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// ---------------------------------------------------------------------------
// 规则 1：port 纯净（-11）—— mods/*/port 只允许依赖标准库、platform/*、
// 本仓 api 生成类型；引入任何业务实现/Ent/Kratos transport/asynq 即红灯。
// ---------------------------------------------------------------------------

func TestRule1PortPurity(t *testing.T) {
	pkgs := loadPackages(t)
	violations := walkImports(t, pkgs, func(pkgPath, _, imported string) string {
		if !strings.HasPrefix(pkgPath, modulePath+"/internal/mods/") || !strings.HasSuffix(pkgPath, "/port") {
			return ""
		}
		if !isExternal(imported) {
			return "" // 标准库
		}
		switch {
		case strings.HasPrefix(imported, modulePath+"/internal/platform/"),
			strings.HasPrefix(imported, modulePath+"/api/"),
			strings.HasPrefix(imported, modulePath+"/internal/mods/") && strings.HasSuffix(pkgPath, "/port"):
			return ""
		}
		return "port 包 import 白名单外依赖（仅允许标准库/platform/*/api 生成类型）"
	})
	reportViolations(t, violations)
}

// ---------------------------------------------------------------------------
// 规则 2：platform 纯净（-3）—— platform/* 不得 import mods/*（反向依赖）
// 与 Kratos transport（平台层与传输解耦，保证可替换性）。
// ---------------------------------------------------------------------------

func TestRule2PlatformPurity(t *testing.T) {
	pkgs := loadPackages(t)
	violations := walkImports(t, pkgs, func(pkgPath, _, imported string) string {
		if !strings.HasPrefix(pkgPath, modulePath+"/internal/platform/") {
			return ""
		}
		if strings.HasPrefix(imported, modulePath+"/internal/mods/") {
			return "platform 反向依赖 mods（禁止）"
		}
		if strings.HasPrefix(imported, "github.com/go-kratos/kratos/") &&
			strings.Contains(imported, "/transport") {
			return "platform 依赖 Kratos transport（禁止）"
		}
		return ""
	})
	reportViolations(t, violations)
}

// ---------------------------------------------------------------------------
// 规则 3：transport 不泄漏（-4）+ Ent 收口（-5）+ 模块边界(规则 3）。
// ---------------------------------------------------------------------------

func TestRule3Layering(t *testing.T) {
	pkgs := loadPackages(t)
	violations := walkImports(t, pkgs, func(pkgPath, file, imported string) string {
		// 3a. biz/data 层不 import Kratos transport（transport 只能在 service 层与 server 装配层）
		isBizOrData := strings.HasPrefix(pkgPath, modulePath+"/internal/mods/") &&
			(strings.HasSuffix(file, "biz.go") || strings.HasSuffix(file, "data.go") ||
				strings.HasSuffix(file, "port.go") || strings.HasSuffix(file, "events.go"))
		if isBizOrData && strings.HasPrefix(imported, "github.com/go-kratos/kratos/") &&
			strings.Contains(imported, "/transport") {
			return "biz/data/port 层依赖 Kratos transport（transport 只允许 service/server 层）"
		}
		// 3b. Ent 收口：只有 internal/data/**、mods/*/data.go、mods/*/providers.go（绑定实现）、
		// internal/admincmd（运维子命令）、internal/migratev1（migrate-from-v1 迁移内核，
		// v1id_maps 读写必需）与 tools/*（构建期迁移工具）可 import ent
		if strings.HasPrefix(imported, modulePath+"/internal/data/ent") ||
			imported == "entgo.io/ent" || strings.HasPrefix(imported, "entgo.io/ent/") {
			allowed := pkgPath == modulePath+"/internal/data" ||
				strings.HasPrefix(pkgPath, modulePath+"/internal/data/") ||
				pkgPath == modulePath+"/internal/admincmd" ||
				pkgPath == modulePath+"/internal/migratev1" ||
				strings.HasPrefix(pkgPath, modulePath+"/tools/") ||
				strings.HasPrefix(pkgPath, modulePath+"/internal/mods/") &&
					(strings.HasPrefix(file[strings.LastIndex(file, "/")+1:], "data") || strings.HasSuffix(file, "providers.go"))
			if !allowed {
				return "Ent import 越界（仅 mods/*/data.go、providers.go、internal/data、admincmd、migratev1、tools 允许）"
			}
		}
		// 3c. 模块边界：mods/A import mods/B 只允许落在 B 的 port/ 包(规则 3）
		if from, ok := strings.CutPrefix(pkgPath, modulePath+"/internal/mods/"); ok {
			if to, ok2 := strings.CutPrefix(imported, modulePath+"/internal/mods/"); ok2 {
				fromMod := strings.SplitN(from, "/", 2)[0]
				toModAndSub := strings.SplitN(to, "/", 2)
				toMod := toModAndSub[0]
				if fromMod != toMod && len(toModAndSub) == 2 && toModAndSub[1] != "port" {
					return "跨模块 import 越界（只允许对方 port/ 包）：mods/" + fromMod + " → mods/" + to
				}
			}
		}
		return ""
	})
	reportViolations(t, violations)
}

// ---------------------------------------------------------------------------
// 规则 4(加餐，金额纪律雏形 -6）：带 amount/price/fee/balance 名字的
// 字段/声明禁止 float32/float64（铁律 1 的静态扫描；完整 AST 规则 补齐参数级）。
// ---------------------------------------------------------------------------

func TestRule4MoneyDiscipline(t *testing.T) {
	pkgs := loadPackages(t)
	bad := []string{}
	for _, p := range pkgs {
		if !strings.HasPrefix(p.PkgPath, modulePath+"/internal/") {
			continue
		}
		for _, f := range p.Syntax {
			file := relToServer(t, p.Fset.Position(f.Pos()).Filename)
			ast.Inspect(f, func(n ast.Node) bool {
				// 字段声明：type T struct { XxxPrice float64 }
				if field, ok := n.(*ast.Field); ok {
					if isFloatType(field.Type) && len(field.Names) > 0 && moneyishName(field.Names[0].Name) && !floatFieldAllowlist[field.Names[0].Name] {
						bad = append(bad, file+": 浮点字段 "+field.Names[0].Name)
					}
				}
				return true
			})
		}
	}
	for _, b := range bad {
		t.Errorf("金额纪律违规（%s）：金额一律 int64 分（money.Cents）", b)
	}
}

// floatFieldAllowlist 费率/汇率类浮点豁免（显式登记制）：这些是「率」不是「金额」，
// 数据库架构 允许 decimal 存储；新增豁免必须在 CR 说明理由。
var floatFieldAllowlist = map[string]bool{
	"PriceMarkupPercent": true, // 上游加价百分比（百分比，非金额）
	"ExchangeRate":       true, // 汇率快照
	"Rate":               true, // 费率快照（佣金）
}

func isFloatType(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && (id.Name == "float32" || id.Name == "float64")
}

func moneyishName(name string) bool {
	l := strings.ToLower(name)
	for _, kw := range []string{"amount", "price", "fee", "balance"} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

func isExternal(path string) bool { return strings.Contains(path, ".") }

func reportViolations(t *testing.T, vs []importViolation) {
	t.Helper()
	for _, v := range vs {
		t.Errorf("[%s] %s (%s) → %s", v.rule, v.file, v.pkg, v.imported)
	}
	if len(vs) > 0 {
		t.Logf("共 %d 处违规（架构测试红灯即阻断合并，规划 §4.10）", len(vs))
	}
}

// ---------------------------------------------------------------------------
// 规则 5（， 验收）：货源适配器出站纪律——mods/supply/adapter 禁止直接
// 构造 http.Client（`&http.Client{` / http.DefaultClient），出站必须经
// platform/httpx（SSRF 防护唯一入口；凭据零泄漏依赖该收口）。
// ---------------------------------------------------------------------------

func TestRule5AdapterOutboundDiscipline(t *testing.T) {
	pkgs := loadPackages(t)
	bad := []string{}
	for _, p := range pkgs {
		if p.PkgPath != modulePath+"/internal/mods/supply/adapter" {
			continue
		}
		for _, f := range p.Syntax {
			file := relToServer(t, p.Fset.Position(f.Pos()).Filename)
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					// &http.Client{...} 直接构造
					if sel, ok := x.Type.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" && sel.Sel.Name == "Client" {
							bad = append(bad, file+": 直接构造 http.Client（必须经 httpx.NewSafeClient）")
						}
					}
				case *ast.SelectorExpr:
					if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "http" && x.Sel.Name == "DefaultClient" {
						bad = append(bad, file+": 使用 http.DefaultClient（必须经 httpx.NewSafeClient）")
					}
				}
				return true
			})
		}
	}
	for _, b := range bad {
		t.Errorf("适配器出站纪律违规（%s）：出站必须经 httpx（适配器出站纪律）", b)
	}
}

// 确认 types 包被使用（violations 结构扩展时保持类型信息需求）。
var _ = types.Interface{}
var _ = token.NoPos
