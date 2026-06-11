package internal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFileFormat(t *testing.T) {
	cases := []struct {
		path string
		want fileFormat
	}{
		{"spec.yaml", formatYAML},
		{"spec.yml", formatYAML},
		{"SPEC.YAML", formatYAML}, // extension matching is case-insensitive
		{"conf.json", formatJSON},
		{"conf.jsonc", formatJSONC},
		{"conf.toml", formatTOML},
		{"conf.xml", formatUnknown},
		{"no-extension", formatUnknown},
	}
	for _, tc := range cases {
		if got := detectFileFormat(tc.path); got != tc.want {
			t.Errorf("detectFileFormat(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestReadSpec_formatsAndTags reads hand-authored documents in each format
// to confirm format detection and that the json struct tags ("$schema",
// "command") drive decoding across YAML/JSON/JSONC alike.
func TestReadSpec_formatsAndTags(t *testing.T) {
	docs := map[string]string{
		".yaml": "$schema: https://x/spec.json\ncommand:\n  name: demo\n  commands:\n    - name: sub\n",
		".json": `{"$schema":"https://x/spec.json","command":{"name":"demo","commands":[{"name":"sub"}]}}`,
		".jsonc": "{\n  // leading comment\n  \"$schema\": \"https://x/spec.json\",\n" +
			"  \"command\": { \"name\": \"demo\", \"commands\": [{\"name\": \"sub\"}] },\n}\n",
	}
	for ext, doc := range docs {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spec"+ext)
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatalf("seed file: %v", err)
			}
			got, err := readSpec(path)
			if err != nil {
				t.Fatalf("readSpec: %v", err)
			}
			if got.Schema != "https://x/spec.json" || got.Command.Name != "demo" {
				t.Errorf("tag mapping wrong: %+v", got)
			}
			if len(got.Command.Commands) != 1 || got.Command.Commands[0].Name != "sub" {
				t.Errorf("commands wrong: %+v", got)
			}
		})
	}
}

func TestReadFile_errors(t *testing.T) {
	// Unsupported extension.
	if _, err := readSpec(filepath.Join(t.TempDir(), "spec.xml")); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("readSpec(.xml) err = %v, want errUnsupportedFormat", err)
	}
	// Missing file.
	if _, err := readSpec(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("readSpec(missing) = nil, want a read error")
	}
	// Undecodable content.
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("command: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSpec(bad); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("readSpec(bad yaml) = %v, want a decode error", err)
	}
}

// TestToJSON converts each supported serialization to canonical JSON bytes —
// the instance form the schema validator consumes.
func TestToJSON(t *testing.T) {
	docs := map[string]string{
		".yaml":  "name: demo\ncount: 2\n",
		".json":  `{"name":"demo","count":2}`,
		".jsonc": "{\n  // comment\n  \"name\": \"demo\",\n  \"count\": 2,\n}\n",
		".toml":  "name = \"demo\"\ncount = 2\n",
	}
	for ext, doc := range docs {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "doc"+ext)
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			raw, err := toJSON(path)
			if err != nil {
				t.Fatalf("toJSON: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("toJSON produced invalid JSON: %v\n%s", err, raw)
			}
			if v["name"] != "demo" {
				t.Errorf("toJSON lost data: %v", v)
			}
		})
	}

	if _, err := toJSON(filepath.Join(t.TempDir(), "doc.xml")); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("toJSON(.xml) err = %v, want errUnsupportedFormat", err)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("a: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := toJSON(bad); err == nil || !strings.Contains(err.Error(), "convert") {
		t.Errorf("toJSON(bad yaml) = %v, want a convert error", err)
	}
}

// TestGetFallbackPaths locks the discovery locations and their precedence order.
func TestGetFallbackPaths(t *testing.T) {
	got := getFallbackPaths("/proj", fileTypeSpec)
	want := []string{
		filepath.Join("/proj", ".rotini.spec.yml"),
		filepath.Join("/proj", ".rotini.spec.yaml"),
		filepath.Join("/proj", ".rotini.spec.toml"),
		filepath.Join("/proj", ".rotini.spec.json"),
		filepath.Join("/proj", ".rotini.spec.jsonc"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	second := filepath.Join(dir, "b")
	if err := os.WriteFile(second, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := firstExisting([]string{filepath.Join(dir, "a"), second}); got != second {
		t.Errorf("firstExisting = %q, want %q", got, second)
	}
	if got := firstExisting([]string{filepath.Join(dir, "a")}); got != "" {
		t.Errorf("firstExisting(no match) = %q, want empty", got)
	}
}

func TestResolveSpecPath(t *testing.T) {
	// An explicit path passes through untouched.
	if got, err := resolveSpecPath("explicit.yaml"); err != nil || got != "explicit.yaml" {
		t.Errorf("resolveSpecPath(explicit) = %q, %v", got, err)
	}

	// An empty path discovers the first .rotini.spec.* in the working directory.
	dir := t.TempDir()
	t.Chdir(dir)
	want := filepath.Join(dir, ".rotini.spec.yaml")
	if err := os.WriteFile(want, []byte("command:\n  name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveSpecPath(""); err != nil || got != want {
		t.Errorf("resolveSpecPath(cwd fallback) = %q, %v; want %q", got, err, want)
	}

	// No match resolves to "" (callers treat that as the required-spec error).
	t.Chdir(t.TempDir())
	if got, err := resolveSpecPath(""); err != nil || got != "" {
		t.Errorf("resolveSpecPath(no match) = %q, %v; want empty", got, err)
	}
}

func TestResolveConfBesideSpec(t *testing.T) {
	if got := resolveConfBesideSpec("spec.yaml", "explicit.yaml"); got != "explicit.yaml" {
		t.Errorf("explicit conf path not honored: %q", got)
	}

	dir := t.TempDir()
	specPath := filepath.Join(dir, ".rotini.spec.yaml")
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	if err := os.WriteFile(confPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveConfBesideSpec(specPath, ""); got != confPath {
		t.Errorf("conf beside spec = %q, want %q", got, confPath)
	}

	if got := resolveConfBesideSpec(filepath.Join(t.TempDir(), "spec.yaml"), ""); got != "" {
		t.Errorf("conf with no match = %q, want empty", got)
	}
}

func TestDiscoverFile(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	if err := os.WriteFile(confPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := discoverFile(dir, fileTypeConf); err != nil || got != confPath {
		t.Errorf("discoverFile(hit) = %q, %v; want %q", got, err, confPath)
	}
	if _, err := discoverFile(t.TempDir(), fileTypeConf); err == nil {
		t.Error("discoverFile(miss) = nil, want an error")
	}
}

func TestFindModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/mod\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// The walk finds the nearest go.mod above the working directory.
	t.Chdir(nested)
	gotRoot, gotName, err := findModule()
	if err != nil {
		t.Fatalf("findModule: %v", err)
	}
	// TempDir may sit behind a symlink (macOS /var → /private/var), so compare resolved paths.
	wantRoot, _ := filepath.EvalSymlinks(root)
	resolvedGot, _ := filepath.EvalSymlinks(gotRoot)
	if resolvedGot != wantRoot || gotName != "example.com/mod" {
		t.Errorf("findModule = (%q, %q), want (%q, %q)", gotRoot, gotName, root, "example.com/mod")
	}

	// A go.mod with no module line is an error.
	noModule := t.TempDir()
	if err := os.WriteFile(filepath.Join(noModule, "go.mod"), []byte("go 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(noModule)
	if _, _, err := findModule(); err == nil || !strings.Contains(err.Error(), "no module path") {
		t.Errorf("findModule(no module line) = %v, want a no-module-path error", err)
	}
}
