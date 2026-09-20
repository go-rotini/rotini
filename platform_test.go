package rotini

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Platform behavior, asserted rather than assumed.
//
// CI runs the suite on ubuntu, macos and windows, but nothing platform-SPECIFIC was ever
// checked: the config channel resolves paths, expands "~", and walks up to a filesystem root,
// and all three of those differ on Windows. A suite that merely runs on three platforms
// proves the code compiles there, not that it behaves there.

// TestConfigPath_tildeExpansion covers "~" in a declared config path. It is the one piece of
// shell syntax rotini honors itself, because the path is read from a file rather than typed at
// a shell that would have expanded it.
func TestConfigPath_tildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this platform: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a bare tilde", "~", home},
		{"tilde and a child", "~/.config/app.yaml", filepath.Join(home, ".config", "app.yaml")},
		{"an absolute path is untouched", filepath.Join(home, "x.yaml"), filepath.Join(home, "x.yaml")},
		{"a relative path is untouched", filepath.Join("etc", "x.yaml"), filepath.Join("etc", "x.yaml")},
		{"a tilde inside the path is NOT a home reference", filepath.Join("a", "~", "x"), filepath.Join("a", "~", "x")},
		{"a tilde-prefixed name is not a home reference", "~user/x", "~user/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandHome(tc.in)
			if filepath.Clean(got) != filepath.Clean(tc.want) {
				t.Errorf("expandHome(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDiscoverWalkUp_reachesTheRoot: the walk-up strategy climbs one parent at a time and has
// to STOP — at "/" on unix and at a drive root on Windows. Getting the stop condition wrong is
// an infinite loop in a user's CLI, which is why it is asserted rather than trusted.
func TestDiscoverWalkUp_reachesTheRoot(t *testing.T) {
	deep := t.TempDir()
	for _, seg := range []string{"a", "b", "c"} {
		deep = filepath.Join(deep, seg)
	}
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)

	dirs, err := discoverDirs(&DiscoverDef{Strategy: "walk-up", File: ".nothing-here.yaml"})
	if err != nil {
		t.Fatalf("walk-up: %v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("walk-up produced no directories to search")
	}

	// The last directory is the root: its parent is itself, which is the only portable
	// way to say "root" and the condition the loop actually tests.
	last := dirs[len(dirs)-1]
	if parent := filepath.Dir(last); parent != last {
		t.Errorf("the walk stopped at %q, whose parent is %q — it did not reach a root", last, parent)
	}
	if !strings.HasPrefix(deep, dirs[0]) {
		t.Errorf("the walk started at %q, not at the working directory %q", dirs[0], deep)
	}
}

// TestDiscoverXDG_isLiteralOnEveryPlatform pins a deliberate cross-platform decision that the
// schema now states outright: 'xdg' means $XDG_CONFIG_HOME (default ~/.config) EVERYWHERE,
// including Windows and macOS. rotini does not substitute %APPDATA% or
// ~/Library/Application Support, so a CLI documented as reading ~/.config/<app> reads the same
// path on every machine and a dotfiles repository works unchanged.
func TestDiscoverXDG_isLiteralOnEveryPlatform(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := os.Getenv("XDG_CONFIG_HOME")

	dirs, err := discoverDirs(&DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "acme"})
	if err != nil {
		t.Fatalf("xdg: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("xdg searched %d directories, want exactly 1", len(dirs))
	}
	if want := filepath.Join(base, "acme"); dirs[0] != want {
		t.Errorf("xdg resolved to %q, want %q — $XDG_CONFIG_HOME must be honored on %s too",
			dirs[0], want, runtime.GOOS)
	}

	// With the variable unset it falls back to ~/.config/<app>, again on every platform.
	os.Unsetenv("XDG_CONFIG_HOME")
	dirs, err = discoverDirs(&DiscoverDef{Strategy: "xdg", File: "config.yaml", App: "acme"})
	if err != nil {
		t.Fatalf("xdg without the variable: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	if want := filepath.Join(home, ".config", "acme"); dirs[0] != want {
		t.Errorf("xdg fell back to %q, want the XDG-literal %q on %s", dirs[0], want, runtime.GOOS)
	}
}

// TestDiscoverDirs_unknownStrategy: an unrecognized strategy is an error, not a silent empty
// search that would look like "the file is simply absent".
func TestDiscoverDirs_unknownStrategy(t *testing.T) {
	if _, err := discoverDirs(&DiscoverDef{Strategy: "magic", File: "x"}); err == nil {
		t.Fatal("an unknown discover strategy returned no error")
	}
}
