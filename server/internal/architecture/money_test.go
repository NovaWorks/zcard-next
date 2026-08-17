package architecture

// 金额纪律守护（铁律 1/15/16 的可执行形态——红灯即阻断合并）：
//
//	后端（铁律 1/16）：
//	  M1. 交易请求（storefront order/payment）禁止携带金额字段——订单总额与支付金额
//	      必须后端权威计算，抓包改金额在协议层即无入口（充值金额例外：档位裁决见 M3）。
//	  M2. 全部 proto 金额字段（cents/amount/price/fee/revenue/balance）必须 int64 分——
//	      金额永不 float/double（铁律 1）。
//	  M3. 管理面金额提交必须服务端边界校验：money.ValidCents/ValidSignedCents
//	      落在 catalog/reseller/wallet 三处提交入口。
//	前端（铁律 15）：
//	  F1. 两前端必须存在统一金额工具（fenToYuan/yuanToFen/formatMoney）。
//	  F2. 模板禁止裸渲染 *_cents（必须经金额工具）。
//	  F3. 禁止硬编码货币符号（符号取后台默认货币）。
//	  F4. 表单输入禁止直接绑定 *_cents（输入一律元，提交经 yuanToFen *100）。
//	  F5. 提交金额的视图必须调用 yuanToFen。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot 仓库根（server 的上一级——admin/storefront 与 server 平级）。
func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(mustRepoRoot(t))
}

// readFile 读文件（不存在则 fail）。
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败 %s: %v", path, err)
	}
	return string(raw)
}

