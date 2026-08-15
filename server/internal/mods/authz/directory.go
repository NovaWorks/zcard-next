package authz

// 权限点目录（P0-03 任务书 T1：权限目录自动生成，替代 server/middleware 手工映射）。
//
// 设计（三源合一，杜绝双源漂移）：
//   - 各模块经 Perm 声明权限点：Op（proto 方法全名）+ Method/Path（HTTP 注解）同条给出
//   - 启动时 Reconcile 用 http.Server.WalkRoute 提取的真实路由表逐条对账——
//     admin 前缀路由未声明且非 Public → 启动失败（fail-fast，主文档 §5.14「新增路由
//     未挂角色 = 仅超管可见」的强化版：未声明权限点直接拒绝启动）
//   - middleware 运行时按 Op 查目录（声明同源，path↔op 不漂移）；miss = 403 + 告警
//
// 敏感权限点（§5.20.4）用 AdminOnly 标记：不进非超管内置角色种子，仅 super_admin 的 * 覆盖。

import (
	"fmt"
	"sort"
	"sync"
)

// Perm 权限点声明（每条 admin RPC 一条；RPC 与 HTTP 一一对应）。
type Perm struct {
	Code   string // 权限点编码（域:动作，如 settings:read）
	Desc   string // 中文描述（权限树展示）
	Domain string // 所属域（树分组；缺省取 Code 前缀）
	Op     string // proto 方法全名（middleware 查询键）
	Method string // HTTP 方法（路由对账）
	Path   string // HTTP 路径模板（路由对账）
	Public bool   // 免鉴权（仅登录等极少数）
	// AdminOnly 敏感权限点：不进入非超管内置角色种子（card:view_content 等）
	AdminOnly bool
}

var (
	declMu   sync.RWMutex
	declared = []Perm{}            // 全部声明（同 Code 可挂多条路由，如读/写分开的 REST 路由）
	byOpSeen = map[string]string{} // op → code（冲突检测）
)

// Declare 注册权限点声明（模块 init/providers 装配期调用；同 Code 可多次声明
// 以覆盖多条路由，同 Op 冲突 panic；仅登记目录无路由的敏感权限点可省略 Op）。
func Declare(ps ...Perm) {
	declMu.Lock()
	defer declMu.Unlock()
	for _, p := range ps {
		if p.Code == "" {
			panic("authz: 权限点声明缺少 Code")
		}
		if p.Op != "" {
			if prev, dup := byOpSeen[p.Op]; dup && prev != p.Code {
				panic(fmt.Sprintf("authz: 权限点 Op 冲突：%s 已属于 %s，又声明为 %s", p.Op, prev, p.Code))
			}
			byOpSeen[p.Op] = p.Code
		}
		if p.Domain == "" {
			p.Domain = domainOf(p.Code)
		}
		declared = append(declared, p)
	}
}

func domainOf(code string) string {
	for i := 0; i < len(code); i++ {
		if code[i] == ':' {
			return code[:i]
		}
	}
	return code
}

// RouteInfo 路由对账输入（与 kratos http.RouteInfo 同构，避免 authz 反向依赖 transport）。
type RouteInfo struct {
	Method string
	Path   string
}

// Directory 权限目录（构建后只读，并发安全）。
type Directory struct {
	perms   map[string]Perm   // code → Perm
	byOp    map[string]string // op → code
	byRoute map[string]string // "METHOD /path" → code
}

// BuildDirectory 以当前全局声明构建目录。
func BuildDirectory() *Directory {
	declMu.RLock()
	defer declMu.RUnlock()
	d := &Directory{
		perms:   make(map[string]Perm, len(declared)),
		byOp:    make(map[string]string, len(declared)),
		byRoute: make(map[string]string, len(declared)),
	}
	for _, p := range declared {
		if _, seen := d.perms[p.Code]; !seen {
			d.perms[p.Code] = p // 元信息取首条定义
		}
		if p.Op != "" {
			d.byOp[p.Op] = p.Code
		}
		if p.Method != "" && p.Path != "" {
			d.byRoute[p.Method+" "+p.Path] = p.Code
		}
	}
	return d
}

