package theme

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHTML = `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><script type="module" src="./assets/main.js"></script><link rel="stylesheet" href="./assets/main.css"></head><body>theme</body></html>`

func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, c := range files {
		f, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write([]byte(c)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func packageFiles(version string) map[string]string {
	return map[string]string{"demo/theme.json": `{"name":"Demo","version":"` + version + `","preview":"preview.svg"}`, "demo/index.html": testHTML, "demo/assets/main.js": "console.log('" + version + "')", "demo/assets/main.css": "body{color:red}", "demo/preview.svg": "<svg/>"}
}
func TestInstallServeAndUpgrade(t *testing.T) {
	t.Chdir(t.TempDir())
	first, err := Install(zipFiles(t, packageFiles("1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if !ServePage(w, httptest.NewRequest("GET", "/product/123", nil), first) {
		t.Fatal("page missing")
	}
	if !strings.Contains(w.Body.String(), `<head><base href="`+first.BaseURL()+`">`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body.String())
	}
	for _, asset := range []string{"assets/main.js", "assets/main.css", "preview.svg", "index.html"} {
		w = httptest.NewRecorder()
		ServeStatic(w, httptest.NewRequest("GET", first.BaseURL()+asset, nil))
		if w.Code != 200 {
			t.Fatalf("%s %d", asset, w.Code)
		}
	}
	next, err := Install(zipFiles(t, packageFiles("2.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	active, err := Resolve("demo")
	if err != nil || active.Revision != next.Revision || first.Revision == next.Revision {
		t.Fatalf("active %+v %v", active, err)
	}
	w = httptest.NewRecorder()
	ServeStatic(w, httptest.NewRequest("GET", first.BaseURL()+"assets/main.js", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "1.0.0") {
		t.Fatal("old lazy chunk lost")
	}
	// The same ZIP can be installed repeatedly without corrupting the active revision.
	if _, err = Install(zipFiles(t, map[string]string{"theme.json": `{"key":"rootpack","name":"Root","version":"1"}`, "index.html": "<html><head></head><body>root</body></html>"})); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidUpgradePreservesPrevious(t *testing.T) {
	t.Chdir(t.TempDir())
	first, err := Install(zipFiles(t, packageFiles("1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]string{
		"nested manifest": {"demo/nested/theme.json": `{"name":"Bad","version":"2"}`},
		"broken json":     {"demo/theme.json": "{", "demo/index.html": testHTML},
		"missing index":   {"demo/theme.json": `{"name":"Bad","version":"2"}`},
		"multiple roots":  {"a/theme.json": "{}", "demo/theme.json": "{}"},
		"reserved":        {"classic/theme.json": `{"name":"Classic","version":"2"}`, "classic/index.html": testHTML},
		"traversal":       {"../escape.txt": "bad"},
		"absolute":        {"/escape.txt": "bad"},
		"backslash":       {"demo\\escape.txt": "bad"},
		"source code":     {"demo/main.ts": "export default {}"},
	}
	bad := packageFiles("2")
	bad["demo/index.html"] = strings.ReplaceAll(testHTML, "./assets/", "/assets/")
	cases["absolute assets"] = bad
	missing := packageFiles("2")
	delete(missing, "demo/assets/main.js")
	cases["missing assets"] = missing
	base := packageFiles("2")
	base["demo/index.html"] = strings.Replace(testHTML, "<head>", `<head><base href="/">`, 1)
	cases["base tag"] = base
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := Install(zipFiles(t, files)); e == nil {
				t.Fatal("invalid package accepted")
			}
			active, e := Resolve("demo")
			if e != nil || active.Revision != first.Revision {
				t.Fatalf("old version lost: %v", e)
			}
		})
	}
	if _, err = os.Stat(filepath.Join(Root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("escaped file")
	}
}
func TestStaticIsolation(t *testing.T) {
	t.Chdir(t.TempDir())
	installed, err := Install(zipFiles(t, packageFiles("1")))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{installed.BaseURL() + "theme.json", installed.BaseURL() + "../current", installed.BaseURL(), "/templates/demo/current", "/templates/demo/assets/main.js", installed.BaseURL() + "missing.js"} {
		w := httptest.NewRecorder()
		ServeStatic(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Fatalf("%s returned %d", p, w.Code)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.js")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(installed.Dir, "escape.js"))
	w := httptest.NewRecorder()
	ServeStatic(w, httptest.NewRequest("GET", installed.BaseURL()+"escape.js", nil))
	if w.Code != 404 {
		t.Fatal("symlink escape")
	}
	w = httptest.NewRecorder()
	ServeStatic(w, httptest.NewRequest("HEAD", installed.BaseURL()+"assets/main.js", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD")
	}
	w = httptest.NewRecorder()
	ServeStatic(w, httptest.NewRequest("POST", installed.BaseURL()+"assets/main.js", nil))
	if w.Code != 405 {
		t.Fatal("POST")
	}
}
func TestInstallLimitsAndSymlink(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, e := Install(make([]byte, MaxZipBytes+1)); e == nil {
		t.Fatal("oversize accepted")
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "demo/link.js"}
	h.SetMode(os.ModeSymlink | 0777)
	f, _ := z.CreateHeader(h)
	f.Write([]byte("../../outside"))
	z.Close()
	if _, e := Install(b.Bytes()); e == nil {
		t.Fatal("symlink accepted")
	}
	// Small compressed payload that exceeds the total expanded budget.
	b.Reset()
	z = zip.NewWriter(&b)
	f, _ = z.Create("demo/huge.txt")
	chunk := make([]byte, 1<<20)
	for i := 0; i < 101; i++ {
		f.Write(chunk)
	}
	z.Close()
	if _, e := Install(b.Bytes()); e == nil {
		t.Fatal("zip bomb accepted")
	}
}

func TestConcurrentInstallsPublishCompleteRevision(t *testing.T) {
	t.Chdir(t.TempDir())
	raw := zipFiles(t, packageFiles("concurrent"))
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := Install(raw); errors <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	active, err := Resolve("demo")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ServeStatic(w, httptest.NewRequest("GET", active.BaseURL()+"assets/main.js", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "concurrent") {
		t.Fatal("incomplete revision published")
	}
}