// walkFiles 收集目录下匹配后缀的文件。
func walkFiles(t *testing.T, root string, suffixes []string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		for _, s := range suffixes {
			if strings.HasSuffix(path, s) {
				out = append(out, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描目录失败 %s: %v", root, err)
	}
	return out
}

// TestMoneyRuleM1TransactionRequestsNoAmount 交易请求禁止携带金额字段（铁律 16）。
// 抓包改金额的协议层防线：下单只传商品/数量，支付只传订单号——金额后端权威计算。
func TestMoneyRuleM1TransactionRequestsNoAmount(t *testing.T) {
	serverRoot := mustRepoRoot(t)
	for _, protoFile := range []string{
		filepath.Join(serverRoot, "api/storefront/v1/order.proto"),
		filepath.Join(serverRoot, "api/storefront/v1/payment.proto"),
	} {
		src := readFile(t, protoFile)
		// 逐 message *Request 块解析字段名
		blockRe := regexp.MustCompile(`(?s)message\s+\w*Request\s*\{([^}]*)\}`)
		fieldRe := regexp.MustCompile(`(?m)^\s*(?:repeated\s+|map<[^>]+>\s+)?[A-Za-z0-9_.]+\s+(\w+)\s*=`)
		for _, m := range blockRe.FindAllStringSubmatch(src, -1) {
			for _, f := range fieldRe.FindAllStringSubmatch(m[1], -1) {
				name := f[1]
				if strings.Contains(name, "cent") || name == "amount" || name == "total" {
					t.Errorf("%s: 交易请求字段 %q 携带金额——订单/支付金额必须后端权威计算（铁律 16），抓包改金额在协议层零入口", protoFile, name)
				}
			}
		}
	}
}

// TestMoneyRuleM2ProtoMoneyFieldsInt64 金额字段永不 float（铁律 1 的协议层形态）。
// 比率（*_percent/*_rate/rate_*）不是金额，不适用本规则。
func TestMoneyRuleM2ProtoMoneyFieldsInt64(t *testing.T) {
	apiRoot := filepath.Join(mustRepoRoot(t), "api")
	fieldRe := regexp.MustCompile(`(?m)^\s*(?:repeated\s+|map<[^>]+>\s+)?(float|double)\s+(\w*[Cc]ents\w*|amount\w*|\w*_cents|price\w*|\w*price\w*|fee\w*|revenue\w*|balance\w*)\s*=`)
	for _, f := range walkFiles(t, apiRoot, []string{".proto"}) {
		src := readFile(t, f)
		for _, m := range fieldRe.FindAllStringSubmatch(src, -1) {
			name := m[2]
			if strings.Contains(name, "percent") || strings.Contains(name, "rate") {
				continue // 比率语义（加价率/汇率等），非金额
			}
			t.Errorf("%s: 金额字段 %s 用了 %s——金额一律 int64 分（铁律 1）", f, name, m[1])
		}
	}
}

// TestMoneyRuleM3ServerBoundsValidation 管理面金额提交入口必须带服务端边界校验（铁律 16）。
func TestMoneyRuleM3ServerBoundsValidation(t *testing.T) {
	modsRoot := filepath.Join(mustRepoRoot(t), "internal/mods")
	for _, f := range []string{
		filepath.Join(modsRoot, "catalog/data_service.go"),
		filepath.Join(modsRoot, "reseller/data_service.go"),
		filepath.Join(modsRoot, "wallet/data_service.go"),
	} {
		src := readFile(t, f)
		if !strings.Contains(src, "money.ValidCents") && !strings.Contains(src, "money.ValidSignedCents") {
			t.Errorf("%s: 管理面金额提交入口缺少 money.ValidCents/ValidSignedCents 边界校验（铁律 16）", f)
		}
	}
}

// TestMoneyRuleF1FrontendUtils 前端统一金额工具必须存在（铁律 15）。
func TestMoneyRuleF1FrontendUtils(t *testing.T) {
	root := repoRoot(t)
	utils := map[string][]string{
		filepath.Join(root, "admin/src/utils/money.ts"): {
			"fenToYuan", "yuanToFen", "formatMoney", "formatSignedMoney", "centsToYuan", "initCurrency",
		},
		filepath.Join(root, "storefront/src/api/client.ts"): {
			"fenToYuan", "yuanToFen", "formatMoney", "formatSignedMoney", "centsToYuan", "initCurrency",
		},
	}
	for path, funcs := range utils {
		src := readFile(t, path)
		for _, fn := range funcs {
			if !strings.Contains(src, "export function "+fn) {
				t.Errorf("%s: 缺少统一金额工具 %s（铁律 15——全站金额转换必须收口）", path, fn)
			}
		}
	}
}

// TestMoneyRuleF2NoRawCentsRender 模板禁止裸渲染 *_cents（必须经金额工具，铁律 15）。
func TestMoneyRuleF2NoRawCentsRender(t *testing.T) {
	root := repoRoot(t)
	rawRe := regexp.MustCompile(`\{\{\s*[^{}()]*_cents[^{}()]*\}\}`)
	for _, dir := range []string{"admin/src", "storefront/src"} {
		for _, f := range walkFiles(t, filepath.Join(root, dir), []string{".vue"}) {
			src := readFile(t, f)
			for _, m := range rawRe.FindAllString(src, -1) {
				t.Errorf("%s: 裸渲染金额 %q——显示必须经 formatMoney/fenToYuan 除以 100（铁律 15）", f, m)
			}
		}
	}
}

// TestMoneyRuleF3NoHardcodedSymbol 禁止硬编码货币符号（符号取后台默认货币，铁律 15）。
func TestMoneyRuleF3NoHardcodedSymbol(t *testing.T) {
	root := repoRoot(t)
	allow := map[string]bool{
		filepath.Join(root, "admin/src/utils/money.ts"):     true,
		filepath.Join(root, "storefront/src/api/client.ts"): true,
	}
	for _, dir := range []string{"admin/src", "storefront/src"} {
		for _, f := range walkFiles(t, filepath.Join(root, dir), []string{".vue", ".ts"}) {
			if allow[f] {
				continue
			}
			if src := readFile(t, f); strings.Contains(src, "¥") {
				t.Errorf("%s: 硬编码货币符号——符号必须取后台默认货币（utils/money 的 formatMoney）", f)
			}
		}
	}
}

// TestMoneyRuleF4NoCentsBoundInputs 表单输入禁止直接绑定 *_cents（输入一律元，铁律 15）。
func TestMoneyRuleF4NoCentsBoundInputs(t *testing.T) {
	root := repoRoot(t)
	inputRe := regexp.MustCompile(`v-model(?::\w+)?="[^"]*_cents"`)
	for _, dir := range []string{"admin/src", "storefront/src"} {
		for _, f := range walkFiles(t, filepath.Join(root, dir), []string{".vue"}) {
			src := readFile(t, f)
			for _, m := range inputRe.FindAllString(src, -1) {
				t.Errorf("%s: 输入框直接绑定分 %q——表单输入必须为元，提交经 yuanToFen 乘 100（铁律 15）", f, m)
			}
		}
	}
}

// TestMoneyRuleF5SubmitViewsConvert 提交金额的视图必须经 yuanToFen 转换（铁律 15）。
func TestMoneyRuleF5SubmitViewsConvert(t *testing.T) {
	root := repoRoot(t)
	for _, f := range []string{
		filepath.Join(root, "admin/src/views/product/index.vue"),
		filepath.Join(root, "admin/src/views/wallet/index.vue"),
	} {
		src := readFile(t, f)
		if !strings.Contains(src, "yuanToFen(") {
			t.Errorf("%s: 提交金额的视图未调用 yuanToFen（元 → 分 *100）", f)
		}
	}
}

// TestMoneyRuleSelfCheck 人为漏转样本必须触发红灯（测试有效性的自证）。
func TestMoneyRuleSelfCheck(t *testing.T) {
	rawRe := regexp.MustCompile(`\{\{\s*[^{}()]*_cents[^{}()]*\}\}`)
	if !rawRe.MatchString("{{ row.price_cents }}") {
		t.Fatal("自证失败：裸渲染样本未被捕获")
	}
	if rawRe.MatchString("{{ formatMoney(row.price_cents) }}") {
		t.Fatal("自证失败：经工具渲染的样本被误伤")
	}
	inputRe := regexp.MustCompile(`v-model(?::\w+)?="[^"]*_cents"`)
	if !inputRe.MatchString(`v-model:value="formData.price_cents"`) {
		t.Fatal("自证失败：分输入框样本未被捕获")
	}
	if inputRe.MatchString(`v-model:value="formData.price_yuan"`) {
		t.Fatal("自证失败：元输入框样本被误伤")
	}
}
