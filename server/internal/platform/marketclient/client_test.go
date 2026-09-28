package marketclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	mc "github.com/NovaWorks/zcard-next/server/internal/platform/marketcontract"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNetworkFailuresAndTrust(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	var origin string
	mode := "ok"
	raw := []byte("artifact")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mc.Prefix + "/capabilities":
			json.NewEncoder(w).Encode(mc.Capabilities{APIVersion: "1", FreeDistribution: true, MaxEntries: mc.MaxEntries})
		case mc.Prefix + "/plugins":
			c := mc.Catalog{APIVersion: "1", Origin: origin, Revision: "1", IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(), Entries: []mc.Entry{}}
			e, _ := mc.Sign(c, "key", key)
			if mode == "tampered" {
				e.Catalog.Revision = "2"
			}
			if mode == "unknown" {
				e.KeyID = "attacker"
			}
			json.NewEncoder(w).Encode(e)
		default:
			if mode == "missing" {
				w.WriteHeader(404)
				return
			}
			if mode == "truncated" {
				w.Header().Set("Content-Length", "99")
			}
			if mode == "corrupt" {
				w.Write([]byte("changed!"))
			} else {
				w.Write(raw)
			}
		}
	}))
	defer server.Close()
	origin = server.URL
	c, e := New(origin, origin, map[string]ed25519.PublicKey{"key": pub})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Catalog(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, m := range []string{"tampered", "unknown"} {
		mode = m
		if _, e = c.Catalog(context.Background()); e == nil {
			t.Fatal(m)
		}
	}
	entry := mc.Entry{ArtifactPath: mc.Prefix + "/artifacts/" + digest, Descriptor: pc.ArtifactDescriptor{ArchiveSHA256: digest, ArchiveBytes: "8"}}
	mode = "ok"
	if _, e = c.Download(context.Background(), entry); e != nil {
		t.Fatal(e)
	}
	for _, m := range []string{"missing", "truncated", "corrupt"} {
		mode = m
		if _, e = c.Download(context.Background(), entry); e == nil {
			t.Fatal(m)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Catalog(ctx); e == nil {
		t.Fatal("canceled request accepted")
	}
	if _, e = New(origin, "", nil); e == nil {
		t.Fatal("HTTP production origin accepted")
	}
}
