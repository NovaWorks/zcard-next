// Package captcha 图形验证码（位数/字符类型可配置；base64Captcha 生成 + 内存 store 一次性消费）。
//
// 场景开关收敛（dujiao 同款纪律）：VerifyScene 读 settings.security.captcha_*，
// 未启用场景直接放行——业务 handler 无条件调用，零侵入。
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
	"github.com/mojocn/base64Captcha"
)

// 场景枚举（对应 settings.security.captcha_* 键）。
const (
	SceneLogin      = "login"       // 登录（captcha_login）
	SceneRegister   = "register"    // 注册（captcha_register）
	SceneOrder      = "order"       // 游客下单（captcha_order）
	SceneReset      = "reset"       // 找回密码发码（captcha_reset）
	SceneAdminLogin = "admin_login" // 后台登录（captcha_admin_login）
)

// 场景 → settings 键映射。
var sceneKeys = map[string]string{
	SceneLogin:      "captcha_login",
	SceneRegister:   "captcha_register",
	SceneOrder:      "captcha_order",
	SceneReset:      "captcha_reset",
	SceneAdminLogin: "captcha_admin_login",
}

// 场景验证错误（业务层映射友好提示用；storefront 原样透传，admin 经 mapLoginErr 映射）。
var (
	ErrCaptchaRequired = errors.New("captcha.REQUIRED: 请输入图形验证码")
	ErrCaptchaInvalid  = errors.New("captcha.INVALID: 图形验证码错误或已过期")
)

// Service 验证码服务（生成 + 场景校验）。
type Service struct {
	store base64Captcha.Store
	read  notifyport.SettingsReader // settings.security 读取（nil=全关）
}

// Image 图片及其输入格式；格式与本次生成的答案一致。
type Image struct {
	ID          string
	ImageBase64 string
	Length      int
	Charset     string
}

// New 构造（默认 4 位纯数字；内存 store 300s 过期——单副本口径）。
func New(read notifyport.SettingsReader) *Service {
	return &Service{
		store: newMemoryStore(),
		read:  read,
	}
}

// Get 每次生成时读取设置，无需重启；独立 driver 避免并发请求修改共享配置。
func (s *Service) Get(ctx context.Context) (*Image, error) {
	length, charset := s.format(ctx)
	width := length * 25
	var driver base64Captcha.Driver
	if charset == "digit" {
		driver = base64Captcha.NewDriverDigit(40, width, length, 0.7, 4)
	} else {
		source := "abcdefghijklmnopqrstuvwxyz"
		if charset == "mixed" {
			source += "0123456789"
		}
		driver = base64Captcha.NewDriverString(40, width, 2, base64Captcha.OptionShowSineLine, length, source, nil, nil, []string{"wqy-microhei.ttc"})
	}
	id, content, answer := driver.GenerateIdQuestionAnswer()
	// 混合模式保证每张图片同时含数字和字母。
	for charset == "mixed" && (!strings.ContainsAny(answer, "0123456789") || !strings.ContainsAny(answer, "abcdefghijklmnopqrstuvwxyz")) {
		id, content, answer = driver.GenerateIdQuestionAnswer()
	}
	item, err := driver.DrawCaptcha(content)
	if err != nil {
		return nil, err
	}
	if err := s.store.Set(id, answer); err != nil {
		return nil, err
	}
	return &Image{ID: id, ImageBase64: item.EncodeB64string(), Length: length, Charset: charset}, nil
}

// format 未配置/读取失败/历史非法值均回退默认值，与设置目录一致。
func (s *Service) format(ctx context.Context) (length int, charset string) {
	length, charset = 4, "digit"
	if s.read == nil {
		return
	}
	if raw, err := s.read.GetJSON(ctx, "security", "captcha_length"); err == nil {
		var n int
		if json.Unmarshal(raw, &n) == nil && n >= 4 && n <= 8 {
			length = n
		}
	}
	if raw, err := s.read.GetJSON(ctx, "security", "captcha_charset"); err == nil {
		var v string
		if json.Unmarshal(raw, &v) == nil && (v == "digit" || v == "letter" || v == "mixed") {
			charset = v
		}
	}
	return
}

// Verify 原始校验（一次性消费：无论对错即删——防重试爆破）。
func (s *Service) Verify(id, code string) bool {
	if id == "" || code == "" {
		return false
	}
	return s.store.Verify(id, strings.TrimSpace(code), true)
}

// VerifyScene 场景校验（开关收敛）：场景未启用直接放行；
// 启用时 payload 缺失 → captcha.REQUIRED；校验失败 → captcha.INVALID。
func (s *Service) VerifyScene(ctx context.Context, scene, captchaID, captchaCode string) error {
	key, ok := sceneKeys[scene]
	if !ok {
		return errors.New("captcha.SCENE_INVALID")
	}
	if !s.sceneEnabled(ctx, key) {
		return nil // 场景未启用：放行（零侵入）
	}
	if captchaID == "" || captchaCode == "" {
		return ErrCaptchaRequired
	}
	if !s.Verify(captchaID, captchaCode) {
		return ErrCaptchaInvalid
	}
	return nil
}

// sceneEnabled 场景开关（settings.security.captcha_*）。
// 未入库时回退目录默认值（captcha_register/reset=true、login/order=false）——
// 与后台表单展示一致（ListSettings 的 withDefaults 同源）。
func (s *Service) sceneEnabled(ctx context.Context, key string) bool {
	if s.read == nil {
		return false
	}
	raw, err := s.read.GetJSON(ctx, "security", key)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return sceneDefault[key]
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return sceneDefault[key]
	}
	return v
}

// sceneDefault 目录默认值（settings.directory security 组 Defaults 同源快照）。
var sceneDefault = map[string]bool{
	"captcha_register":    true,
	"captcha_reset":       true,
	"captcha_login":       false,
	"captcha_order":       false,
	"captcha_admin_login": false,
}

// SceneEnabledFor 场景开关查询（前端 config 已下发；此方法供内部/测试用）。
func (s *Service) SceneEnabledFor(ctx context.Context, scene string) bool {
	key, ok := sceneKeys[scene]
	if !ok {
		return false
	}
	return s.sceneEnabled(ctx, key)
}
