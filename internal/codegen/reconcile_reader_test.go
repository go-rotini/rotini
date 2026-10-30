package codegen

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

// TestReadSpec_formatsAndTags pins format detection and json-tag-driven decoding across
// YAML, JSON and JSONC.
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

// TestReconcileSpec_byteOrderMark reads a spec that starts with a byte order mark, as some
// Windows editors write it, in every format, and reports positions on its first line as an
// editor shows them.
func TestReconcileSpec_byteOrderMark(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	docs := map[string]string{
		".yaml":  "version: 0.0.0\ncommand:\n  name: demo\n",
		".json":  `{"version": "0.0.0", "command": {"name": "demo"}}`,
		".jsonc": "// c\n{\"version\": \"0.0.0\", \"command\": {\"name\": \"demo\"}}",
		".toml":  "version = \"0.0.0\"\n[command]\nname = \"demo\"\n",
	}
	for ext, doc := range docs {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".rotini.spec"+ext)
			if err := os.WriteFile(path, []byte(bom+doc), 0o600); err != nil {
				t.Fatal(err)
			}
			rs, err := reconcileSpec(path)
			if err != nil {
				t.Fatalf("reconcileSpec: %v", err)
			}
			if rs.spec.Command.Name != "demo" {
				t.Errorf("command name %q, want demo", rs.spec.Command.Name)
			}
		})
	}
	path := filepath.Join(t.TempDir(), ".rotini.spec.json")
	if err := os.WriteFile(path, []byte(bom+`{"version": "0.0.0", "command": {"name": "demo"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := reconcileSpec(path)
	if err != nil {
		t.Fatal(err)
	}
	if line, col, _ := rs.locate("/command"); line != 1 || col != 33 {
		t.Errorf("/command is at %d:%d, want 1:33", line, col)
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
	if err := os.WriteFile(bad, []byte("name: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSpec(bad); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("readSpec(bad yaml) = %v, want a decode error", err)
	}
}

// TestValidate_jsonSyntaxErrorIsPositioned pins that a JSON spec or conf that doesn't parse is
// reported in its file at a line and column, as YAML and TOML are, rather than as the schema
// validator failing to decode an anonymous instance.
func TestValidate_jsonSyntaxErrorIsPositioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, dir, "bad.json", "{\n  \"version\": \"0.0.0\",\n  \"command\": {\n    \"name\": \"app\",\n  }\n}\n")
	writeTestFile(t, dir, "ok.json", `{"version": "0.0.0", "command": {"name": "app", "summary": "a demo"}}`)
	p := NewProcessor("0.0.0")
	for _, tc := range []struct{ spec, conf string }{{"bad.json", ""}, {"ok.json", "bad.json"}} {
		err := p.Validate(tc.spec, tc.conf, false, "", "", nil, nil)
		if err == nil {
			t.Fatalf("Validate(%s, %s) = nil, want a syntax error", tc.spec, tc.conf)
		}
		want := "decode bad.json: json: line 5, column 3: invalid character '}'"
		if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "jsonschema") {
			t.Errorf("Validate(%s, %s) = %v, want it to contain %q", tc.spec, tc.conf, err, want)
		}
	}
}

// TestBytesToJSON pins conversion of each supported format to JSON.
func TestBytesToJSON(t *testing.T) {
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
			format, data, err := readRaw(path)
			if err != nil {
				t.Fatalf("readRaw: %v", err)
			}
			raw, err := bytesToJSON(format, data)
			if err != nil {
				t.Fatalf("bytesToJSON: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("bytesToJSON produced invalid JSON: %v\n%s", err, raw)
			}
			if v["name"] != "demo" {
				t.Errorf("bytesToJSON lost data: %v", v)
			}
		})
	}

	if _, err := bytesToJSON(formatUnknown, nil); !errors.Is(err, errUnsupportedFormat) {
		t.Errorf("bytesToJSON(unknown) err = %v, want errUnsupportedFormat", err)
	}
	if _, err := bytesToJSON(formatYAML, []byte("a: [unclosed")); err == nil {
		t.Error("bytesToJSON(bad yaml) = nil, want a convert error")
	}
}

// TestDiscoverFile pins the .rotini.<type>.<ext> naming, extension precedence, and the
// spec/conf distinction.
func TestDiscoverFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".rotini.spec.toml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := discoverFile(dir, fileTypeSpec)
	if !ok || got != filepath.Join(dir, ".rotini.spec.toml") {
		t.Fatalf("discoverFile(spec) = %q, %v; want the .rotini.spec.toml path", got, ok)
	}

	// Precedence is yml > yaml > toml > json > jsonc, so a .yaml wins over .toml.
	if err := os.WriteFile(filepath.Join(dir, ".rotini.spec.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := discoverFile(dir, fileTypeSpec); got != filepath.Join(dir, ".rotini.spec.yaml") {
		t.Errorf("discoverFile precedence = %q, want the .rotini.spec.yaml", got)
	}

	// A conf lookup in the same dir finds nothing (only spec files present).
	if _, ok := discoverFile(dir, fileTypeConf); ok {
		t.Error("discoverFile(conf) matched unexpectedly")
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
	if err := os.WriteFile(want, []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveSpecPath(""); err != nil || got != want {
		t.Errorf("resolveSpecPath(cwd fallback) = %q, %v; want %q", got, err, want)
	}

	// No match resolves to "".
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

func TestDiscoverConf(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	if err := os.WriteFile(confPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := discoverConf(dir); err != nil || got != confPath {
		t.Errorf("discoverConf(hit) = %q, %v; want %q", got, err, confPath)
	}
	if _, err := discoverConf(t.TempDir()); err == nil {
		t.Error("discoverConf(miss) = nil, want an error")
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

// TestSourceLocators pins pointer-to-line:col resolution for YAML, JSON, JSONC and TOML.
func TestSourceLocators(t *testing.T) {
	cases := []struct {
		name    string
		locator func([]byte) sourceLocator
		doc     string
		pointer string
		line    int
	}{
		{"jsonc", jsoncLocator, "{\n  \"version\": \"0.0.0\",\n  \"command\": {\n    \"name\": \"demo\"\n  }\n}", "/command/name", 4},
		{"json-via-jsonc", jsoncLocator, "{\n  \"version\": \"0.0.0\",\n  \"command\": {\"name\": \"demo\"}\n}", "/command/name", 3},
		{"toml", tomlLocator, "version = \"0.0.0\"\n\n[command]\nname = \"demo\"\n", "/command/name", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, col, ok := tc.locator([]byte(tc.doc))(tc.pointer)
			if !ok {
				t.Fatalf("%s locator could not resolve %s", tc.name, tc.pointer)
			}
			if line != tc.line || col <= 0 {
				t.Errorf("%s locator = line %d col %d, want line %d and a positive column", tc.name, line, col, tc.line)
			}
		})
	}
}

// TestTOMLLocator_arraysOfTables pins that a pointer into the i-th [[name]] entry, a
// sub-table header under it, an inline table or an inline array lands on that value's start.
func TestTOMLLocator_arraysOfTables(t *testing.T) {
	doc := "[command]\nname = \"x\"\na.b = 1\n\n[[command.flags]]\nname = \"a\"\n\n[command.flags.schema]\n" +
		"minimum = \"x\"\n\n[[command.flags]]\nname = \"b\"\nschema = {type = \"int\", minimum = 2}\n" +
		"list = [1, {q = 2}]\n\n[[command.commands]]\nname = \"c\"\n\n[[command.commands.flags]]\nname = \"cf\"\n"
	locate := tomlLocator([]byte(doc))
	for pointer, want := range map[string][2]int{
		"/command/name":                    {2, 8},
		"/command/a/b":                     {3, 7},
		"/command/flags":                   {5, 1},
		"/command/flags/0":                 {5, 1},
		"/command/flags/0/name":            {6, 8},
		"/command/flags/0/schema":          {8, 1},
		"/command/flags/0/schema/minimum":  {9, 11},
		"/command/flags/1/name":            {12, 8},
		"/command/flags/1/schema/minimum":  {13, 35},
		"/command/flags/1/list/1/q":        {14, 17},
		"/command/commands/0/flags/0/name": {20, 8},
	} {
		line, col, ok := locate(pointer)
		if !ok || line != want[0] || col != want[1] {
			t.Errorf("tomlLocator(%s) = %d:%d (ok %v), want %d:%d", pointer, line, col, ok, want[0], want[1])
		}
	}
	for _, pointer := range []string{"/command/flags/2", "/command/flags/x", "/command/name/0", "/command/nope"} {
		if line, col, ok := locate(pointer); ok {
			t.Errorf("tomlLocator(%s) = %d:%d, want no position", pointer, line, col)
		}
	}
}

// TestSourceLocators_degradeCleanly pins that an unparseable document or unresolvable
// pointer reports no position.
func TestSourceLocators_degradeCleanly(t *testing.T) {
	for name, locator := range map[string]func([]byte) sourceLocator{"jsonc": jsoncLocator, "toml": tomlLocator} {
		if _, _, ok := locator([]byte("<<<not a document>>>"))("/command/name"); ok {
			t.Errorf("%s locator resolved a position in an unparseable document", name)
		}
		if _, _, ok := locator([]byte("{}"))("/nope/missing"); ok {
			t.Errorf("%s locator resolved a position for an absent pointer", name)
		}
	}
}

// TestFindModuleFile_vendorModInsideGoModTree pins that a vendor.mod module inside a tree with
// its own go.mod further up is found by its vendor.mod, the nearer module file.
func TestFindModuleFile_vendorModInsideGoModTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/outer\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := filepath.Join(root, "vm")
	nested := filepath.Join(vm, "cmd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vm, "vendor.mod"), []byte("module example.com/vm\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	_, name, file, err := findModuleFile()
	if err != nil || name != "example.com/vm" || filepath.Base(file) != "vendor.mod" {
		t.Errorf("findModuleFile = (%q, %q, %v), want example.com/vm from vendor.mod", name, file, err)
	}
}
