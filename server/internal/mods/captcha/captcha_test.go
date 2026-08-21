package captcha

// 图形验证码测试：生成/一次性消费/场景开关收敛。

import (
	"context"
	"testing"
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
	id, img, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || len(img) < 100 {
		t.Fatalf("生成异常: id=%q imgLen=%d", id, len(img))
	}
	// 错码（一次性：无论对错即删）
	if svc.Verify(id, "0000") {
		t.Fatal("错码不应通过")
	}
	// 正确码也已消费
	if svc.Verify(id, "0000") {
		t.Fatal("验证码应一次性")
	}
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
