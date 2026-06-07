package main

import "testing"

// TestReleaseTag covers the classification that drives the $schema ref and the
// validate guard: only a clean vX.Y.Z release tag yields a tag segment; a dev
// build, an untagged install (pseudo-version), and a pre-release all yield "" —
// which means "scaffold the baseline schema and skip the version guard".
func TestReleaseTag(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean release tag", "v1.2.3", "1.2.3"},
		{"canonicalized patch", "v0.4.0", "0.4.0"},
		{"empty (no build info)", "", ""},
		{"devel build", "(devel)", ""},
		{"unknown literal", "(unknown)", ""},
		{"pseudo-version (untagged install)", "v0.0.0-20260607230016-012c443827ff", ""},
		{"pseudo-version above a tag", "v1.2.4-0.20260607230016-012c443827ff", ""},
		{"pre-release tag", "v1.2.3-rc1", ""},
		{"build metadata", "v1.2.3+meta", ""},
		{"not semver", "1.2.3", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseTag(tc.in); got != tc.want {
				t.Errorf("releaseTag(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
