package internal

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModLocator(t *testing.T) {
	m, v, sub, err := parseModLocator("mod://github.com/acme/clis@v1.2.0/deploy/.rotini.spec.yaml")
	if err != nil || m != "github.com/acme/clis" || v != "v1.2.0" || sub != "deploy/.rotini.spec.yaml" {
		t.Fatalf("parse = %q / %q / %q, %v", m, v, sub, err)
	}
	// Module root (no subpath) → sub ".".
	if _, _, sub, err := parseModLocator("mod://x.com/m@v1"); err != nil || sub != "." {
		t.Errorf("root sub = %q, %v; want .", sub, err)
	}
	for _, bad := range []string{"mod://noatsign/x", "mod://@v1/x", "mod://m@/x"} {
		if _, _, _, err := parseModLocator(bad); err == nil {
			t.Errorf("parse(%q) = nil err, want an error", bad)
		}
	}
}

func TestLocateRef(t *testing.T) {
	// Local base + relative ref → cleaned absolute path.
	loc, err := locateRef("/abs/cmd/parent", "../child/.rotini.spec.yaml")
	if err != nil || loc != filepath.Clean("/abs/cmd/child/.rotini.spec.yaml") {
		t.Errorf("local locate = %q, %v", loc, err)
	}
	// A scheme-qualified ref is absolute regardless of base.
	loc, err = locateRef("/abs/dir", "mod://x.com/m@v1/deploy/x.yaml")
	if err != nil || loc != "mod://x.com/m@v1/deploy/x.yaml" {
		t.Errorf("mod abs locate = %q, %v", loc, err)
	}
	// A relative ref against a mod:// base joins within the module subtree.
	loc, err = locateRef("mod://x.com/m@v1/deploy", "../shared/x.yaml")
	if err != nil || loc != "mod://x.com/m@v1/shared/x.yaml" {
		t.Errorf("mod rel locate = %q, %v", loc, err)
	}
	// A relative ref may not escape the module.
	if _, err := locateRef("mod://x.com/m@v1/deploy", "../../x.yaml"); err == nil {
		t.Error("locate(escape) = nil err, want an error")
	}
}

func TestLocateRef_external(t *testing.T) {
	cases := []struct{ name, base, ref, want string }{
		{"git absolute", "/abs", "git::https://github.com/acme/clis@v1/deploy/x.yaml", "git::https://github.com/acme/clis@v1/deploy/x.yaml"},
		{"git relative on git base", "git::https://github.com/acme/clis@v1/deploy", "../shared/x.yaml", "git::https://github.com/acme/clis@v1/shared/x.yaml"},
		{"https absolute", "/abs", "https://example.com/cli/x.yaml", "https://example.com/cli/x.yaml"},
		{"https relative on https base", "https://example.com/a/b/spec.yaml", "../c/d.yaml", "https://example.com/a/c/d.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, err := locateRef(tc.base, tc.ref)
			if err != nil || loc != tc.want {
				t.Errorf("locateRef = %q, %v; want %q", loc, err, tc.want)
			}
		})
	}
	// A relative git ref may not escape its repo.
	if _, err := locateRef("git::https://x/r@v1/deploy", "../../escape.yaml"); err == nil {
		t.Error("locateRef(git escape) = nil err, want an error")
	}
}

func TestLoadLockedExternal(t *testing.T) {
	root := t.TempDir()
	locator := "git::https://github.com/acme/clis@v1/deploy/.rotini.spec.yaml"
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: deploy\n"
	h := hashBytes([]byte(spec))

	// Unlocked → a clear "run rotini mod" error.
	if _, err := loadLockedExternal(locator, root); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Errorf("unlocked = %v, want a not-locked error", err)
	}

	// Locked + cached → resolves, hash verified, childBase is the git dir.
	if err := writeLockfile(root, map[string]lockEntry{locator: {revision: "abc", hash: h, format: formatYAML, schema: "0.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if err := cacheWrite(root, h, []byte(spec)); err != nil {
		t.Fatal(err)
	}
	rr, err := loadLockedExternal(locator, root)
	if err != nil {
		t.Fatalf("loadLockedExternal: %v", err)
	}
	if rr.spec.Command.Name != "deploy" {
		t.Errorf("name = %q, want deploy", rr.spec.Command.Name)
	}
	if rr.childBase != "git::https://github.com/acme/clis@v1/deploy" {
		t.Errorf("childBase = %q", rr.childBase)
	}

	// Tampered cache (content no longer hashes to the lock) → a hard mismatch error.
	if err := cacheWrite(root, h, []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLockedExternal(locator, root); err == nil || !strings.Contains(err.Error(), "does not match the lock") {
		t.Errorf("tampered = %v, want a hash-mismatch error", err)
	}
}

func TestLoadRef_mod(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "deploy", ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\nname: deploy\n")
	orig := moduleDirFunc
	moduleDirFunc = func(module, version string) (string, error) {
		if module != "x.com/m" || version != "v1.0.0" {
			t.Fatalf("moduleDirFunc(%q, %q) — unexpected", module, version)
		}
		return dir, nil
	}
	defer func() { moduleDirFunc = orig }()

	rr, err := loadRef("mod://x.com/m@v1.0.0/deploy/.rotini.spec.yaml", "consuming.com/me", "")
	if err != nil {
		t.Fatalf("loadRef: %v", err)
	}
	if rr.spec.Command.Name != "deploy" {
		t.Errorf("spec name = %q, want deploy", rr.spec.Command.Name)
	}
	// The module is the EXTERNAL one (so its handlers import from there), not the consumer.
	if rr.module != "x.com/m" {
		t.Errorf("module = %q, want x.com/m", rr.module)
	}
	if rr.childBase != "mod://x.com/m@v1.0.0/deploy" {
		t.Errorf("childBase = %q, want mod://x.com/m@v1.0.0/deploy", rr.childBase)
	}
	if rr.dir != filepath.Join(dir, "deploy") {
		t.Errorf("dir = %q", rr.dir)
	}
}
