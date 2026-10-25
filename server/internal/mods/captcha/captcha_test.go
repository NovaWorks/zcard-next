package captcha

// 图形验证码测试：生成/一次性消费/场景开关收敛。

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"
)

// fakeSettings 测试用 settings 读取。
type fakeSettings map[string]bool

func (f fakeSettings) GetJSON(_ context.Context, group, key string) ([]byte, error) {
	if group == "security" && f[key] {
		return []byte("true"), nil
	}
	return []byte("false"), nil
}

// TestGetAndVerify 生成 + 校验 + 一次性。
func TestGetAndVerify(t *testing.T) {
	svc := New(nil)
	img, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if img.ID == "" || len(img.ImageBase64) < 100 || img.Length != 4 || img.Charset != "digit" {
		t.Fatalf("生成异常: %+v", img)
	}
	answer := svc.store.Get(img.ID, false)
	// 错码（一次性：无论对错即删）
	if svc.Verify(img.ID, "wrong") {
		t.Fatal("错码不应通过")
	}
	// 正确码也已消费
	if svc.Verify(img.ID, answer) {
		t.Fatal("验证码应一次性")
	}
}

type jsonSettings map[string]string

func (f jsonSettings) GetJSON(_ context.Context, group, key string) ([]byte, error) {
	if group != "security" {
		return nil, errors.New("unexpected settings group")
	}
	return []byte(f[key]), nil
}

func TestConfiguredImages(t *testing.T) {
	for _, charset := range []string{"digit", "letter", "mixed"} {
		for length := 4; length <= 8; length++ {
			t.Run(fmt.Sprintf("%s/%d", charset, length), func(t *testing.T) {
				svc := New(jsonSettings{"captcha_length": fmt.Sprint(length), "captcha_charset": fmt.Sprintf("%q", charset)})
				img, err := svc.Get(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if img.Length != length || img.Charset != charset {
					t.Fatalf("image format: got %d/%s, want %d/%s", img.Length, img.Charset, length, charset)
				}
				answer := svc.store.Get(img.ID, false)
				if len(answer) != length {
					t.Fatalf("answer length: got %d, want %d", len(answer), length)
				}
				digits, letters := false, false
				for _, c := range answer {
					switch {
					case c >= '0' && c <= '9':
						digits = true
					case c >= 'a' && c <= 'z':
						letters = true
					default:
						t.Fatalf("unexpected answer character %q", c)
					}
				}
				if (charset == "digit" && letters) || (charset == "letter" && digits) || (charset == "mixed" && (!digits || !letters)) {
					t.Fatalf("answer does not match charset %s", charset)
				}
				b64, ok := strings.CutPrefix(img.ImageBase64, "data:image/png;base64,")
				if !ok {
					t.Fatal("image is not a PNG data URL")
				}
				encoded, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := png.Decode(bytes.NewReader(encoded))
				if err != nil || decoded.Bounds().Dx() != length*25 || decoded.Bounds().Dy() != 40 {
					t.Fatalf("invalid image dimensions or PNG: %v", err)
				}
				if !svc.Verify(img.ID, " "+strings.ToUpper(answer)+" ") {
					t.Fatal("correct answer (case insensitive, trimmed) rejected")
				}
				if svc.Verify(img.ID, answer) {
					t.Fatal("correct answer was not consumed")
				}
			})
		}
	}
}

func TestFormatDefaults(t *testing.T) {
	for _, raw := range []string{"", "null", `"6"`, "true", "3", "9", "6.5", "6.0", "{}", "[]"} {
		svc := New(jsonSettings{"captcha_length": raw, "captcha_charset": `"letter"`})
		if length, charset := svc.format(context.Background()); length != 4 || charset != "letter" {
			t.Errorf("invalid length %s: got %d/%s", raw, length, charset)
		}
	}
	for _, raw := range []string{"", "null", `"number"`, "true", "6", "{}", "[]"} {
		svc := New(jsonSettings{"captcha_length": "8", "captcha_charset": raw})
		if length, charset := svc.format(context.Background()); length != 8 || charset != "digit" {
			t.Errorf("invalid charset %s: got %d/%s", raw, length, charset)
		}
	}
}

func TestFormatChangesKeepExistingImageValid(t *testing.T) {
	settings := jsonSettings{"captcha_length": "6", "captcha_charset": `"letter"`}
	svc := New(settings)
	old, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	answer := svc.store.Get(old.ID, false)
	settings["captcha_length"], settings["captcha_charset"] = "8", `"mixed"`
	updated, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Length != 8 || updated.Charset != "mixed" || old.Length != 6 || old.Charset != "letter" {
		t.Fatal("format did not follow current settings or changed old image metadata")
	}
	if !svc.Verify(old.ID, answer) {
		t.Fatal("settings change invalidated the existing image")
	}
}

func TestStoreImageFormatAndConcurrentGeneration(t *testing.T) {
	svc := New(jsonSettings{"captcha_length": "8", "captcha_charset": `"mixed"`})
	api := NewStoreCaptchaService(svc)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			img, err := api.GetImage(context.Background(), &emptypb.Empty{})
			if err != nil {
				t.Error(err)
				return
			}
			if img.Length != 8 || img.Charset != "mixed" || img.ImageBase64 == "" {
				t.Errorf("incomplete image response: %+v", img)
			}
			if !svc.Verify(img.CaptchaId, svc.store.Get(img.CaptchaId, false)) {
				t.Error("concurrent generation stored an incorrect answer")
			}
		}()
	}
	wg.Wait()
}

// TestVerifyScene 场景开关收敛：未启用放行；启用时缺失/错误拒绝。
func TestVerifyScene(t *testing.T) {
	ctx := context.Background()

	// 全关：任何场景放行（零侵入）
	off := New(fakeSettings{})
	if err := off.VerifyScene(ctx, SceneLogin, "", ""); err != nil {
		t.Fatalf("未启用场景应放行: %v", err)
	}

	// login 开：缺 payload 拒绝
	on := New(fakeSettings{"captcha_login": true})
	if err := on.VerifyScene(ctx, SceneLogin, "", ""); err == nil {
		t.Fatal("启用场景缺 payload 应拒绝")
	}
	// register 未启用场景放行（scene 分发验证）
	if err := on.VerifyScene(ctx, SceneRegister, "", ""); err != nil {
		t.Fatalf("register 未启用应放行: %v", err)
	}

	// 未知场景
	if err := on.VerifyScene(ctx, "unknown", "", ""); err == nil {
		t.Fatal("未知场景应报错")
	}
}

// TestSceneEnabledFor 开关查询。
func TestSceneEnabledFor(t *testing.T) {
	svc := New(fakeSettings{"captcha_order": true})
	ctx := context.Background()
	if !svc.SceneEnabledFor(ctx, SceneOrder) {
		t.Fatal("order 场景应启用")
	}
	if svc.SceneEnabledFor(ctx, SceneLogin) {
		t.Fatal("login 场景应关闭")
	}
}
