package adapter

// acg_spec.go — acg-faka 多规格模型（race 种类 + sku 加价规格）解析与组合编码。
//
// 契约（对照 acg-faka 源码 app/Util/Ini.php toArray + Bind/Order.php parseConfig）：
//   - 商品 config 为 INI 字符串：段落 [category]/[sku]/[wholesale]/[category_wholesale]
//   - 行格式 k=v（恰好一个 '='，点号嵌套：[sku] 下 `区域.QQ区=2` → {区域:{QQ区:2}}）
//   - 下单必传：race=种类名（[category] 配置时）+ sku[规格名]=选项（[sku] 配置时）
//
// 组合编码（product_skus.upstream_sku_id，varchar(64)）：
//   race|规格名=选项;规格名=选项   （规格名按字母序，确定性；无 race 时前导 |）
// 禁字符 |=;, 出现在种类/规格名/选项时该商品不支持规格同步（防御命名边界）。

import (
	"fmt"
	"sort"
	"strings"
)

// MaxAcgCombos 组合数护栏：race × 各规格选项的笛卡尔积超限 → 商品不同步上架。
const MaxAcgCombos = 64

// acgSpecDelims 编码保留字符（种类/规格名/选项含任一 → 不支持）。
const acgSpecDelims = "|=;,"

// acgSpecINI 解析后的规格配置。
type acgSpecINI struct {
	Race map[string]string   // [category] 种类名 → 单价（元字符串）
	Sku  map[string]map[string]string // [sku] 规格名 → 选项 → 加价（元字符串）
}

// specDelimOK 种类/规格名/选项不含编码保留字符。
func specDelimOK(s string) bool { return !strings.ContainsAny(s, acgSpecDelims) }

// parseAcgINI 极简 INI 解析（段落 + k=v + 点号嵌套一层；与 Ini::toArray 同语义）。
// 仅提取 [category] 与 [sku] 两段（wholesale 批量价不参与下游零售定价）。
func parseAcgINI(content string) (*acgSpecINI, error) {
	out := &acgSpecINI{}
	section := ""
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // acg 侧对该行会抛错；容忍跳过（不阻断整品同步）
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch section {
		case "category":
			if out.Race == nil {
				out.Race = map[string]string{}
			}
			out.Race[key] = val
		case "sku":
			// 点号嵌套：规格名.选项=加价
			dot := strings.Index(key, ".")
			if dot <= 0 || dot == len(key)-1 {
				continue
			}
			name := strings.TrimSpace(key[:dot])
			opt := strings.TrimSpace(key[dot+1:])
			if name == "" || opt == "" {
				continue
			}
			if out.Sku == nil {
				out.Sku = map[string]map[string]string{}
			}
			if out.Sku[name] == nil {
				out.Sku[name] = map[string]string{}
			}
			out.Sku[name][opt] = val
		}
	}
	return out, nil
}

// acgCombo 一个可购组合。
type acgCombo struct {
	Race     string            // 种类名（空=无 race）
	Choices  map[string]string // 规格名 → 选项
	Code     string            // 编码（upstream_sku_id）
	Name     string            // 展示名（种类 · 选项1 · 选项2）
	BaseCents int64            // race 价（无 race=商品价，由调用方填）
	AddCents int64             // Σ规格加价（分）
}

// Encode 生成紧凑编码：race|规格名=选项;…（规格名按字母序）。
func (c *acgCombo) Encode() string {
	keys := make([]string, 0, len(c.Choices))
	for k := range c.Choices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(c.Race)
	b.WriteString("|")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(";")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(c.Choices[k])
	}
	return b.String()
}

// ComboName 展示名：种类 · 选项值…（选项按规格名字母序）。
func (c *acgCombo) ComboName() string {
	keys := make([]string, 0, len(c.Choices))
	for k := range c.Choices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	if c.Race != "" {
		parts = append(parts, c.Race)
	}
	for _, k := range keys {
		parts = append(parts, c.Choices[k])
	}
	return strings.Join(parts, " · ")
}

