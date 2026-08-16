// Package port 为 media 模块对外契约（零依赖包）。
package port

import "context"

// UploadInput 上传入参（内存字节；ticket 附件/未来模块复用）。
type UploadInput struct {
	Name        string // 原始文件名（扩展名参与白名单）
	ContentType string // 声明 MIME（参与三重校验）
	Data        []byte
	CategoryID  uint64 // 0 = 未分类
	UploaderID  uint64
}

// UploadResult 上传结果。
type UploadResult struct {
	ID     uint64
	Path   string // 相对存储根（/uploads/<path> 可访问）
	Width  int32
	Height int32
}

// Uploader 上传端口（业务模块消费，通道 A——经安全三件套）。
type Uploader interface {
	Upload(ctx context.Context, in UploadInput) (*UploadResult, error)
}

// Referencer 引用计数端口（catalog/content/ticket 保存时调用）。
type Referencer interface {
	// AddRefs 引用 +1（ids 去重后统一加）。
	AddRefs(ctx context.Context, ids []uint64) error
	// ReleaseRefs 引用 -1（下限 0）。
	ReleaseRefs(ctx context.Context, ids []uint64) error
}
