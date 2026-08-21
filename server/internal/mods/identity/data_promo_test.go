package identity

// 推广码体系测试：生成格式、懒补、双格式解析（随机码 + 旧数字 ID）、注册用码归因。

import (
	"context"
	"testing"
)

// TestPromoCodeGenFormat 推广码格式：8 位、合法字母表、每次不同。
func TestPromoCodeGenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code := genPromoCode()
		if len(code) != promoCodeLen {
			t.Fatalf("码长 = %d, want %d", len(code), promoCodeLen)
		}
		for _, c := range code {
			valid := false
			for _, a := range promoAlphabet {
				if byte(a) == byte(c) {
					valid = true
					break
				}
			}
			if !valid {
				t.Fatalf("非法字符 %q in %q", c, code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 95 {
		t.Fatalf("随机性不足：100 次仅 %d 个不同码", len(seen))
	}
}

// TestRegisterWithPromoCode 注册用推广码归因（新码体系）。
func TestRegisterWithPromoCode(t *testing.T) {
	svc, d := newUserSvc(t)
	ctx := context.Background()
	repo := NewUserRepo(d)

	// A 注册（自动得码）
	a, err := svc.repo.Register(ctx, RegisterInput{Username: "promoer", Password: "pass123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.PromoCode) != 8 {
		t.Fatalf("注册未生成推广码: %+v", a.PromoCode)
	}

	// B 用 A 的推广码注册 → l1=A
	b, err := svc.repo.Register(ctx, RegisterInput{Username: "invited", Password: "pass123", InviteCode: a.PromoCode})
	if err != nil {
		t.Fatal(err)
	}
	if b.InviteL1 != a.ID {
		t.Fatalf("l1 = %d, want %d（推广码归因失败）", b.InviteL1, a.ID)
	}

	// 双格式：旧数字 ID 也可归因（存量兼容）
	c, err := svc.repo.Register(ctx, RegisterInput{Username: "legacy", Password: "pass123", InviteCode: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.InviteL1 != 1 {
		t.Fatalf("旧数字码 l1 = %d, want 1", c.InviteL1)
	}

	// 非法码拒绝
	if _, err := svc.repo.Register(ctx, RegisterInput{Username: "bad", Password: "pass123", InviteCode: "ZZZZ9999"}); err == nil {
		t.Fatal("不存在的推广码应拒绝")
	}

	// 大小写不敏感解析
	if u := repo.ResolvePromoCode(ctx, lower(a.PromoCode)); u == nil || u.ID != a.ID {
		t.Fatalf("大小写不敏感解析失败: %v", u)
	}
}

// TestEnsurePromoCode 懒生成：存量无码用户补码；已有不覆盖。
func TestEnsurePromoCode(t *testing.T) {
	svc, d := newUserSvc(t)
	ctx := context.Background()
	repo := NewUserRepo(d)

	a, err := svc.repo.Register(ctx, RegisterInput{Username: "lazy", Password: "pass123"})
	if err != nil {
		t.Fatal(err)
	}
	// 清空码模拟存量用户
	if _, err := d.Client.User.UpdateOne(a).SetNillablePromoCode(nil).Save(ctx); err != nil {
		t.Fatal(err)
	}
	code1 := repo.EnsurePromoCode(ctx, a.ID)
	if len(code1) != 8 {
		t.Fatalf("懒生成失败: %q", code1)
	}
	// 再次调用返回同一码（不覆盖）
	if code2 := repo.EnsurePromoCode(ctx, a.ID); code2 != code1 {
		t.Fatalf("已有码被覆盖: %q → %q", code1, code2)
	}
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}
