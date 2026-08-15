package schema

import "time"

// nowUTC 统一 UTC 时间（规划 §3.4：时间一律 time.Time（UTC 存储））。
func nowUTC() time.Time { return time.Now().UTC() }