// buildAcgCombos 笛卡尔积展开（race × 各规格选项）。baseCents 用于无 race 的基准价。
// 返回 nil 表示无规格（[category]/[sku] 均未配置）；组合超护栏返回错误。
func buildAcgCombos(ini *acgSpecINI, baseCents int64) ([]acgCombo, error) {
	if ini == nil || (len(ini.Race) == 0 && len(ini.Sku) == 0) {
		return nil, nil
	}
	// 禁字符检查（编码安全前提）
	for r := range ini.Race {
		if !specDelimOK(r) {
			return nil, fmt.Errorf("acg spec: 种类名 %q 含保留字符，不支持规格同步", r)
		}
	}
	for name, opts := range ini.Sku {
		if !specDelimOK(name) {
			return nil, fmt.Errorf("acg spec: 规格名 %q 含保留字符，不支持规格同步", name)
		}
		for opt := range opts {
			if !specDelimOK(opt) {
				return nil, fmt.Errorf("acg spec: 选项 %q 含保留字符，不支持规格同步", opt)
			}
		}
	}
	// 规格名固定顺序（字母序）；race 名也按字母序保证组合顺序确定
	raceNames := make([]string, 0, len(ini.Race))
	for r := range ini.Race {
		raceNames = append(raceNames, r)
	}
	sort.Strings(raceNames)
	if len(raceNames) == 0 {
		raceNames = []string{""} // 无 race：单基准
	}
	specNames := make([]string, 0, len(ini.Sku))
	for n := range ini.Sku {
		specNames = append(specNames, n)
	}
	sort.Strings(specNames)

	// 选项按字母序展开
	optsBySpec := make([][]string, len(specNames))
	for i, n := range specNames {
		opts := make([]string, 0, len(ini.Sku[n]))
		for o := range ini.Sku[n] {
			opts = append(opts, o)
		}
		sort.Strings(opts)
		optsBySpec[i] = opts
	}

	// 组合数护栏
	total := len(raceNames)
	for _, opts := range optsBySpec {
		total *= len(opts)
	}
	if total > MaxAcgCombos {
		return nil, fmt.Errorf("acg spec: 组合数 %d 超护栏 %d", total, MaxAcgCombos)
	}

	var out []acgCombo
	for _, race := range raceNames {
		// 基准价：race 价（元字符串→分）；无 race 用商品价
		base := baseCents
		if race != "" {
			base = parseYuanToCents(ini.Race[race])
		}
		// 笛卡尔积（递归展开 specNames 各选项）
		var walk func(idx int, choices map[string]string, addCents int64)
		walk = func(idx int, choices map[string]string, addCents int64) {
			if idx == len(specNames) {
				c := acgCombo{Race: race, Choices: choices, BaseCents: base, AddCents: addCents}
				c.Code = c.Encode()
				c.Name = c.ComboName()
				// 拷贝 choices（walk 复用 map）
				cp := make(map[string]string, len(choices))
				for k, v := range choices {
					cp[k] = v
				}
				c.Choices = cp
				out = append(out, c)
				return
			}
			name := specNames[idx]
			for _, opt := range optsBySpec[idx] {
				choices[name] = opt
				walk(idx+1, choices, addCents+parseYuanToCents(ini.Sku[name][opt]))
			}
			delete(choices, name)
		}
		walk(0, map[string]string{}, 0)
	}
	return out, nil
}

// DecodeAcgSpecCode 反解编码 → race + 规格选择（采购提交时还原表单字段）。
// 非法格式返回错误（调用方应视为无规格处理并告警）。
func DecodeAcgSpecCode(code string) (race string, choices map[string]string, err error) {
	if code == "" {
		return "", nil, nil
	}
	bar := strings.Index(code, "|")
	if bar < 0 {
		return "", nil, fmt.Errorf("acg spec: 编码缺少 '|' 分隔: %q", code)
	}
	race = code[:bar]
	choices = map[string]string{}
	rest := code[bar+1:]
	if rest == "" {
		return race, choices, nil
	}
	for _, kv := range strings.Split(rest, ";") {
		eq := strings.Index(kv, "=")
		if eq <= 0 {
			return "", nil, fmt.Errorf("acg spec: 编码段非法: %q", kv)
		}
		choices[kv[:eq]] = kv[eq+1:]
	}
	return race, choices, nil
}

// AcgSpecFormFields 编码 → 表单字段（race + sku[规格名]，键序确定供签名一致）。
func AcgSpecFormFields(code string) (map[string]string, error) {
	race, choices, err := DecodeAcgSpecCode(code)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	if race != "" {
		fields["race"] = race
	}
	for k, v := range choices {
		fields["sku["+k+"]"] = v
	}
	return fields, nil
}
