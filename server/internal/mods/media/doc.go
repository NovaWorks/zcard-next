// Package media 素材库模块（M1）：分类树、上传（本地 uploads/ 或 S3 兼容）、
// 改名、批量移动/删除、外链导入、图片引用计数。
//
// 表：media / media_categories。上传安全：类型白名单 + 图片重编码（防图片马）+ 大小限制。
package media
