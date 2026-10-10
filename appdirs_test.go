package rotini

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppDirsFor(t *testing.T) {
	j := filepath.Join
	home := j("/", "home", "ada")
	homeOK := func() (string, error) { return home, nil }
	full := map[string]string{
		"XDG_CONFIG_HOME": j("/", "x", "config"), "XDG_DATA_HOME": j("/", "x", "data"),
		"XDG_CACHE_HOME": j("/", "x", "cache"), "XDG_STATE_HOME": j("/", "x", "state"),
		"AppData": j("/", "roam"), "LocalAppData": j("/", "local"), "home": j("/", "usr", "ada"),
	}
	relative := map[string]string{"XDG_CONFIG_HOME": "rel", "XDG_DATA_HOME": "rel", "XDG_CACHE_HOME": "rel", "XDG_STATE_HOME": "rel"}
	defaults := AppDirectories{
		Config: j(home, ".config", "app"), Data: j(home, ".local", "share", "app"),
		Cache: j(home, ".cache", "app"), State: j(home, ".local", "state", "app"),
	}
	fromXDG := AppDirectories{
		Config: j("/", "x", "config", "app"), Data: j("/", "x", "data", "app"),
		Cache: j("/", "x", "cache", "app"), State: j("/", "x", "state", "app"),
	}
	darwin := AppDirectories{
		Config: j(home, "Library", "Application Support", "app"), Data: j(home, "Library", "Application Support", "app"),
		Cache: j(home, "Library", "Caches", "app"), State: j(home, "Library", "Application Support", "app", "state"),
	}
	windows := AppDirectories{
		Config: j("/", "roam", "app"), Data: j("/", "local", "app"),
		Cache: j("/", "local", "app", "cache"), State: j("/", "local", "app", "state"),
	}
	plan9 := AppDirectories{
		Config: j("/", "usr", "ada", "lib", "app"), Data: j("/", "usr", "ada", "lib", "app"),
		Cache: j("/", "usr", "ada", "lib", "cache", "app"), State: j("/", "usr", "ada", "lib", "app", "state"),
	}
	for _, tc := range []struct {
		goos, strategy string
		env            map[string]string
		want           AppDirectories
	}{
		{"linux", "xdg", nil, defaults},
		{"linux", "xdg", full, fromXDG},
		{"linux", "xdg", relative, defaults},
		{"linux", "native", full, fromXDG},
		{"linux", "native", nil, defaults},
		{"freebsd", "native", relative, defaults},
		{"darwin", "xdg", full, fromXDG},
		{"darwin", "native", full, darwin},
		{"ios", "native", nil, darwin},
		{"windows", "xdg", nil, defaults},
		{"windows", "xdg", full, fromXDG},
		{"windows", "native", full, windows},
		{"plan9", "native", full, plan9},
		{"plan9", "xdg", nil, defaults},
	} {
		got, err := appDirsFor(tc.goos, func(k string) string { return tc.env[k] }, homeOK, "app", tc.strategy)
		if err != nil || got != tc.want {
			t.Errorf("%s/%s with %v = %+v, %v; want %+v", tc.goos, tc.strategy, tc.env, got, err, tc.want)
		}
	}
}

func TestAppDirsFor_errors(t *testing.T) {
	none := func(string) string { return "" }
	noHome := func() (string, error) { return "", errors.New("$HOME is not defined") }
	homeOK := func() (string, error) { return "/h", nil }
	for _, tc := range []struct {
		goos, app, strategy string
		home                func() (string, error)
		want                string
	}{
		{"linux", "", "xdg", homeOK, `invalid app name ""`},
		{"linux", "..", "xdg", homeOK, `invalid app name ".."`},
		{"linux", "a/b", "xdg", homeOK, `invalid app name "a/b"`},
		{"linux", `a\b`, "xdg", homeOK, `invalid app name "a\\b"`},
		{"linux", "app", "walk-up", homeOK, `unknown strategy "walk-up"`},
		{"linux", "app", "xdg", noHome, "$HOME is not defined"},
		{"darwin", "app", "native", noHome, "$HOME is not defined"},
		{"windows", "app", "native", homeOK, "%AppData% is not defined"},
		{"plan9", "app", "native", homeOK, "$home is not defined"},
	} {
		_, err := appDirsFor(tc.goos, none, tc.home, tc.app, tc.strategy)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), "app directories: ") {
			t.Errorf("%s %q %s: err = %v, want %q", tc.goos, tc.app, tc.strategy, err, tc.want)
		}
	}
	_, err := appDirsFor("windows", func(k string) string { return map[string]string{"AppData": "/r"}[k] }, homeOK, "app", "native")
	if err == nil || !strings.Contains(err.Error(), "%LocalAppData% is not defined") {
		t.Errorf("windows without LocalAppData: %v", err)
	}
}

// AppDirs reads the run's injected environment, not the process's.
func TestAppDirs_injected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "process"))
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "AppData=" + filepath.Join(home, "r"), "LocalAppData=" + filepath.Join(home, "l"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "injected")}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron(env)
	d, err := AppDirs(rtx, "demo", "xdg")
	if err != nil || d.Cache != filepath.Join(home, "injected", "demo") || d.Config != filepath.Join(home, ".config", "demo") {
		t.Errorf("AppDirs = %+v, %v", d, err)
	}
	if _, err := AppDirs(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron([]string{}), "demo", "xdg"); err == nil && runtime.GOOS != "android" && runtime.GOOS != "ios" {
		t.Error("an empty injected environment has no home; want an error")
	}
	if d, err := AppDirs(nil, "demo", "xdg"); err != nil || d.Cache != filepath.Join(home, "process", "demo") {
		t.Errorf("nil rtx reads the process environment: %+v, %v", d, err)
	}
}
