// plugin-pack signs an offline first-party v1 artifact. No keys are generated or embedded.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	pc "github.com/NovaWorks/zcard-next/server/internal/platform/plugincontract"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func run() error {
	manifest := flag.String("manifest", "", "manifest.json")
	script := flag.String("script", "", "main.js")
	keyPath := flag.String("key", "", "Ed25519 PKCS8 PEM private key, mode 0600")
	keyID := flag.String("key-id", "", "approved key ID")
	out := flag.String("out", "", "new output directory")
	flag.Parse()
	mraw, err := os.ReadFile(*manifest)
	if err != nil {
		return err
	}
	if err = pc.Validate(pc.ManifestKind, mraw); err != nil {
		return err
	}
	var m pc.Manifest
	if err = json.Unmarshal(mraw, &m); err != nil {
		return err
	}
	js, err := os.ReadFile(*script)
	if err != nil {
		return err
	}
	if len(js) > pc.MaxScriptBytes {
		return fmt.Errorf("script too large")
	}
	st, err := os.Stat(*keyPath)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private key must be a private regular file")
	}
	kb, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(kb)
	if block == nil {
		return fmt.Errorf("invalid PEM")
	}
	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	key, ok := pk.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("Ed25519 key required")
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, f := range []struct {
		name string
		raw  []byte
	}{{"manifest.json", mraw}, {"main.js", js}} {
		w, e := zw.Create(f.name)
		if e != nil {
			return e
		}
		if _, e = w.Write(f.raw); e != nil {
			return e
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	d := pc.ArtifactDescriptor{SchemaVersion: 1, PluginID: m.ID, Version: m.Version, ManifestSHA256: sum(mraw), ArchiveSHA256: sum(b.Bytes()), ArchiveBytes: pc.Decimal(strconv.Itoa(b.Len())), KeyID: *keyID}
	message, err := pc.SigningMessage(d)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(d)
	if *out == "" {
		return fmt.Errorf("--out required")
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return err
	}
	for name, content := range map[string][]byte{"descriptor.json": raw, "signature.ed25519": ed25519.Sign(key, message), "plugin.zplug": b.Bytes()} {
		if err = os.WriteFile(filepath.Join(*out, name), content, 0600); err != nil {
			return err
		}
	}
	fmt.Println(d.ArchiveSHA256)
	return nil
}
