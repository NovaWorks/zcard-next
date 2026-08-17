package license

// M3 许可证模块测试：安装（校验通过落库）/篡改拒绝/清除/状态 fail-closed。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/platform/license"

	"github.com/go-kratos/kratos/v3/errors"
)

// fakeSettingsStore 设置读写假实现（内存 map）。
type fakeSettingsStore struct {
	m map[string]json.RawMessage
}

func (f *fakeSettingsStore) Get(_ context.Context, group, key string) (json.RawMessage, error) {
	if v, ok := f.m[group+"/"+key]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeSettingsStore) Put(_ context.Context, group, key string, value json.RawMessage) error {
	f.m[group+"/"+key] = value
	return nil
}

// newLicenseEnv 测试环境：公钥配置 + 许可证仓储。
func newLicenseEnv(t *testing.T) (*LicenseRepo, *AdminLicenseService, []byte, string) {
	t.Helper()
	pub, priv, err := license.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeSettingsStore{m: map[string]json.RawMessage{
		"license/pubkey": json.RawMessage(`"` + base64.StdEncoding.EncodeToString(pub) + `"`),
	}}
	repo := NewLicenseRepo(store)
	svc := NewAdminLicenseService(repo)
	// 生成一份有效许可证（实例 ID 由 repo 生成后签发）
	id, err := repo.InstanceID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := license.Sign(priv, license.License{
		InstanceID: id, Features: []string{"analytics"},
		IssuedAt: "2026-08-17T00:00:00Z", ExpiresAt: "2028-08-17T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, svc, raw, string(priv)
}

// TestLicenseInstallVerify 安装→状态有效→特性清单；清除回社区版。
func TestLicenseInstallVerify(t *testing.T) {
	_, svc, raw, _ := newLicenseEnv(t)
	ctx := context.Background()

	st, err := svc.GetLicenseStatus(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed || st.Valid || st.Error == "" {
		t.Fatalf("未安装应 fail-closed: %+v", st)
	}

	st, err = svc.InstallLicense(ctx, &adminv1.InstallLicenseRequest{Content: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || !st.Valid {
		t.Fatalf("合法许可证应有效: %+v", st)
	}
	if len(st.Features) != 1 || st.Features[0] != "analytics" {
		t.Fatalf("特性清单错误: %+v", st.Features)
	}
	if st.InstanceId == "" || st.ExpiresAt == "" {
		t.Fatalf("实例/到期缺失: %+v", st)
	}

	// 清除 → 社区版
	if _, err := svc.ClearLicense(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.GetLicenseStatus(ctx, nil)
	if st.Installed || st.Valid {
		t.Fatalf("清除后应回社区版: %+v", st)
	}
}

// TestLicenseInstallReject 篡改/坏签名/其他实例许可证安装拒绝（不落库）。
func TestLicenseInstallReject(t *testing.T) {
	repo, svc, raw, priv := newLicenseEnv(t)
	ctx := context.Background()
	_ = priv

	// 篡改特性字段（等长替换）
	tampered := append([]byte{}, raw...)
	idx := indexOfBytes(tampered, []byte("analytics"))
	if idx < 0 {
		t.Fatal("测试样本未找到特性字段")
	}
	copy(tampered[idx:idx+9], []byte("reseller"))
	if _, err := svc.InstallLicense(ctx, &adminv1.InstallLicenseRequest{Content: string(tampered)}); !errors.IsBadRequest(err) {
		t.Fatalf("篡改许可证应拒绝: %v", err)
	}

	// 其他实例的许可证（签名有效但绑定不符）→ 拒绝
	otherPub, otherPriv, _ := license.GenerateKeyPair()
	_ = otherPub
	id, _ := repo.InstanceID(ctx)
	otherRaw, _ := license.Sign(otherPriv, license.License{InstanceID: id + "x", Features: []string{"x"}})
	if _, err := svc.InstallLicense(ctx, &adminv1.InstallLicenseRequest{Content: string(otherRaw)}); !errors.IsBadRequest(err) {
		t.Fatalf("绑定不符应拒绝: %v", err)
	}

	// 拒绝后状态仍未安装
	st, _ := svc.GetLicenseStatus(ctx, nil)
	if st.Installed {
		t.Fatal("拒绝的许可证不应落库")
	}
}

func indexOfBytes(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}
