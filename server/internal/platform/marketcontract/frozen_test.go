package marketcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestFrozenV1Contract(t *testing.T) {
	raw, e := os.ReadFile("v1.sha256")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatal("invalid freeze record")
		}
		content, e := os.ReadFile(parts[1])
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(content)
		if hex.EncodeToString(h[:]) != parts[0] {
			t.Fatalf("market API v1 changed: %s; update protocol deliberately", parts[1])
		}
	}
}
