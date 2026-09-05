package media

// 上传安全三件套（，）：
// 1. 类型白名单三重校验：扩展名 + 声明 MIME + 文件头魔数
// 2. 大小限制 10MB（1.x 验证参数）
// 3. 图片重编码：Decode → 重新 Encode（剥离 EXIF/嵌入载荷/尾部数据——防图片马）
// jpeg/png/gif 全重编码；webp 标准库无编码器——仅魔数+解码校验
// （x/image/webp 解码成功即合法图像，载荷风险以白名单源头控制）

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp" // webp 解码注册（编码不可用——校验降级说明见上）
)

// 上传约束（1.x 参数平移）。
const (
	MaxSizeBytes = 10 * 1024 * 1024 // 10MB
)

// allowedTypes 白名单：ext → {mime, 魔数}。
var allowedTypes = map[string]struct {
	mime  string
	magic []byte // 文件头（前 N 字节匹配）
}{
	".jpg":  {"image/jpeg", []byte{0xFF, 0xD8, 0xFF}},
	".jpeg": {"image/jpeg", []byte{0xFF, 0xD8, 0xFF}},
	".png":  {"image/png", []byte{0x89, 'P', 'N', 'G'}},
	".webp": {"image/webp", []byte{'R', 'I', 'F', 'F'}}, // RIFF....WEBP
	".gif":  {"image/gif", []byte{'G', 'I', 'F', '8'}},
}

// ErrInvalidType 类型不在白名单（伪装扩展名同此错误——对外统一，不泄漏细节）。
var ErrInvalidType = fmt.Errorf("media.INVALID_TYPE: 仅支持 jpg/png/webp/gif 图片")

// ErrTooLarge 超大小限制。
var ErrTooLarge = fmt.Errorf("media.TOO_LARGE: 超过 10MB 上限")

// ErrNotImage 内容不是合法图片（魔数/解码失败）。
var ErrNotImage = fmt.Errorf("media.NOT_IMAGE: 文件内容不是合法图片")

// ValidateAndReencode 三件套入口：校验 + 重编码。
// 返回净化后的字节、真实 MIME、尺寸；错误为哨兵（API 层直接映射 4xx）。
func ValidateAndReencode(filename, contentType string, data []byte) (out []byte, mime string, width, height int, err error) {
	if len(data) == 0 {
		return nil, "", 0, 0, ErrNotImage
	}
	if len(data) > MaxSizeBytes {
		return nil, "", 0, 0, ErrTooLarge
	}
	ext := strings.ToLower(filepath.Ext(filename))
	spec, ok := allowedTypes[ext]
	if !ok {
		return nil, "", 0, 0, ErrInvalidType
	}
	// MIME 校验（声明为空回退魔数判定；非空必须匹配白名单项）
	if contentType != "" && contentType != "application/octet-stream" {
		if !strings.HasPrefix(contentType, spec.mime) &&
			!(contentType == "image/jpg" && ext == ".jpg") { // 常见非标别名
			return nil, "", 0, 0, ErrInvalidType
		}
	}
	// 魔数校验（webp RIFF 头后还需 WEBP 标识）
	if !bytes.HasPrefix(data, spec.magic) {
		return nil, "", 0, 0, ErrNotImage
	}
	if ext == ".webp" && (len(data) < 12 || string(data[8:12]) != "WEBP") {
		return nil, "", 0, 0, ErrNotImage
	}

	cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
	if decodeErr != nil {
		return nil, "", 0, 0, ErrNotImage
	}
	width, height = cfg.Width, cfg.Height

	// 重编码（webp 除外——标准库无编码器；解码成功即入白名单语义）
	switch ext {
	case ".jpg", ".jpeg":
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, "", 0, 0, ErrNotImage
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, "", 0, 0, fmt.Errorf("media.REENCODE_FAILED: %w", err)
		}
		return buf.Bytes(), "image/jpeg", width, height, nil
	case ".png":
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, "", 0, 0, ErrNotImage
		}
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, "", 0, 0, fmt.Errorf("media.REENCODE_FAILED: %w", err)
		}
		return buf.Bytes(), "image/png", width, height, nil
	case ".gif":
		// 保留动画：DecodeAll → EncodeAll（逐帧重编码剥离附加块）
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, "", 0, 0, ErrNotImage
		}
		var buf bytes.Buffer
		if err := gif.EncodeAll(&buf, g); err != nil {
			return nil, "", 0, 0, fmt.Errorf("media.REENCODE_FAILED: %w", err)
		}
		return buf.Bytes(), "image/gif", width, height, nil
	case ".webp":
		// 解码校验已过（DecodeConfig 成功）；原样保留（无编码器）
		return data, "image/webp", width, height, nil
	}
	return nil, "", 0, 0, ErrInvalidType
}

// SniffContentType 便捷（http.DetectContentType 标准库）。
func SniffContentType(data []byte) string { return http.DetectContentType(data) }

// SniffImage 魔数实测图片类型（外链导入用：URL 名/响应头都可能说谎，内容不会）。
// ok=false 表示不是白名单内格式（AVIF/HEIC/BMP/HTML…）。
func SniffImage(data []byte) (ext, mime string, ok bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg", "image/jpeg", true
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}):
		return ".png", "image/png", true
	case bytes.HasPrefix(data, []byte{'G', 'I', 'F', '8'}):
		return ".gif", "image/gif", true
	case len(data) >= 12 && bytes.HasPrefix(data, []byte{'R', 'I', 'F', 'F'}) && string(data[8:12]) == "WEBP":
		return ".webp", "image/webp", true
	}
	return "", "", false
}
