package codegen

import (
	"archive/zip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProcessorDiff_modSourceFetched diffs against a mod:// contract that isn't in the module
// cache, so `go mod download` really fetches it, from a module proxy on disk.
func TestProcessorDiff_modSourceFetched(t *testing.T) {
	dir := t.TempDir()
	diffModule(t, dir, diffSourceSpec, diffSourceConf)
	proxy := t.TempDir()
	writeProxyModule(t, proxy, "example.com/app", "v1.0.0", map[string]string{
		"go.mod":            "module example.com/app\n\ngo 1.27\n",
		"cli-contract.json": readTestFile(t, filepath.Join(dir, "cli-contract.json")),
	})
	t.Setenv("GOPROXY", fileURL(proxy))
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOFLAGS", "-modcacherw") // so the temporary cache can be removed
	t.Setenv("GOSUMDB", "off")         // the proxy's module is unknown to the checksum database
	t.Setenv("GOTOOLCHAIN", "local")

	writeTestFile(t, dir, ".rotini.spec.yaml", removeCompact(diffSourceSpec))
	r, err := NewProcessor("0.0.0").Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "mod://example.com/app@v1.0.0/cli-contract.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := diffRules(r); len(got) != 1 || got[0] != "COMMAND_NO_DELETE app compact" {
		t.Errorf("findings = %v", got)
	}

	_, err = NewProcessor("0.0.0").Diff(".rotini.spec.yaml", ".rotini.conf.yaml", "mod://example.com/app@v9.9.9/cli-contract.json", "", "")
	if err == nil || !strings.Contains(err.Error(), "go get example.com/app@v9.9.9") {
		t.Errorf("an unpublished version: err = %v, want the go get hint", err)
	}
}

// writeProxyModule lays out module@version under root in the module proxy protocol's form, the
// files given by their path in the module.
func writeProxyModule(t *testing.T, root, module, version string, files map[string]string) {
	t.Helper()
	at := filepath.Join(root, filepath.FromSlash(module), "@v")
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, at, "list", version+"\n")
	writeTestFile(t, at, version+".info", `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	writeTestFile(t, at, version+".mod", files["go.mod"])
	f, err := os.Create(filepath.Join(at, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(module + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// fileURL is the file:// URL of the absolute path dir. A Windows path's volume follows a third
// slash (file:///C:/x), as the go command requires of a file:// GOPROXY.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func TestFileURL(t *testing.T) {
	for dir, want := range map[string]string{
		"/tmp/proxy":        "file:///tmp/proxy",
		"/a b/%x":           "file:///a%20b/%25x",
		"C:/Users/me/proxy": "file:///C:/Users/me/proxy",
	} {
		if got := fileURL(dir); got != want {
			t.Errorf("fileURL(%q) = %q, want %q", dir, got, want)
		}
	}
	if filepath.Separator == '\\' {
		if got, want := fileURL(`C:\Users\me\proxy`), "file:///C:/Users/me/proxy"; got != want {
			t.Errorf("fileURL = %q, want %q", got, want)
		}
	}
}
