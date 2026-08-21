// Package captcha 图形验证码（4 位纯数字；base64Captcha 生成 + 内存 store 一次性消费）。
//
// 场景开关收敛（dujiao 同款纪律）：VerifyScene 读 settings.security.captcha_*，
// 未启用场景直接放行——业务 handler 无条件调用，零侵入。
package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mojocn/base64Captcha"
	notifyport "github.com/NovaWorks/zcard-next/server/internal/mods/notify/port"
)

// 场景枚举（对应 settings.security.captcha_* 键）。
const (
	SceneLogin       = "login"        // 登录（captcha_login）
	SceneRegister    = "register"     // 注册（captcha_register）
	SceneOrder       = "order"        // 游客下单（captcha_order）
	SceneReset       = "reset"        // 找回密码发码（captcha_reset）
	SceneAdminLogin  = "admin_login"  // 后台登录（captcha_admin_login）
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
	mu     sync.RWMutex
	driver *base64Captcha.DriverDigit
	store  base64Captcha.Store
	read   notifyport.SettingsReader // settings.security 读取（nil=全关）
}

// New 构造（4 位纯数字 100x40；内存 store 10240 条/300s 过期——单副本口径）。
func New(read notifyport.SettingsReader) *Service {
	return &Service{
		driver: base64Captcha.NewDriverDigit(40, 100, 4, 0.7, 4),
		store:  base64Captcha.NewMemoryStore(10240, 300*time.Second),
		read:   read,
	}
}

// Get 生成验证码 → {captcha_id, image_base64}。
func (s *Service) Get() (id, imageBase64 string, err error) {
	id, content, answer := s.driver.GenerateIdQuestionAnswer()
	s.store.Set(id, answer)
	item, err := s.driver.DrawCaptcha(content)
	if err != nil {
		return "", "", err
	}
	b64 := item.EncodeB64string()
	return id, b64, nil
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
	"captcha_register":     true,
	"captcha_reset":        true,
	"captcha_login":        false,
	"captcha_order":        false,
	"captcha_admin_login":  false,
}

// SceneEnabledFor 场景开关查询（前端 config 已下发；此方法供内部/测试用）。
func (s *Service) SceneEnabledFor(ctx context.Context, scene string) bool {
	key, ok := sceneKeys[scene]
	if !ok {
		return false
	}
	return s.sceneEnabled(ctx, key)
}
