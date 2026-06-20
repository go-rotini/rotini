package internal

import (
	"path/filepath"
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

	rr, err := loadRef("mod://x.com/m@v1.0.0/deploy/.rotini.spec.yaml", "consuming.com/me")
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
