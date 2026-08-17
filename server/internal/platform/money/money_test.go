package money

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		in        Cents
		precision int
		want      string
	}{
		{1234, 2, "12.34"},
		{-1234, 2, "-12.34"},
		{5, 2, "0.05"},
		{120, 0, "120"},
		{1, 0, "1"},
		{0, 2, "0.00"},
	}
	for _, c := range cases {
		if got := c.in.Format(c.precision); got != c.want {
			t.Errorf("%d.Format(%d) = %q, want %q", c.in, c.precision, got, c.want)
		}
	}
}

func TestParseDecimalStr(t *testing.T) {
	cases := []struct {
		in        string
		precision int
		want      Cents
		wantErr   bool
	}{
		{"12.34", 2, 1234, false},
		{"-12.34", 2, -1234, false},
		{"0.05", 2, 5, false},
		{"100", 2, 10000, false},
		{"100", 0, 100, false},
		{"1.999", 2, 0, true}, // 精度越界必须报错，禁止静默截断
		{"1.2.3", 2, 0, true}, // 非法格式
		{"", 2, 0, true},      // 空串
		{"abc", 2, 0, true},   // 非数字
	}
	for _, c := range cases {
		got, err := ParseDecimalStr(c.in, c.precision)
		if c.wantErr != (err != nil) {
			t.Errorf("ParseDecimalStr(%q,%d) error = %v, wantErr %v", c.in, c.precision, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("ParseDecimalStr(%q,%d) = %d, want %d", c.in, c.precision, got, c.want)
		}
	}
}

// TestAmountBounds 服务端金额边界（铁律 16 的校验口径）。
func TestAmountBounds(t *testing.T) {
	if !ValidCents(0) || !ValidCents(1) || !ValidCents(MaxCents) {
		t.Fatal("合法金额被拒绝")
	}
	if ValidCents(-1) || ValidCents(MaxCents+1) {
		t.Fatal("越界金额被放行")
	}
	if !ValidSignedCents(-MaxCents) || !ValidSignedCents(MaxCents) {
		t.Fatal("合法有符号金额被拒绝")
	}
	if ValidSignedCents(-MaxCents-1) || ValidSignedCents(MaxCents+1) {
		t.Fatal("越界有符号金额被放行")
	}
	// 元↔分换算安全性：1 元=100 分，10 亿分=1 亿元上限内的典型值必须通过
	if !ValidCents(100) || !ValidCents(123456789) {
		t.Fatal("典型金额被拒绝")
	}
}
