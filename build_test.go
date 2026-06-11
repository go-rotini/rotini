package rotini

import (
	"runtime/debug"
	"testing"
	"time"
)

// stubReadBuildInfo swaps the readBuildInfo seam for the duration of a test,
// restoring the real implementation on cleanup.
func stubReadBuildInfo(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() { readBuildInfo = prev })
}

// fullBuildInfo is a synthetic *debug.BuildInfo exercising every recognized
// build setting (plus an unrecognized one that must be ignored).
func fullBuildInfo() *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: "go1.22.0",
		Path:      "example.com/app/cmd/app",
		Main:      debug.Module{Path: "example.com/app", Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
			{Key: "-compiler", Value: "gc"},
			{Key: "CGO_ENABLED", Value: "1"},
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-06-10T12:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "-unknown", Value: "ignored"},
		},
	}
}

func TestBuildInfo_storesFallback(t *testing.T) {
	fb := &BuildInformation{Version: "v9.9.9"}
	if b := BuildInfo(fb); b.fallback != fb {
		t.Errorf("fallback = %v, want %v", b.fallback, fb)
	}

	// A nil fallback is accepted.
	if got := BuildInfo(nil); got.fallback != nil {
		t.Errorf("BuildInfo(nil).fallback = %v, want nil", got.fallback)
	}
}

func TestGet_fullSettings(t *testing.T) {
	stubReadBuildInfo(t, fullBuildInfo(), true)

	b := BuildInfo(nil)

	wantTime := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	want := BuildInformation{
		Version:   "v1.2.3",
		GoVersion: "go1.22.0",
		Path:      "example.com/app/cmd/app",
		Module:    "example.com/app",
		OS:        "linux",
		Arch:      "amd64",
		Compiler:  "gc",
		CGO:       true,
		VCS:       "git",
		Revision:  "abc123",
		Time:      wantTime,
		Dirty:     true,
	}
	assertFields(t, b, want)
}

// CGO_ENABLED other than "1" and vcs.modified other than "true" resolve to false;
// an unparseable vcs.time leaves Time zero.
func TestGet_falseyAndBadTime(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "vcs.modified", Value: "false"},
			{Key: "vcs.time", Value: "not-a-timestamp"},
		},
	}
	stubReadBuildInfo(t, info, true)

	b := BuildInfo(nil)

	if b.CGO {
		t.Error("CGO should be false for CGO_ENABLED=0")
	}
	if b.Dirty {
		t.Error("Dirty should be false for vcs.modified=false")
	}
	if !b.Time.IsZero() {
		t.Errorf("Time = %v, want zero for unparseable vcs.time", b.Time)
	}
}

// A "(devel)" live version defers to the fallback.
func TestGet_develDefersToFallback(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	stubReadBuildInfo(t, info, true)

	b := BuildInfo(&BuildInformation{Version: "v3.0.0"})

	if b.Version != "v3.0.0" {
		t.Errorf("Version = %q, want fallback %q", b.Version, "v3.0.0")
	}
}

// A real live version is kept even when a fallback is present.
func TestGet_liveVersionBeatsFallback(t *testing.T) {
	stubReadBuildInfo(t, fullBuildInfo(), true)

	b := BuildInfo(&BuildInformation{Version: "v3.0.0"})

	if b.Version != "v1.2.3" {
		t.Errorf("Version = %q, want live %q", b.Version, "v1.2.3")
	}
}

// When no build info is available, only the fallback overlay runs and every
// empty field is filled from the fallback.
func TestGet_noBuildInfoOverlaysFallback(t *testing.T) {
	stubReadBuildInfo(t, nil, false)

	wantTime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	fb := &BuildInformation{
		Version:   "v3.0.0",
		GoVersion: "go1.21.0",
		Path:      "example.com/fb/cmd",
		Module:    "example.com/fb",
		OS:        "darwin",
		Arch:      "arm64",
		Compiler:  "gccgo",
		VCS:       "hg",
		Revision:  "deadbeef",
		Time:      wantTime,
	}
	b := BuildInfo(fb)

	want := *fb // every string/time field should be copied over
	assertFields(t, b, want)
}

// No build info and no fallback yields a zero-valued BuildInformation (Version "").
func TestGet_noBuildInfoNoFallback(t *testing.T) {
	stubReadBuildInfo(t, nil, false)

	b := BuildInfo(nil)

	if (*b) != (BuildInformation{}) {
		t.Errorf("BuildInfo(nil) = %+v, want zero value", *b)
	}
}

func TestCleanVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"pseudo-version with dirty metadata", "v0.0.0-20260610090009-0ddb0aafb83c+dirty", "0.0.0"},
		{"plain release tag", "v1.2.3", "1.2.3"},
		{"no v prefix", "1.2.3", "1.2.3"},
		{"pre-release suffix", "v1.2.3-rc.1", "1.2.3"},
		{"incompatible metadata", "v10.20.30+incompatible", "10.20.30"},
		{"devel", "(devel)", ""},
		{"empty", "", ""},
		{"non-semver", "nightly", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &BuildInformation{Version: c.version}
			if got := b.CleanVersion(); got != c.want {
				t.Errorf("CleanVersion(%q) = %q, want %q", c.version, got, c.want)
			}
		})
	}
}

// assertFields compares the exported, non-boolean projection plus the booleans.
func assertFields(t *testing.T, got *BuildInformation, want BuildInformation) {
	t.Helper()
	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
	if got.GoVersion != want.GoVersion {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, want.GoVersion)
	}
	if got.Path != want.Path {
		t.Errorf("Path = %q, want %q", got.Path, want.Path)
	}
	if got.Module != want.Module {
		t.Errorf("Module = %q, want %q", got.Module, want.Module)
	}
	if got.OS != want.OS {
		t.Errorf("OS = %q, want %q", got.OS, want.OS)
	}
	if got.Arch != want.Arch {
		t.Errorf("Arch = %q, want %q", got.Arch, want.Arch)
	}
	if got.Compiler != want.Compiler {
		t.Errorf("Compiler = %q, want %q", got.Compiler, want.Compiler)
	}
	if got.CGO != want.CGO {
		t.Errorf("CGO = %v, want %v", got.CGO, want.CGO)
	}
	if got.VCS != want.VCS {
		t.Errorf("VCS = %q, want %q", got.VCS, want.VCS)
	}
	if got.Revision != want.Revision {
		t.Errorf("Revision = %q, want %q", got.Revision, want.Revision)
	}
	if !got.Time.Equal(want.Time) {
		t.Errorf("Time = %v, want %v", got.Time, want.Time)
	}
	if got.Dirty != want.Dirty {
		t.Errorf("Dirty = %v, want %v", got.Dirty, want.Dirty)
	}
}
