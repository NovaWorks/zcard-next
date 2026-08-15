package schema

// 时间字段类型纪律自检（防止新增裸 field.Time 回退到 MySQL timestamp）：
// 所有 field.Time(...) 声明必须携带 .SchemaType(mysqlTime)，
// 保证 MySQL 落到 datetime(3)（《数据库架构设计.md》§0 类型映射）。

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestTimeFieldsUseMysqlType(t *testing.T) {
	decl := regexp.MustCompile(`field\.Time\("[^"\n]+"\)`)
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || name == "schema_test.go" || name == "timefield.go" {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(content)
		for _, loc := range decl.FindAllStringIndex(s, -1) {
			after := s[loc[1]:]
			if !strings.HasPrefix(after, ".SchemaType") {
				offenders = append(offenders, name+": "+s[loc[0]:loc[1]])
			}
		}
	}
	for _, o := range offenders {
		t.Errorf("裸 field.Time（缺 .SchemaType(mysqlTime)，MySQL 将回退 timestamp 秒精度）：%s", o)
	}
	if len(offenders) == 0 {
		t.Log("全部 time 字段已声明 mysqlTime ✓")
	}
}
