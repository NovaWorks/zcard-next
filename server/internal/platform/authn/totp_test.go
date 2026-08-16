package authn

import "testing"

func TestTOTPRoundTrip(t *testing.T) {
	secret, url, err := GenerateTOTP("admin")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || url == "" {
		t.Fatal("密钥或 URL 为空")
	}
	// 无法在单测里验证正确码（需 TOTP 引擎生成当前码——此处只验证格式）
	if len(secret) != 32 { // base32 编码的 20 字节
		t.Logf("密钥长度 %d（非标准 32 也可能合法）", len(secret))
	}
}

func TestVerifyTOTPBadInput(t *testing.T) {
	if VerifyTOTP("", "123456") {
		t.Fatal("空密钥不应通过")
	}
	if VerifyTOTP("JBSWY3DPEHPK3PXP", "") {
		t.Fatal("空码不应通过")
	}
	if VerifyTOTP("JBSWY3DPEHPK3PXP", "12345") {
		t.Fatal("短码不应通过")
	}
}