// Reconcile 路由对账：admin 前缀路由必须被声明覆盖（Public 或权限点），否则返回错误
// （启动 fail-fast）。非 admin 路由（storefront/supply/回调）直接放行。
func (d *Directory) Reconcile(routes []RouteInfo, adminPrefix string) error {
	for _, r := range routes {
		if !hasPrefix(r.Path, adminPrefix) {
			continue
		}
		_, ok := d.byRoute[r.Method+" "+r.Path]
		if !ok {
			return fmt.Errorf("authz: 管理路由未声明权限点（拒绝启动）：%s %s——请在模块内 authz.Declare 登记", r.Method, r.Path)
		}
	}
	return nil
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// PermissionForOp middleware 查询：返回 (code, public, ok)。
// operation 归一化：HTTP transport 的 operation 带前导斜杠（/pkg.Svc/Method），
// 声明侧统一不带——两种形态均兼容。
func (d *Directory) PermissionForOp(op string) (code string, public bool, ok bool) {
	if len(op) > 0 && op[0] == '/' {
		op = op[1:]
	}
	c, exists := d.byOp[op]
	if !exists {
		return "", false, false
	}
	return c, d.perms[c].Public, true
}

// Perm 取权限点定义。
func (d *Directory) Perm(code string) (Perm, bool) {
	p, ok := d.perms[code]
	return p, ok
}

// Tree 权限树节点（GetPermissionTree API 输出）。
type TreeNode struct {
	Domain string
	Label  string
	Perms  []Perm
}

// Tree 按域分组导出（域与权限点均按字典序，展示稳定）。
func (d *Directory) Tree() []TreeNode {
	domains := map[string][]Perm{}
	for _, p := range d.perms {
		domains[p.Domain] = append(domains[p.Domain], p)
	}
	names := make([]string, 0, len(domains))
	for k := range domains {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]TreeNode, 0, len(names))
	for _, name := range names {
		perms := domains[name]
		sort.Slice(perms, func(i, j int) bool { return perms[i].Code < perms[j].Code })
		out = append(out, TreeNode{Domain: name, Label: domainLabel(name), Perms: perms})
	}
	return out
}

// ValidateCodes 校验权限点集全部存在于目录（防脏数据写入角色）。
func (d *Directory) ValidateCodes(codes []string) error {
	for _, c := range codes {
		if _, ok := d.perms[c]; !ok {
			return fmt.Errorf("authz: 权限点 %q 不在目录内（拒绝写入角色）", c)
		}
	}
	return nil
}

// Codes 全部权限点编码（种子与覆盖测试用）。
func (d *Directory) Codes() []string {
	out := make([]string, 0, len(d.perms))
	for c := range d.perms {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// NonAdminOnlyCodes 非敏感权限点（内置角色种子候选）。
func (d *Directory) NonAdminOnlyCodes() []string {
	out := make([]string, 0, len(d.perms))
	for c, p := range d.perms {
		if !p.AdminOnly && !p.Public {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// AdminOnlyCodes 敏感权限点清单。
func (d *Directory) AdminOnlyCodes() []string {
	out := make([]string, 0)
	for c, p := range d.perms {
		if p.AdminOnly {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func domainLabel(domain string) string {
	if l, ok := domainLabels[domain]; ok {
		return l
	}
	return domain
}

// domainLabels 域中文名（权限树分组标题；新域在此补充）。
var domainLabels = map[string]string{
	"auth":        "认证",
	"settings":    "设置中心",
	"authz":       "权限管理",
	"identity":    "员工管理",
	"catalog":     "商品目录",
	"inventory":   "卡密库存",
	"order":       "订单",
	"payment":     "支付",
	"wallet":      "钱包",
	"supply":      "上游货源",
	"procurement": "采购",
	"supplier":    "对外供货",
	"reseller":    "分站",
	"affiliate":   "分销",
	"coupon":      "营销",
	"ticket":      "工单",
	"media":       "素材",
	"dashboard":   "工作台",
	"notify":      "通知",
	"audit":       "审计",
	"system":      "系统",
}
