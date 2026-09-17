package media

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/mods/media/port"
	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/types/known/emptypb"
)

const VideoUploadOperation = "/zcard.api.admin.v1.AdminMediaService/UploadVideo"

func MaxVideoBytes() int64 {
	n, err := strconv.ParseInt(os.Getenv("ZCARD_VIDEO_MAX_MB"), 10, 64)
	if err != nil || n < 1 || n > 1024 {
		n = 100
	}
	return n * 1024 * 1024
}

// RegisterVideoUpload keeps multipart parsing inside the authenticated middleware.
func (s *AdminMediaService) RegisterVideoUpload(srv *khttp.Server) {
	srv.Route("/").POST("/api/v1/admin/media/video", func(c khttp.Context) error {
		khttp.SetOperation(c, VideoUploadOperation)
		h := c.Middleware(func(ctx context.Context, _ any) (any, error) {
			return s.uploadVideo(ctx, c.Request(), c.Response())
		})
		out, err := h(c, &emptypb.Empty{})
		if err != nil {
			return err
		}
		return c.Result(http.StatusOK, out)
	})
}

func (s *AdminMediaService) uploadVideo(ctx context.Context, r *http.Request, w http.ResponseWriter) (*adminv1.MediaItem, error) {
	limit := MaxVideoBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+64*1024)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	defer http.NewResponseController(w).SetReadDeadline(time.Time{})
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, errors.BadRequest("media.VIDEO_FORM", "请使用文件上传")
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || strings.ToLower(filepath.Ext(part.FileName())) != ".mp4" {
		return nil, errors.BadRequest("media.VIDEO_TYPE", "请选择 MP4 视频（H.264 / AAC）")
	}
	name := filepath.Base(part.FileName())
	dir := filepath.Join(StorageRoot, time.Now().UTC().Format("2006/01"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".video-*")
	if err != nil {
		return nil, err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(part, limit+1))
	if err != nil || n > limit {
		return nil, errors.BadRequest("media.VIDEO_SIZE", "视频上传中断或超过大小限制")
	}
	if _, err := reader.NextPart(); err != io.EOF {
		return nil, errors.BadRequest("media.VIDEO_FORM", "一次只能上传一个视频文件")
	}
	if err := validateMP4(f, n); err != nil {
		return nil, errors.BadRequest("media.VIDEO_INVALID", err.Error())
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.Chmod(0644); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	final := filepath.Join(dir, randToken()+".mp4")
	if err := os.Rename(f.Name(), final); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(StorageRoot, final)
	rel = filepath.ToSlash(rel)
	m, err := s.repo.CreateMedia(ctx, port.UploadInput{Name: name, UploaderID: adminUID(ctx)}, rel, "video/mp4", n, 0, 0, hex.EncodeToString(hash.Sum(nil)))
	if err != nil {
		_ = os.Remove(final)
		return nil, err
	}
	return toMediaPB(m), nil
}

// Walk bounded ISO-BMFF boxes without loading the video payload into memory.
func validateMP4(r io.ReaderAt, size int64) error {
	ftyp, moov, mdat, avc := false, false, false, false
	var walk func(int64, int64, int) error
	boxes := 0
	walk = func(start, end int64, depth int) error {
		if depth > 8 {
			return fmt.Errorf("视频结构层级异常")
		}
		for pos := start; pos < end; {
			boxes++
			if boxes > 100000 || end-pos < 8 {
				return fmt.Errorf("视频文件不完整")
			}
			h := make([]byte, 16)
			if _, err := r.ReadAt(h[:8], pos); err != nil {
				return err
			}
			n, header := int64(binary.BigEndian.Uint32(h)), int64(8)
			if n == 1 {
				if _, err := r.ReadAt(h[8:], pos+8); err != nil {
					return err
				}
				u := binary.BigEndian.Uint64(h[8:])
				if u > uint64(end-pos) {
					return fmt.Errorf("视频长度异常")
				}
				n, header = int64(u), 16
			} else if n == 0 {
				n = end - pos
			}
			if n < header || n > end-pos {
				return fmt.Errorf("视频文件不完整")
			}
			kind := string(h[4:8])
			if depth == 0 {
				switch kind {
				case "ftyp":
					ftyp = pos == 0 && n >= 16
				case "moov":
					moov = true
				case "mdat":
					mdat = n > header
				}
			}
			switch kind {
			case "moov", "trak", "mdia", "minf", "stbl":
				if err := walk(pos+header, pos+n, depth+1); err != nil {
					return err
				}
			case "stsd":
				if n < header+8 {
					return fmt.Errorf("缺少视频编码信息")
				}
				if err := walk(pos+header+8, pos+n, depth+1); err != nil {
					return err
				}
			case "avc1", "avc3":
				avc = n >= header+78
			case "hvc1", "hev1", "av01", "vp09":
				return fmt.Errorf("请先转换为 H.264 编码的 MP4 视频")
			}
			pos += n
		}
		return nil
	}
	if err := walk(0, size, 0); err != nil {
		return err
	}
	if !ftyp || !moov || !mdat || !avc {
		return fmt.Errorf("请选择完整的 H.264 MP4 视频文件")
	}
	return nil
}
