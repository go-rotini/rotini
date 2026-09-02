package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

// The locator helpers are pure string work with no IO — the part of $ref resolution
// that decides IDENTITY (and so cycle detection) before anything is read. They are
// tested directly because the composition paths that call them need a module cache.

func TestParseModLocator(t *testing.T) {
	cases := []struct {
		loc                  string
		module, version, sub string
		wantErr              bool
	}{
		{loc: "mod://example.com/m@v1.2.3/cli/.rotini.spec.yaml", module: "example.com/m", version: "v1.2.3", sub: "cli/.rotini.spec.yaml"},
		{loc: "mod://example.com/m@v1.2.3", module: "example.com/m", version: "v1.2.3", sub: "."}, // no subpath = module root
		{loc: "mod://example.com/m@v1.2.3/a/../b", module: "example.com/m", version: "v1.2.3", sub: "b"},
		{loc: "mod://example.com/m", wantErr: true},  // no version
		{loc: "mod://example.com/m@", wantErr: true}, // empty version
		{loc: "mod://@v1.0.0", wantErr: true},        // empty module
	}
	for _, tc := range cases {
		module, version, sub, err := parseModLocator(tc.loc)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseModLocator(%q) = nil error, want one", tc.loc)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseModLocator(%q): %v", tc.loc, err)
			continue
		}
		if module != tc.module || version != tc.version || sub != tc.sub {
			t.Errorf("parseModLocator(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.loc, module, version, sub, tc.module, tc.version, tc.sub)
		}
	}
}

// TestModLocator_roundTrips pins the identity property cycle detection relies on:
// a locator parsed and rebuilt is the same string.
func TestModLocator_roundTrips(t *testing.T) {
	const want = "mod://example.com/m@v1.2.3/cli"
	module, version, sub, err := parseModLocator(want)
	if err != nil {
		t.Fatal(err)
	}
	if got := modLocator(module, version, sub); got != want {
		t.Errorf("modLocator round-trip = %q, want %q", got, want)
	}
}

func TestJoinModLocator(t *testing.T) {
	const base = "mod://example.com/m@v1.0.0/cli"
	cases := []struct{ ref, want string }{
		{"child/.rotini.spec.yaml", "mod://example.com/m@v1.0.0/cli/child/.rotini.spec.yaml"},
		{"../other/spec.yaml", "mod://example.com/m@v1.0.0/other/spec.yaml"}, // up, but still inside
		{"./spec.yaml", "mod://example.com/m@v1.0.0/cli/spec.yaml"},
	}
	for _, tc := range cases {
		got, err := joinModLocator(base, tc.ref)
		if err != nil {
			t.Errorf("joinModLocator(%q, %q): %v", base, tc.ref, err)
			continue
		}
		if got != tc.want {
			t.Errorf("joinModLocator(%q, %q) = %q, want %q", base, tc.ref, got, tc.want)
		}
	}
	// Escaping the module is refused: a ref may not reach outside the pinned subtree.
	if _, err := joinModLocator(base, "../../elsewhere/spec.yaml"); err == nil {
		t.Error("joinModLocator escaping the module = nil error, want a refusal")
	}
}

func TestIsExternalLocator(t *testing.T) {
	for _, loc := range []string{"git::https://example.com/r@v1/spec.yaml", "https://example.com/spec.yaml"} {
		if !isExternalLocator(loc) {
			t.Errorf("isExternalLocator(%q) = false, want true", loc)
		}
	}
	for _, loc := range []string{"mod://example.com/m@v1.0.0/spec.yaml", "../sibling/spec.yaml", "spec.yaml"} {
		if isExternalLocator(loc) {
			t.Errorf("isExternalLocator(%q) = true, want false", loc)
		}
	}
}

func TestLocateRef(t *testing.T) {
	// A scheme-qualified ref is absolute — the base is ignored.
	if got, err := locateRef("/anywhere", "mod://example.com/m@v1.0.0/spec.yaml"); err != nil || got != "mod://example.com/m@v1.0.0/spec.yaml" {
		t.Errorf("locateRef(mod://…) = (%q, %v), want the locator unchanged", got, err)
	}
	// A relative ref under a mod:// base stays in the module subtree.
	if got, err := locateRef("mod://example.com/m@v1.0.0/cli", "child/spec.yaml"); err != nil || got != "mod://example.com/m@v1.0.0/cli/child/spec.yaml" {
		t.Errorf("locateRef(relative under mod://) = (%q, %v)", got, err)
	}
	// A local relative ref resolves to an absolute filesystem path against its base.
	got, err := locateRef(filepath.FromSlash("/tmp/base"), "child/spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || !strings.HasSuffix(filepath.ToSlash(got), "base/child/spec.yaml") {
		t.Errorf("locateRef(local) = %q, want an absolute path ending base/child/spec.yaml", got)
	}
}

// TestLoadRef_refusesExternal pins the stance that rotini neither fetches nor pins
// external refs: git:: and raw https:// are rejected at load, not silently attempted.
func TestLoadRef_refusesExternal(t *testing.T) {
	for _, loc := range []string{"git::https://example.com/r@v1/spec.yaml", "https://example.com/spec.yaml"} {
		if _, err := loadRef(loc, ""); err == nil {
			t.Errorf("loadRef(%q) = nil error, want a refusal", loc)
		}
	}
}
