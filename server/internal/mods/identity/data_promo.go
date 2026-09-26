package identity

// 推广码（promo_code）：8 位随机字符串邀请标识，替代裸 user_id（防枚举）。
// 存量用户懒生成（登录/查询时补）；注册自动生成。

import (
	"context"
	"crypto/rand"
	"strconv"
	"strings"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/user"
)

// promoAlphabet 推广码字母表（剔除 I/O/0/1 防手抄混淆——dujiao 同款纪律）。
const promoAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const promoCodeLen = 8

// genPromoCode 生成 8 位随机推广码（crypto/rand）。
func genPromoCode() string {
	buf := make([]byte, promoCodeLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败极罕见：回退时间戳低位（仍满足唯一性重试语义）
		return "FALLBACK"
	}
	for i, b := range buf {
		buf[i] = promoAlphabet[int(b)%len(promoAlphabet)]
	}
	return string(buf)
}

// EnsurePromoCode 用户推广码懒生成（空则补；唯一冲突重试 8 次）。
// 返回有效推广码（已有直接返回）。Me/Login/推广中心等入口调用。
func (r *UserRepo) EnsurePromoCode(ctx context.Context, userID uint64) string {
	client := data.Client(ctx, r.data)
	u, err := client.User.Get(ctx, userID)
	if err != nil {
		return ""
	}
	if u.PromoCode != "" {
		return u.PromoCode
	}
	for i := 0; i < 8; i++ {
		code := genPromoCode()
		_, err := client.User.UpdateOne(u).SetPromoCode(code).Save(ctx)
		if err == nil {
			return code
		}
		// 唯一冲突（概率 ~8 字符空间碰撞极低）或更新失败：重试新码
	}
	return ""
}

// ResolvePromoCode 推广标识 → 邀请人（双格式兼容）：
// 1) 8 位随机码 → 按 promo_code 精确匹配；
// 2) 纯数字 → 旧 user_id（存量链接兼容）。
// 大小写不敏感（手抄场景）；解析失败返回 nil。
func (r *UserRepo) ResolvePromoCode(ctx context.Context, code string) *ent.User {
	u, _ := r.ResolvePromoCodeChecked(ctx, code)
	return u
}

// ResolvePromoCodeChecked 保留数据库错误，避免注册时将故障误认为无权益。
func (r *UserRepo) ResolvePromoCodeChecked(ctx context.Context, code string) (*ent.User, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 20 {
		return nil, nil
	}
	client := data.Client(ctx, r.data)
	u, err := client.User.Query().Where(user.PromoCode(strings.ToUpper(code))).Only(ctx)
	if err == nil {
		return u, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	if isAllDigits(code) {
		id, e := strconv.ParseUint(code, 10, 64)
		if e == nil && id > 0 {
			u, err = client.User.Get(ctx, id)
			if err == nil {
				return u, nil
			}
			if !ent.IsNotFound(err) {
				return nil, err
			}
		}
	}
	return nil, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
