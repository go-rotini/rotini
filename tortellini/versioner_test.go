package tortellini

import (
	"runtime/debug"
	"testing"
)

// withBuildInfo swaps the build-info reader (the white-box seam) for one test.
func withBuildInfo(t *testing.T, version string, ok bool) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		info := &debug.BuildInfo{}
		info.Main.Version = version
		return info, true
	}
	t.Cleanup(func() { readBuildInfo = prev })
}

func TestNewVersioner(t *testing.T) {
	cases := []struct {
		name          string
		moduleVersion string
		moduleOK      bool
		ldflag        string
		want          string
		wantSemantic  string
	}{
		{"module release version wins", "v1.2.3", true, "v9.9.9", "v1.2.3", "1.2.3"},
		{"(devel) falls back to the ldflag", "(devel)", true, "v2.0.1", "v2.0.1", "2.0.1"},
		{"no build info falls back to the ldflag", "", false, "3.4.5", "3.4.5", "3.4.5"},
		{"empty version still yields semantic 0.0.0", "(devel)", true, "", "", "0.0.0"},
		{"prerelease keeps the leading X.Y.Z", "v1.2.3-rc.1", true, "", "v1.2.3-rc.1", "1.2.3"},
		{"unparseable version yields semantic 0.0.0", "deadbeef", true, "", "deadbeef", "0.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withBuildInfo(t, tc.moduleVersion, tc.moduleOK)
			v := NewVersioner(tc.ldflag)
			if v.Version != tc.want || v.VersionSemantic != tc.wantSemantic {
				t.Errorf("NewVersioner(%q) = {%q %q}, want {%q %q}",
					tc.ldflag, v.Version, v.VersionSemantic, tc.want, tc.wantSemantic)
			}
		})
	}
}
