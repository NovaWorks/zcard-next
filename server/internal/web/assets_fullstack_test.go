//go:build fullstack

package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type buildAsset struct {
	File           string   `json:"file"`
	CSS            []string `json:"css"`
	Assets         []string `json:"assets"`
	Imports        []string `json:"imports"`
	DynamicImports []string `json:"dynamicImports"`
	IsEntry        bool     `json:"isEntry"`
}

func validateBuildManifest(root fs.FS) (map[string]buildAsset, error) {
	raw, err := fs.ReadFile(root, ".vite/manifest.json")
	if err != nil {
		return nil, err
	}
	var entries map[string]buildAsset
	if err = json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	entryCount := 0
	for name, entry := range entries {
		if entry.IsEntry {
			entryCount++
		}
		files := append([]string{entry.File}, entry.CSS...)
		files = append(files, entry.Assets...)
		for _, file := range files {
			info, err := fs.Stat(root, file)
			if err != nil || info.IsDir() || info.Size() == 0 {
				return nil, fmt.Errorf("%s references missing/empty asset %s", name, file)
			}
		}
		for _, dep := range append(entry.Imports, entry.DynamicImports...) {
			if _, ok := entries[dep]; !ok {
				return nil, fmt.Errorf("%s references absent manifest entry %s", name, dep)
			}
		}
	}
	if entryCount == 0 {
		return nil, fmt.Errorf("manifest has no application entry")
	}
	return entries, nil
}

// This runs after copying dist into the embed roots and before release compilation.
// It includes lazy routes and their CSS/dependencies, not just index.html's scripts.
func TestEmbeddedBuildManifests(t *testing.T) {
	for _, app := range []struct {
		name   string
		root   fs.FS
		prefix string
	}{
		{"admin", adminFS, "/admin"}, {"storefront", storefrontFS, ""},
	} {
		t.Run(app.name, func(t *testing.T) {
			entries, err := validateBuildManifest(app.root)
			if err != nil {
				t.Fatal(err)
			}
			if app.name == "admin" {
				for _, route := range []string{"marketing", "wallet"} {
					if _, ok := entries["src/views/"+route+"/index.vue"]; !ok {
						t.Fatalf("missing %s route in release manifest", route)
					}
				}
			}
			index, err := fs.ReadFile(app.root, "index.html")
			if err != nil {
				t.Fatal(err)
			}
			handler := newHandler(app.root, app.prefix, nil)
			for _, entry := range entries {
				if entry.IsEntry && !strings.Contains(string(index), entry.File) {
					t.Fatalf("index does not reference build entry %s", entry.File)
				}
				if !strings.HasSuffix(entry.File, ".js") && !strings.HasSuffix(entry.File, ".css") {
					continue
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest("GET", app.prefix+"/"+entry.File, nil))
				contentType := rec.Header().Get("Content-Type")
				if rec.Code != 200 || strings.Contains(contentType, "text/html") || (!strings.Contains(contentType, "javascript") && !strings.Contains(contentType, "text/css")) {
					t.Fatalf("asset %s: %d %s", entry.File, rec.Code, contentType)
				}
			}
			t.Logf("verified %d manifest entries and embedded asset responses", len(entries))
		})
	}
}

func TestManifestRejectsMissingLazyDependencies(t *testing.T) {
	for _, missing := range []string{"assets/wallet.js", "assets/wallet.css"} {
		files := fstest.MapFS{
			".vite/manifest.json": {Data: []byte(`{"main":{"file":"assets/main.js","isEntry":true,"dynamicImports":["wallet"]},"wallet":{"file":"assets/wallet.js","css":["assets/wallet.css"]}}`)},
			"assets/main.js":      {Data: []byte("main")}, "assets/wallet.js": {Data: []byte("wallet")}, "assets/wallet.css": {Data: []byte("css")},
		}
		delete(files, missing)
		if _, err := validateBuildManifest(files); err == nil {
			t.Fatalf("accepted missing %s", missing)
		}
	}
}
