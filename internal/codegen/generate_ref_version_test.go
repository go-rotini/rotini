package codegen

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoModBuildCheck pins which version go.mod builds a module at, read from a real go.mod:
// the requirement, a replacement naming that version over one naming none, a directory or
// another module as the replacement, and no requirement at all.
func TestGoModBuildCheck(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.0.0
	example.com/c v1.0.0
	example.com/d v1.0.0 // indirect
	example.com/f v1.0.0
)

replace example.com/b => example.com/b v1.2.0

replace (
	example.com/c => example.com/c v1.5.0
	example.com/c v1.0.0 => ../c
	example.com/d => other.com/d v1.0.0
	example.com/e => example.com/e v1.0.0
	example.com/f v0.9.0 => ../f
)
`)
	t.Chdir(dir)
	build, err := readGoModBuild()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		module, version string
		want            string // "" accepts
	}{
		{"example.com/a", "v1.0.0", ""},
		{"example.com/a", "v1.1.0", "it names example.com/a v1.1.0, but go.mod builds example.com/a at v1.0.0; align the $ref or go.mod"},
		{"example.com/b", "v1.2.0", ""},
		{"example.com/b", "v1.0.0", "go.mod builds example.com/b at v1.2.0"},
		{"example.com/c", "v1.0.0", "go.mod replaces example.com/c with the directory ../c, which is what builds"},
		{"example.com/d", "v1.0.0", "go.mod replaces example.com/d with other.com/d v1.0.0, which is what builds"},
		{"example.com/e", "v1.0.0", "go.mod does not require example.com/e; add it with go get example.com/e@v1.0.0"},
		{"example.com/f", "v1.0.0", ""}, // the replace names another version, so it doesn't apply
	} {
		err := build.check(tc.module, tc.version)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s@%s: %v, want accepted", tc.module, tc.version, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s@%s: %v, want %q", tc.module, tc.version, err, tc.want)
		}
	}
}

// modRefModule writes a module requiring example.com/db at required, whose spec mounts
// example.com/db at ref, and points the module cache at a published db spec.
func modRefModule(t *testing.T, required, ref string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n\nrequire example.com/db "+required+"\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: acme\n  commands:\n    - $ref: mod://example.com/db@"+ref+"/.rotini.spec.yaml\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", lintFixtureConf)
	cache := t.TempDir()
	writeTestFile(t, cache, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: db\n  summary: manage the database\n")
	writeTestFile(t, cache, ".rotini.conf.yaml", "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: dbcli/zz_rotini.go\n")
	orig := moduleDirFunc
	t.Cleanup(func() { moduleDirFunc = orig })
	moduleDirFunc = func(module, version string) (string, error) {
		if module == "example.com/db" {
			return cache, nil
		}
		return "", errors.New("module " + module + "@" + version + " not available")
	}
	t.Chdir(dir)
	return cache
}

func validateModRef(t *testing.T) error {
	t.Helper()
	return NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "collect", "", func(string, error) {}, func([]error) {})
}

// TestValidate_modRefVersion pins that a mod:// $ref at the version go.mod builds composes, and
// one at another version is refused once, at the $ref, naming both versions.
func TestValidate_modRefVersion(t *testing.T) {
	modRefModule(t, "v1.0.0", "v1.0.0")
	if err := validateModRef(t); err != nil {
		t.Fatalf("matching versions: %v", err)
	}
	if _, err := resolveTree(mustReadSpec(t, ".rotini.spec.yaml"), ".rotini.spec.yaml", "example.com/acme"); err != nil {
		t.Fatalf("compose at the matching version: %v", err)
	}

	modRefModule(t, "v1.0.0", "v1.1.0")
	err := validateModRef(t)
	const want = `spec: .rotini.spec.yaml:5:13: command acme: $ref "mod://example.com/db@v1.1.0/.rotini.spec.yaml": it names example.com/db v1.1.0, but go.mod builds example.com/db at v1.0.0; align the $ref or go.mod`
	if err == nil || strings.TrimSpace(err.Error()) != want {
		t.Errorf("err = %v, want only\n%s", err, want)
	}
	// generate composes through loadRef, which refuses it too.
	if _, err := resolveTree(mustReadSpec(t, ".rotini.spec.yaml"), ".rotini.spec.yaml", "example.com/acme"); err == nil || !strings.Contains(err.Error(), "go.mod builds example.com/db at v1.0.0") {
		t.Errorf("compose: %v, want the version refused", err)
	}
}

// TestValidate_modRefVersionInLocalChild pins that a mismatched mod:// $ref written in a spec the
// parent composes by relative path is reported in that spec's file, not again on the parent.
func TestValidate_modRefVersionInLocalChild(t *testing.T) {
	modRefModule(t, "v1.0.0", "v1.0.0")
	writeTestFile(t, ".", ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: acme\n  commands:\n    - $ref: kid/.rotini.spec.yaml\n")
	writeTestFile(t, ".", "kid/.rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: kid\n  commands:\n    - $ref: mod://example.com/db@v2.0.0/.rotini.spec.yaml\n")
	err := validateModRef(t)
	want := filepath.FromSlash("kid/.rotini.spec.yaml") + ":5:13: command kid: "
	if err == nil || !strings.Contains(err.Error(), want) || strings.Count(err.Error(), "go.mod builds") != 1 {
		t.Errorf("err = %v, want one problem at %s", err, want)
	}
}

// TestCompose_modRefVersionInModChild pins that a mod:// $ref inside a spec read from the module
// cache is held to go.mod as well; with no file of this module to place it in, it is on the root.
func TestCompose_modRefVersionInModChild(t *testing.T) {
	cache := modRefModule(t, "v1.0.0", "v1.0.0")
	writeTestFile(t, cache, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: db\n  commands:\n    - $ref: mod://example.com/util@v1.0.0/.rotini.spec.yaml\n")
	err := validateModRef(t)
	if err == nil || !strings.Contains(err.Error(), ".rotini.spec.yaml:3:3: command acme: composition: ") ||
		!strings.Contains(err.Error(), "go.mod does not require example.com/util") {
		t.Errorf("err = %v, want the nested ref refused on the root", err)
	}
}

// TestValidate_refLocatorWithoutModule pins that how a $ref is written is checked, and placed at
// the $ref, with no go.mod to fetch anything through.
func TestValidate_refLocatorWithoutModule(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: acme\n  commands:\n    - $ref: mod://example.com/db/.rotini.spec.yaml\n    - $ref: http://example.com/web.yaml\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", lintFixtureConf)
	t.Chdir(dir)
	err := validateModRef(t)
	for _, want := range []string{
		`.rotini.spec.yaml:5:13: command acme: invalid module ref "mod://example.com/db/.rotini.spec.yaml"`,
		`.rotini.spec.yaml:6:13: command acme: $ref "http://example.com/web.yaml" is a remote source`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func mustReadSpec(t *testing.T, path string) *Spec {
	t.Helper()
	spec, err := readSpec(path)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
