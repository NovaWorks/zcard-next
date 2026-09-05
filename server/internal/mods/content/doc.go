// Package content 内容模块（ 完整落地）：轻量 CMS。
// - Banner：横幅/轮播（position/生效时间窗/跳转类型/移动端图回落）
// - Post：文章（blog/notice，多语言 JSON 字段，content 逐语言 sanitize）
// - PostCategory：文章分类（slug 唯一；有文章禁删）
//
// 发布状态机：草稿 → 发布（首发回填 published_at，取消再发布不覆盖）。
// 前台读取：生效横幅（30s 进程内缓存）、已发布文章分页、slug 详情、分类。
// 多语言回落：locale → zh_CN → 首个非空。
package content
