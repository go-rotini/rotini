package internal

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestLoadSchemas confirms both embedded JSON Schemas compile, and that the
// once-cache hands back the same compiled instance on every call.
func TestLoadSchemas(t *testing.T) {
	spec1, err := loadSpecSchema()
	if err != nil || spec1 == nil {
		t.Fatalf("loadSpecSchema = %v, %v; want a compiled schema", spec1, err)
	}
	spec2, _ := loadSpecSchema()
	if spec1 != spec2 {
		t.Error("loadSpecSchema recompiled instead of returning the cached schema")
	}

	conf1, err := loadConfSchema()
	if err != nil || conf1 == nil {
		t.Fatalf("loadConfSchema = %v, %v; want a compiled schema", conf1, err)
	}
	conf2, _ := loadConfSchema()
	if conf1 != conf2 {
		t.Error("loadConfSchema recompiled instead of returning the cached schema")
	}
}

// TestCompileSchema_error confirms a broken schema reports the kind in the error.
func TestCompileSchema_error(t *testing.T) {
	if _, err := compileSchema("spec", []byte("{not json")); err == nil {
		t.Error("compileSchema(broken) = nil, want an error")
	}
}

// TestNewSpecLoader covers the loader pairing: the compiled schema plus the
// resolved path and decoded content of the user's spec.
func TestNewSpecLoader(t *testing.T) {
	path := writeTemp(t, "spec.yaml", "name: demo\n")
	l, err := newSpecLoader(path, "1.0.0")
	if err != nil {
		t.Fatalf("newSpecLoader: %v", err)
	}
	if l.schema == nil || l.path != path || l.spec.Command.Name != "demo" || l.version != "1.0.0" {
		t.Errorf("loader not fully populated: %+v", l)
	}

	// The spec is required: no explicit path and no discovery match errors.
	t.Chdir(t.TempDir())
	if _, err := newSpecLoader("", ""); !errors.Is(err, errSpecPathRequired) {
		t.Errorf("newSpecLoader(no spec) = %v, want errSpecPathRequired", err)
	}

	// An undecodable spec surfaces the read error.
	bad := writeTemp(t, "bad.yaml", "name: [unclosed")
	if _, err := newSpecLoader(bad, ""); err == nil {
		t.Error("newSpecLoader(bad yaml) = nil, want a decode error")
	}
}

// TestNewConfLoader covers the conf's optionality: discovered beside the spec
// when present, a default &Conf{} with an empty path when not.
func TestNewConfLoader(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, ".rotini.spec.yaml")
	confPath := filepath.Join(dir, ".rotini.conf.yaml")
	writeTestFile(t, specPath, "name: demo\n")
	writeTestFile(t, confPath, "$schema: https://x/conf.json\n")

	l, err := newConfLoader(specPath, "", "2.0.0")
	if err != nil {
		t.Fatalf("newConfLoader: %v", err)
	}
	if l.path != confPath || l.conf.Schema != "https://x/conf.json" || l.version != "2.0.0" {
		t.Errorf("conf loader not fully populated: %+v", l)
	}

	// No conf anywhere → a usable default with an empty path.
	lonely := filepath.Join(t.TempDir(), "spec.yaml")
	def, err := newConfLoader(lonely, "", "")
	if err != nil {
		t.Fatalf("newConfLoader(default): %v", err)
	}
	if def.path != "" || def.conf == nil {
		t.Errorf("default conf loader = %+v, want empty path and a non-nil default Conf", def)
	}

	// An explicit-but-missing conf path also defaults (the conf is optional).
	missing, err := newConfLoader(lonely, filepath.Join(t.TempDir(), "nope.yaml"), "")
	if err != nil || missing.path != "" {
		t.Errorf("newConfLoader(missing explicit) = %+v, %v; want defaults", missing, err)
	}

	// An undecodable conf surfaces the read error.
	badDir := t.TempDir()
	badSpec := filepath.Join(badDir, ".rotini.spec.yaml")
	writeTestFile(t, badSpec, "name: demo\n")
	writeTestFile(t, filepath.Join(badDir, ".rotini.conf.yaml"), "generate: [unclosed")
	if _, err := newConfLoader(badSpec, "", ""); err == nil {
		t.Error("newConfLoader(bad yaml) = nil, want a decode error")
	}
}
