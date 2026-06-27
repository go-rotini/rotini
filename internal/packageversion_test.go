package internal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rotini "github.com/go-rotini/rotini/internal/runtime"
)

func writeGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestModuleRotiniVersion(t *testing.T) {
	cases := []struct {
		name         string
		gomod        string
		wantVer      string
		wantReplaced bool
	}{
		{
			name:    "single-line require",
			gomod:   "module x\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v1.4.2\n",
			wantVer: "v1.4.2",
		},
		{
			name:    "block require with siblings + comment",
			gomod:   "module x\ngo 1.26\nrequire (\n\tgithub.com/other/dep v0.3.0\n\tgithub.com/go-rotini/rotini v2.1.0 // indirect\n)\n",
			wantVer: "v2.1.0",
		},
		{
			name:         "single-line replace (local dev)",
			gomod:        "module x\nrequire github.com/go-rotini/rotini v1.4.2\nreplace github.com/go-rotini/rotini => ../rotini\n",
			wantVer:      "v1.4.2",
			wantReplaced: true,
		},
		{
			name:         "block replace with version on the left",
			gomod:        "module x\nrequire github.com/go-rotini/rotini v1.4.2\nreplace (\n\tgithub.com/go-rotini/rotini v1.4.2 => ../rotini\n)\n",
			wantVer:      "v1.4.2",
			wantReplaced: true,
		},
		{
			name:  "no rotini require",
			gomod: "module x\n\ngo 1.26\n\nrequire github.com/other/dep v0.3.0\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ver, replaced := moduleRotiniVersion(writeGoMod(t, tc.gomod))
			if ver != tc.wantVer || replaced != tc.wantReplaced {
				t.Errorf("ver/replaced = %q/%v, want %q/%v", ver, replaced, tc.wantVer, tc.wantReplaced)
			}
		})
	}
}

func TestCheckPackageVersion(t *testing.T) {
	lib := func(v string) string { return "module x\nrequire github.com/go-rotini/rotini " + v + "\n" }

	// Cross-major → a typed package-arm error.
	err := checkPackageVersion(writeGoMod(t, lib("v1.5.0")), "v2.0.0")
	var ve *rotini.CompositionVersionError
	if !errors.As(err, &ve) {
		t.Fatalf("cross-major: want *CompositionVersionError, got %v", err)
	}
	if ve.Arm != rotini.CompositionPackageArm || ve.Want != "2.0.0" || ve.Got != "1.5.0" {
		t.Errorf("error = %+v", ve)
	}
	if !errors.Is(err, rotini.ErrInternal) {
		t.Error("want errors.Is(err, rotini.ErrInternal)")
	}

	// Each of these must be a clean skip (nil).
	skips := []struct {
		name, gomod, tool string
	}{
		{"same major", lib("v1.2.0"), "v1.9.9"},
		{"pre-1.0 both major 0", lib("v0.4.0"), "v0.7.0"},
		{"dev tool version", lib("v1.5.0"), ""},
		{"local replace", lib("v2.0.0") + "replace github.com/go-rotini/rotini => ../rotini\n", "v1.0.0"},
		{"no rotini require", "module x\nrequire github.com/other/dep v0.3.0\n", "v1.0.0"},
	}
	for _, s := range skips {
		t.Run(s.name, func(t *testing.T) {
			if err := checkPackageVersion(writeGoMod(t, s.gomod), s.tool); err != nil {
				t.Errorf("want nil (skip), got %v", err)
			}
		})
	}
}
