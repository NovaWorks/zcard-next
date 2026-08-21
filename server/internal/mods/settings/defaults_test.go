package settings

// 列表默认值补齐测试（admin 参数设置空白回归防护）：
// ListSettings 必须把目录中未写入 DB 的键以默认 JSON 补齐展示，
// DB 已写入的键值优先；整体按 (group, key) 排序。

import (
	"encoding/json"
	"testing"

	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

func TestWithDefaults(t *testing.T) {
	// DB 已写入：site.name（覆盖默认值）、affiliate.enabled（覆盖默认值）
	items := []port.Item{
		{Group: "site", Key: "name", Value: json.RawMessage(`"自定义站名"`)},
		{Group: "affiliate", Key: "enabled", Value: json.RawMessage(`false`)},
	}
	out := withDefaults("", items)

	// 1) 全部分组目录键都可见（未写入的以默认值补齐）
	got := map[string]bool{}
	for _, it := range out {
		got[it.Group+"."+it.Key] = true
	}
	for _, gname := range GroupsSorted() {
		g, _ := Group(gname)
		for k := range g.Defaults {
			if !got[gname+"."+k] {
				t.Errorf("缺失目录键 %s.%s（默认值未补齐）", gname, k)
			}
		}
	}

	// 2) DB 值优先（不被目录默认覆盖）
	for _, it := range out {
		if it.Group == "site" && it.Key == "name" {
			if string(it.Value) != `"自定义站名"` {
				t.Errorf("site.name 应保留 DB 值，实际 %s", it.Value)
			}
		}
		if it.Group == "affiliate" && it.Key == "enabled" {
			if string(it.Value) != `false` {
				t.Errorf("affiliate.enabled 应保留 DB 值，实际 %s", it.Value)
			}
		}
	}

	// 3) 排序（group, key 升序）
	for i := 1; i < len(out); i++ {
		a, b := out[i-1], out[i]
		if a.Group > b.Group || (a.Group == b.Group && a.Key > b.Key) {
			t.Fatalf("顺序错误：%s.%s 排在 %s.%s 之前", a.Group, a.Key, b.Group, b.Key)
		}
	}
}

// TestWithDefaultsGroupFilter 分组过滤时不泄漏其他组键。
func TestWithDefaultsGroupFilter(t *testing.T) {
	out := withDefaults("i18n", nil)
	if len(out) == 0 {
		t.Fatal("i18n 组默认值未补齐")
	}
	for _, it := range out {
		if it.Group != "i18n" {
			t.Errorf("分组过滤泄漏：%s.%s", it.Group, it.Key)
		}
	}
}
