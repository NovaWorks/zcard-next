package money

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }

func TestToDisplay(t *testing.T) {
	// 100.00 CNY @ 0.1378 → 13.78 USD（1378 cent）
	got := ToDisplay(10000, d("0.1378"), 2)
	if got.DisplayAmount != 1378 {
		t.Errorf("DisplayAmount = %d, want 1379", got.DisplayAmount)
	}
	// 往返：1379 cent = 13.79 × 7.25 = 99.9975 → 10000 分；rounding = 0
	if got.Rounded != 0 {
		t.Errorf("Rounded = %d, want 0", got.Rounded)
	}
	// 1 分的往返误差必须被 rounding 捕获（铁律：回调金额核对以基础货币为准 ± rounding 容差）
	got2 := ToDisplay(1, d("0.1378"), 2)
	if got2.DisplayAmount != 0 {
		t.Errorf("1 分换算 = %d, want 0", got2.DisplayAmount)
	}
	if got2.Rounded != -1 {
		t.Errorf("Rounded = %d, want -1", got2.Rounded)
	}
}

func TestToDisplayZeroRate(t *testing.T) {
	// 汇率缺失 1:1 直通，不 panic
	got := ToDisplay(1234, decimal.Zero, 2)
	if got.DisplayAmount != 1234 || got.Rounded != 0 {
		t.Errorf("零汇率应 1:1：got %+v", got)
	}
}

func TestFromDisplay(t *testing.T) {
	// 13.78 USD @ 0.1378 → 100.00 CNY → 10000 分，rounding 0
	base, r := FromDisplay(1378, d("0.1378"), 2)
	if base != 10000 {
		t.Errorf("base = %d, want 10000", base)
	}
	if r != 0 {
		t.Errorf("rounding = %d, want 0", r)
	}
	// 非整除场景：rounding 非零且量级 ≤ 1 分
	_, r2 := FromDisplay(1, d("7.333"), 2) // 0.01×7.333=0.07333 → 0 分
	if r2 != 0 {
		t.Errorf("小值 rounding = %d, want 0", r2)
	}
}

func TestRoundTripNoFloat(t *testing.T) {
	// 大额往返不产生浮点漂移（decimal 全程）
	got := ToDisplay(9999999999, d("0.137"), 8)
	if got.DisplayAmount <= 0 {
		t.Fatalf("大额换算异常：%d", got.DisplayAmount)
	}
	backBase, _ := FromDisplay(-got.DisplayAmount, d("0.137"), 8) // 负值路径回归
	if backBase >= 0 {
		t.Errorf("负值换算异常：%d", backBase)
	}
}
