package media

// Identify the encoding from bytes, never from a browser MIME or a filename.
// Raster formats with encoders are sanitized; animated containers retain frames.
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

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const MaxSizeBytes = 10 * 1024 * 1024
const maxImagePixels = 40_000_000
const maxAnimationPixels = 100_000_000
const SupportedImages = "JPG/JPEG/JFIF、PNG/APNG、GIF、WebP、AVIF、BMP、ICO、SVG、TIFF、HEIC/HEIF"

var ErrInvalidType = fmt.Errorf("media.INVALID_TYPE: 支持 %s 图片", SupportedImages)
var ErrTooLarge = fmt.Errorf("media.TOO_LARGE: 超过 10MB 上限")
var ErrNotImage = fmt.Errorf("media.NOT_IMAGE: 文件内容不是合法图片")
var ErrImageDimensions = fmt.Errorf("media.DIMENSIONS: 图片尺寸或动画总像素过大")

func validDimensions(w, h int) bool { return w > 0 && h > 0 && int64(w)*int64(h) <= maxImagePixels }

// Retain the executable-extension rejection, but allow extensionless images and
// generic download names. A misleading *image* suffix is corrected on storage.
func allowedFilename(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case "", ".jpg", ".jpeg", ".jfif", ".pjpeg", ".pjp", ".png", ".apng", ".gif", ".webp", ".avif", ".bmp", ".dib", ".ico", ".svg", ".tif", ".tiff", ".heic", ".heif", ".bin", ".dat", ".tmp", ".blob", ".download":
		return true
	}
	return false
}

func ValidateAndReencode(filename, contentType string, data []byte) (out []byte, mime string, width, height int, err error) {
	if len(data) == 0 {
		return nil, "", 0, 0, ErrNotImage
	}
	if len(data) > MaxSizeBytes {
		return nil, "", 0, 0, ErrTooLarge
	}
	if !allowedFilename(filename) {
		return nil, "", 0, 0, ErrInvalidType
	}
	ext, mime, ok := SniffImage(data)
	if !ok {
		return nil, "", 0, 0, ErrNotImage
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if ct != "" && ct != "application/octet-stream" && ct != "binary/octet-stream" && !strings.HasPrefix(ct, "image/") && !(ext == ".svg" && (ct == "text/plain" || ct == "text/xml" || ct == "application/xml")) {
		return nil, "", 0, 0, ErrInvalidType
	}
	switch ext {
	case ".avif", ".heic", ".heif":
		if !validImageContainer(data) {
			return nil, "", 0, 0, ErrNotImage
		}
		return data, mime, 0, 0, nil
	case ".svg":
		if !validSVG(data) {
			return nil, "", 0, 0, ErrNotImage
		}
		return data, mime, 0, 0, nil
	case ".ico":
		w, h, err := validateICO(data)
		return data, mime, w, h, err
	case ".webp":
		clean, w, h, err := validateWebP(data)
		return clean, mime, w, h, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, ErrNotImage
	}
	width, height = cfg.Width, cfg.Height
	if !validDimensions(width, height) {
		return nil, "", 0, 0, ErrImageDimensions
	}
	if ext == ".gif" {
		if err := checkGIFBudget(data); err != nil {
			return nil, "", 0, 0, err
		}
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, "", 0, 0, ErrNotImage
		}
		var buf bytes.Buffer
		if err = gif.EncodeAll(&buf, g); err != nil {
			return nil, "", 0, 0, err
		}
		return buf.Bytes(), mime, width, height, nil
	}
	if ext == ".png" {
		end, animated, err := checkPNG(data)
		if err != nil {
			return nil, "", 0, 0, err
		}
		// Standard PNG decoders only decode the fallback frame. Retain APNG chunks.
		if animated {
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				return nil, "", 0, 0, ErrNotImage
			}
			return data[:end], mime, width, height, nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, ErrNotImage
	}
	var buf bytes.Buffer
	if ext == ".jpg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	} else {
		// TIFF/BMP become browser-compatible PNGs, including transparency.
		err = png.Encode(&buf, img)
		mime = "image/png"
	}
	if err != nil {
		return nil, "", 0, 0, err
	}
	return buf.Bytes(), mime, width, height, nil
}
func SniffContentType(data []byte) string { return http.DetectContentType(data) }

// SniffImage identifies a candidate; ValidateAndReencode validates its structure.
func SniffImage(data []byte) (ext, mime string, ok bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return ".jpg", "image/jpeg", true
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return ".png", "image/png", true
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return ".gif", "image/gif", true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp", "image/webp", true
	case bytes.HasPrefix(data, []byte("BM")):
		return ".bmp", "image/bmp", true
	case bytes.HasPrefix(data, []byte{0, 0, 1, 0}):
		return ".ico", "image/x-icon", true
	case bytes.HasPrefix(data, []byte{'I', 'I', 42, 0}) || bytes.HasPrefix(data, []byte{'M', 'M', 0, 42}):
		return ".tiff", "image/tiff", true
	}
	if ext, mime := sniffImageContainer(data); ext != "" {
		return ext, mime, true
	}
	if validSVG(data) {
		return ".svg", "image/svg+xml", true
	}
	return "", "", false
}

func imageExtension(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	case "image/x-icon":
		return ".ico"
	case "image/svg+xml":
		return ".svg"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	}
	return ""
}
