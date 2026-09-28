// Creates only new, isolated localhost fixtures. Run from the repository root.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root, err := os.MkdirTemp("", "zcard-plugin-ui-")
	if err != nil {
		panic(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			panic(err)
		}
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(err)
	}
	write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	keys := make([]string, 5)
	for i := range keys {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			panic(err)
		}
		keys[i] = hex.EncodeToString(b)
	}
	os.MkdirAll(filepath.Join(root, "conf"), 0700)
	os.MkdirAll(filepath.Join(root, "data"), 0700)
	config := fmt.Sprintf("server:\n  http:\n    addr: 127.0.0.1:18083\n    timeout: 30s\n  grpc:\n    addr: 127.0.0.1:19093\n  migrate_on_start: true\n  admin_base_path: /p3-admin\ndata:\n  database:\n    driver: sqlite\n    source: %s/data/zcard.db\n  plugin_data_dir: %s/data\n  plugin_trusted_keys:\n    ui-test: %s\nsecurity:\n  jwt_admin_key: %s\n  jwt_user_key: %s\n  card_key: %s\n  data_key: %s\ntenancy:\n  mode: row\nlog:\n  level: warn\n", root, root, base64.StdEncoding.EncodeToString(pub), keys[0], keys[1], keys[2], keys[3])
	write("conf/config.yaml", []byte(config))
	login, _ := json.Marshal(map[string]string{"username": "p3-admin", "password": keys[4]})
	write("login.json", login)
	manifest, err := os.ReadFile("examples/plugins/member-purchase-gate/manifest.json")
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err = json.Unmarshal(manifest, &m); err != nil {
		panic(err)
	}
	for i := 0; i < 2; i++ {
		m["version"] = fmt.Sprintf("0.1.%d", i)
		b, _ := json.Marshal(m)
		write(fmt.Sprintf("manifest%d.json", i+1), b)
	}
	fmt.Println(root)
}
