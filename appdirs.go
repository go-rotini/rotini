package rotini

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
)

// AppDirectories are where a program keeps its files. [AppDirs] computes them; it creates
// nothing.
type AppDirectories struct {
	Config string // settings the user edits
	Data   string // files the program keeps and the user would back up
	Cache  string // files that can be deleted at any time
	State  string // history and logs: kept across runs, not worth backing up
}

// AppDirs returns app's directories, read through the run's environment ([Context.LookupEnv]
// and the home directory it implies), so a test that injects an environment gets its own.
// strategy uses the words of a config file's `discover.strategy`:
//
//   - "xdg": the XDG base-directory layout on every OS: $XDG_CONFIG_HOME (default
//     ~/.config), $XDG_DATA_HOME (~/.local/share), $XDG_CACHE_HOME (~/.cache) and
//     $XDG_STATE_HOME (~/.local/state), each joined with app.
//   - "native": the platform's own. On macOS, ~/Library/Application Support/<app> for config
//     and data, ~/Library/Caches/<app>, and ~/Library/Application Support/<app>/state, with
//     XDG variables ignored. On Windows, %AppData%\<app> (roaming) for config,
//     %LocalAppData%\<app> for data, and %LocalAppData%\<app>\cache and \state. Elsewhere,
//     the same as "xdg".
//
// An XDG variable holding a relative path is ignored, as the XDG spec requires. app must be a
// single directory name. A missing home directory is an error. A nil rtx reads the process
// environment. Create a data or state directory with 0o700, as the XDG spec asks.
func AppDirs(rtx *Context, app, strategy string) (AppDirectories, error) {
	view := rtx.osView()
	return appDirsFor(runtime.GOOS, view.getenv, view.home, app, strategy)
}

// appDirsFor is AppDirs for goos, over the environment getenv reads and the home directory
// home returns.
func appDirsFor(goos string, getenv func(string) string, home func() (string, error), app, strategy string) (AppDirectories, error) {
	if err := validAppName(app); err != nil {
		return AppDirectories{}, fmt.Errorf("app directories: %w", err)
	}
	if strategy != "xdg" && strategy != "native" {
		return AppDirectories{}, fmt.Errorf("app directories: unknown strategy %q (use xdg or native)", strategy)
	}
	var d AppDirectories
	var err error
	switch {
	case strategy == "native" && (goos == "darwin" || goos == "ios"):
		d, err = darwinAppDirs(home, app)
	case strategy == "native" && goos == "windows":
		d, err = windowsAppDirs(getenv, app)
	case strategy == "native" && goos == "plan9":
		d, err = plan9AppDirs(getenv, app)
	default:
		d, err = xdgAppDirs(getenv, home, app)
	}
	if err != nil {
		return AppDirectories{}, fmt.Errorf("app directories: %w", err)
	}
	return d, nil
}

// xdgAppDirs is the XDG base-directory layout.
func xdgAppDirs(getenv func(string) string, home func() (string, error), app string) (AppDirectories, error) {
	var h string
	dir := func(variable string, fallback ...string) (string, error) {
		if v := getenv(variable); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, app), nil
		}
		if h == "" {
			var err error
			if h, err = home(); err != nil {
				return "", err
			}
		}
		return filepath.Join(append(append([]string{h}, fallback...), app)...), nil
	}
	var d AppDirectories
	var err error
	if d.Config, err = dir("XDG_CONFIG_HOME", ".config"); err != nil {
		return d, err
	}
	if d.Data, err = dir("XDG_DATA_HOME", ".local", "share"); err != nil {
		return d, err
	}
	if d.Cache, err = dir("XDG_CACHE_HOME", ".cache"); err != nil {
		return d, err
	}
	d.State, err = dir("XDG_STATE_HOME", ".local", "state")
	return d, err
}

// darwinAppDirs is macOS's layout under ~/Library.
func darwinAppDirs(home func() (string, error), app string) (AppDirectories, error) {
	h, err := home()
	if err != nil {
		return AppDirectories{}, err
	}
	support := filepath.Join(h, "Library", "Application Support", app)
	return AppDirectories{
		Config: support,
		Data:   support,
		Cache:  filepath.Join(h, "Library", "Caches", app),
		State:  filepath.Join(support, "state"),
	}, nil
}

// windowsAppDirs is Windows' layout: roaming config, local everything else.
func windowsAppDirs(getenv func(string) string, app string) (AppDirectories, error) {
	roaming, local := getenv("AppData"), getenv("LocalAppData")
	switch {
	case roaming == "":
		return AppDirectories{}, errors.New("%AppData% is not defined")
	case local == "":
		return AppDirectories{}, errors.New("%LocalAppData% is not defined")
	}
	data := filepath.Join(local, app)
	return AppDirectories{
		Config: filepath.Join(roaming, app),
		Data:   data,
		Cache:  filepath.Join(data, "cache"),
		State:  filepath.Join(data, "state"),
	}, nil
}

// plan9AppDirs is Plan 9's layout under $home/lib.
func plan9AppDirs(getenv func(string) string, app string) (AppDirectories, error) {
	h := getenv("home")
	if h == "" {
		return AppDirectories{}, errors.New("$home is not defined")
	}
	lib := filepath.Join(h, "lib", app)
	return AppDirectories{
		Config: lib,
		Data:   lib,
		Cache:  filepath.Join(h, "lib", "cache", app),
		State:  filepath.Join(lib, "state"),
	}, nil
}
