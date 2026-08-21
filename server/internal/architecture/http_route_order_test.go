package architecture

// HTTP 路由注册顺序守护（架构测试规则补充；wallet 提现/礼品卡 CODEC 400 回归防护）：
// protoc-gen-go-http 按 proto 方法声明顺序生成 r.Handle 注册，gorilla/mux 按注册顺序
// 匹配（先注册先命中）。若含通配段的路由（/wallet/{user_id}）先于同形静态路由
// （/wallet/withdrawals、/wallet/giftcard-batches）注册，静态路由将被通配路由吞掉，
// 请求落错 handler 后在 BindVars 抛 CODEC 400。因此任何 *_http.pb.go 内禁止
// 「通配路由注册在静态路由之前且二者可命中同一路径」——proto 中静态路径方法须声明在前。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type httpRoute struct{ method, path string }

var routeHandleRE = regexp.MustCompile(`r\.Handle\("([A-Z*]+)",\s*"([^"]+)"`)

// routeShadowConflicts 返回被更早注册的同形通配路由遮蔽的静态路由。
// W 遮蔽 S ⟺ 同 method、段数相同、且 W 的每个通配段与 S 对应段可同时命中
// （W[i] 为 {var} 或 W[i]==S[i]）。
func routeShadowConflicts(routes []httpRoute) []string {
	var out []string
	for i, s := range routes {
		if strings.Contains(s.path, "{") {
			continue
		}
		ssegs := strings.Split(strings.Trim(s.path, "/"), "/")
		for j := 0; j < i; j++ {
			w := routes[j]
			if w.method != "*" && w.method != s.method {
				continue
			}
			wsegs := strings.Split(strings.Trim(w.path, "/"), "/")
			if len(wsegs) != len(ssegs) {
				continue
			}
			shadow := true
			for k := range wsegs {
				if strings.Contains(wsegs[k], "{") {
					continue
				}
				if wsegs[k] != ssegs[k] {
					shadow = false
					break
				}
			}
			if shadow {
				out = append(out, w.method+" "+w.path+" → "+s.method+" "+s.path)
			}
		}
	}
	return out
}

// 自证用例：wallet 修复前（通配先注册）必红、修复后（静态先注册）必绿。
func TestRouteShadowConflictsSelfProof(t *testing.T) {
	before := []httpRoute{
		{"GET", "/api/v1/admin/wallet/{user_id}"},
		{"POST", "/api/v1/admin/wallet/{user_id}/adjust"},
		{"GET", "/api/v1/admin/wallet/{user_id}/transactions"},
		{"GET", "/api/v1/admin/wallet/withdrawals"},
		{"GET", "/api/v1/admin/wallet/giftcard-batches"},
	}
	if got := routeShadowConflicts(before); len(got) != 2 {
		t.Fatalf("修复前应检出 2 个遮蔽冲突，实际 %v", got)
	}
	after := []httpRoute{
		{"GET", "/api/v1/admin/wallet/withdrawals"},
		{"POST", "/api/v1/admin/wallet/withdrawals/{id}/review"},
		{"POST", "/api/v1/admin/wallet/withdrawals/{id}/pay"},
		{"POST", "/api/v1/admin/wallet/giftcard-batches"},
		{"GET", "/api/v1/admin/wallet/giftcard-batches"},
		{"GET", "/api/v1/admin/wallet/{user_id}"},
		{"POST", "/api/v1/admin/wallet/{user_id}/adjust"},
		{"GET", "/api/v1/admin/wallet/{user_id}/transactions"},
	}
	if got := routeShadowConflicts(after); len(got) != 0 {
		t.Fatalf("修复后不应检出冲突，实际 %v", got)
	}
}

// 全量扫描：所有提交的 *_http.pb.go 不得存在通配遮蔽。
func TestGeneratedRoutesNoShadowing(t *testing.T) {
	files, err := filepath.Glob("../../api/*/v1/*_http.pb.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("未找到 api/*/v1/*_http.pb.go（相对路径错误？）")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var routes []httpRoute
		for _, m := range routeHandleRE.FindAllStringSubmatch(string(data), -1) {
			routes = append(routes, httpRoute{method: m[1], path: m[2]})
		}
		if bad := routeShadowConflicts(routes); len(bad) > 0 {
			t.Errorf("%s 存在通配路由遮蔽静态路由（需调整 proto 方法声明顺序，静态在前）：\n  %s",
				f, strings.Join(bad, "\n  "))
		}
	}
}
