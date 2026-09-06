package migratev1

// 1.x 时间口径转换：MySQL DATETIME 无时区（PHP 端 APP_TIMEZONE，默认 UTC）→
// 2.0 全 UTC。源扫描一律以字符串读出（parseTime=false），经本文件显式转换。

import (
	"fmt"
	"time"
)

// srcTZ 源时区（来自 .env APP_TIMEZONE；空 = UTC）。
func srcTimezone(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}

// parseNaiveTime 解析 '2006-01-02 15:04:05[.fff]'（源 DATETIME 字符串），
// 在源时区解释后转 UTC。空串/NULL 返回零值与 false。
func parseNaiveTime(s string, tz *time.Location) (time.Time, bool) {
	if s == "" || s == "NULL" {
		return time.Time{}, false
	}
	// go-sql-driver 字符串形态；sqlite 测试源同格式
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, tz); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// mustTime 同 parseNaiveTime，解析失败时返回错误而非静默（时间列损坏应暴露）。
func mustTime(s string, tz *time.Location) (time.Time, bool, error) {
	if s == "" || s == "NULL" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, tz); err == nil {
			return t.UTC(), true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("时间字符串无法解析: %q", s)
}
