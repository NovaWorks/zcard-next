package httpx

import (
	"net"
	"net/http"
	"testing"
)

func TestValidateURLBlocksPrivate(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/api",
		"http://10.0.0.5/api",
		"http://192.168.1.1/api",
		"http://172.16.0.9/api",
		"http://169.254.169.254/latest/meta-data", // 云元数据端点（SSRF 重灾区）
		"http://[::1]:9000/api",
		"file:///etc/passwd",
		"gopher://127.0.0.1",
	}
	for _, u := range blocked {
		if err := ValidateURL(u); err == nil {
			t.Errorf("应拦截: %s", u)
		}
	}
	if err := ValidateURL("https://example.com/api"); err != nil {
		t.Errorf("公网地址不应拦截: %v", err)
	}
}

func TestRedirectRevalidation(t *testing.T) {
	client := NewSafeClient(5e9)
	// 构造指向内网的重定向请求，CheckRedirect 必须拦截
	req, _ := http.NewRequest(http.MethodGet, "http://192.168.0.10/inner", nil)
	err := client.CheckRedirect(req, nil)
	if err == nil {
		t.Fatal("重定向到内网必须被逐跳校验拦截")
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL("https://user:secret@upstream.example.com/api?x=1")
	if got != "https://upstream.example.com/api?x=1" {
		t.Fatalf("脱敏失败: %s", got)
	}
}

func TestIsPrivateIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.31.255.1", "192.168.0.1", "169.254.1.1", "::1", "fd00::1"} {
		if !IsPrivateIP(net.ParseIP(ip)) {
			t.Errorf("%s 应判定为私有段", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if IsPrivateIP(net.ParseIP(ip)) {
			t.Errorf("%s 不应判定为私有段", ip)
		}
	}
}
