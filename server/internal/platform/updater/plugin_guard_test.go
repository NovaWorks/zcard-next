package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPluginHostReplacementGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture executable")
	}
	t.Setenv("ZCARD_CONTAINER", "0")
	dir := t.TempDir()
	bin := filepath.Join(dir, "zcard")
	next := filepath.Join(dir, "zcard.new")
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write(bin, "current")
	if err := ProtectPluginHost(bin, dir); err != nil {
		t.Fatal(err)
	}
	write(next, "#!/bin/sh\nexit 2\n")
	if err := ApplyFile(bin, "v1.2.89", "v1.3.0", next); err == nil {
		t.Fatal("legacy core accepted")
	}
	if b, _ := os.ReadFile(bin); string(b) != "current" {
		t.Fatal("rejected update touched current")
	}
	write(filepath.Join(dir, prevName), "#!/bin/sh\nexit 2\n")
	if err := Rollback(bin); err == nil {
		t.Fatal("legacy rollback accepted")
	}
	if b, _ := os.ReadFile(bin); string(b) != "current" {
		t.Fatal("rejected rollback touched current")
	}
	write(next, "#!/bin/sh\n[ \"$1\" = plugin-host-check ] && [ \"$2\" = -conf ] && [ -d \"$3\" ]\n")
	if err := ApplyFile(bin, "v1.2.89", "v1.3.0", next); err != nil {
		t.Fatal(err)
	}
}
