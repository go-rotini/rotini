package rotini

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-rotini/recon"
	"strings"
	"testing"
)

// Platform-specific behavior of the config channel: path resolution, "~" expansion and the
// walk up to a filesystem root, all of which differ on Windows.

// TestConfigPath_tildeExpansion: a leading "~" in a config path is the home directory; the
// path comes from a file, so no shell has expanded it.
func TestConfigPath_tildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this platform: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string // "" means only the assertions after the table apply
	}{
		{"tilde and a child", "~/.config/app.yaml", filepath.Join(home, ".config", "app.yaml")},
		{"an absolute path is untouched", filepath.Join(home, "x.yaml"), filepath.Join(home, "x.yaml")},
		// An interior "~" is an ordinary directory name, not a home reference.
		{"a tilde inside the path is not a home reference", filepath.Join("a", "~", "x.yaml"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewInputReader(InputSettings{})
			src, err := b.fileSource(ConfigFile{Name: "cfg", Path: tc.in}, nil)
			if err != nil {
				t.Fatalf("fileSource(%q): %v", tc.in, err)
			}
			// fileSource wraps the recon source to carry the entry's logical name;
			// the path lives on the file source underneath.
			if named, wrapped := src.(namedSource); wrapped {
				src = named.Source
			}
			fsrc, ok := src.(*recon.FileSource)
			if !ok {
				t.Fatalf("the config source is a %T, not a *recon.FileSource", src)
			}
			got := filepath.Clean(fsrc.Path())
			if tc.want != "" && got != filepath.Clean(tc.want) {
				t.Errorf("path %q resolved to %q, want %q", tc.in, got, tc.want)
			}
			// The literal "~" segment survives. Comparing against $HOME would not work: a
			// checkout usually lives under it.
			if tc.want == "" && !strings.Contains(got, string(filepath.Separator)+"~"+string(filepath.Separator)) {
				t.Errorf("path %q resolved to %q — an interior ~ is a directory name and must survive", tc.in, got)
			}
		})
	}
}

// TestDiscoverWalkUp_reachesTheRoot: the walk-up strategy stops at "/" on unix and at a drive
// root on Windows.
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

	// The last directory is the root: its parent is itself, the portable test for a root.
	last := dirs[len(dirs)-1]
	if parent := filepath.Dir(last); parent != last {
		t.Errorf("the walk stopped at %q, whose parent is %q — it did not reach a root", last, parent)
	}
	if !strings.HasPrefix(deep, dirs[0]) {
		t.Errorf("the walk started at %q, not at the working directory %q", dirs[0], deep)
	}
}

// TestDiscoverXDG_isLiteralOnEveryPlatform: 'xdg' means $XDG_CONFIG_HOME (default ~/.config)
// on every platform, with no %APPDATA% or ~/Library/Application Support substitution.
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

// TestDiscoverDirs_unknownStrategy: an unrecognized strategy is an error, not an empty search.
func TestDiscoverDirs_unknownStrategy(t *testing.T) {
	if _, err := discoverDirs(&DiscoverDef{Strategy: "magic", File: "x"}); err == nil {
		t.Fatal("an unknown discover strategy returned no error")
	}
}
