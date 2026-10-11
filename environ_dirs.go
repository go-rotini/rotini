package rotini

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// userConfigDir is the platform's own config directory, by os.UserConfigDir's rules over the
// run's environment, except that a relative $XDG_CONFIG_HOME is ignored rather than an error.
func (v *osView) userConfigDir() (string, error) {
	return userConfigDirFor(runtime.GOOS, v.getenv, v.home)
}

// userConfigDirFor applies os.UserConfigDir's rules for goos: %AppData% on Windows,
// ~/Library/Application Support on darwin and ios, $home/lib on plan9, else $XDG_CONFIG_HOME
// when absolute, falling back to ~/.config.
func userConfigDirFor(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	fromHome := func(rel string) (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, rel), nil
	}
	switch goos {
	case "windows":
		if d := getenv("AppData"); d != "" {
			return d, nil
		}
		return "", errors.New("%AppData% is not defined")
	case "darwin", "ios":
		return fromHome(filepath.Join("Library", "Application Support"))
	case "plan9":
		if h := getenv("home"); h != "" {
			return filepath.Join(h, "lib"), nil
		}
		return "", errors.New("$home is not defined")
	}
	if d := getenv("XDG_CONFIG_HOME"); d != "" && filepath.IsAbs(d) {
		return d, nil
	}
	return fromHome(".config")
}

// xdgConfigDirs is the XDG system config directories: each absolute entry of $XDG_CONFIG_DIRS,
// in order, else /etc/xdg. On Windows there is no default, so it is empty unless the variable
// is set.
func (v *osView) xdgConfigDirs() []string {
	return xdgConfigDirsFor(runtime.GOOS, v.getenv("XDG_CONFIG_DIRS"))
}

func xdgConfigDirsFor(goos, list string) []string {
	if list == "" {
		if goos == "windows" {
			return nil
		}
		return []string{"/etc/xdg"}
	}
	var dirs []string
	for _, d := range filepath.SplitList(list) {
		if d != "" && filepath.IsAbs(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// appDir joins a validated application directory name onto base.
func appDir(base, app string) (string, error) {
	if err := validAppName(app); err != nil {
		return "", err
	}
	return filepath.Join(base, app), nil
}

// validAppName rejects an app directory name that would leave its base directory.
func validAppName(app string) error {
	if app == "" || app == "." || app == ".." || strings.ContainsAny(app, `/\`) || strings.ContainsRune(app, 0) {
		return fmt.Errorf("invalid app name %q", app)
	}
	return nil
}

// discoverNativeDirs is the 'native' strategy's one directory: <user config dir>/<app>.
func discoverNativeDirs(d *DiscoverDef, view *osView) ([]string, error) {
	base, err := view.userConfigDir()
	if err != nil {
		return nil, fmt.Errorf("native discovery: %w", err)
	}
	dir, err := appDir(base, d.App)
	if err != nil {
		return nil, fmt.Errorf("native discovery: %w", err)
	}
	if dir, err = absPath(view, dir); err != nil {
		return nil, fmt.Errorf("native discovery: %w", err)
	}
	return []string{dir}, nil
}

// discoverSystemDirs is the 'xdg-system' strategy's directories: <dir>/<app> for each XDG
// system config directory, in order.
func discoverSystemDirs(d *DiscoverDef, view *osView) ([]string, error) {
	bases := view.xdgConfigDirs()
	dirs := make([]string, 0, len(bases))
	for _, base := range bases {
		dir, err := appDir(base, d.App)
		if err != nil {
			return nil, fmt.Errorf("xdg-system discovery: %w", err)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}
