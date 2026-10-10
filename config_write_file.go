package rotini

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini/internal/cfgedit"
)

// configFileTarget is the file a config write edits: where it is, what it holds now, and the
// profile the run selects.
type configFileTarget struct {
	file    ConfigFile
	profile profileChoice
	path    string      // the file as resolved; a symbolic link is followed only when writing
	target  string      // the file written: path with symbolic links resolved
	exists  bool        // the file exists
	mode    fs.FileMode // an existing file's permissions
	src     []byte      // its content; nil when it doesn't exist
}

// configTarget resolves the named configuration file for writing and reads it.
func configTarget(rtx *Context, name string) (configFileTarget, error) {
	lf, err := locateConfigFile(rtx, name)
	if err != nil {
		return configFileTarget{}, err
	}
	f := lf.file
	if f.Discover != nil && f.Discover.Strategy == "xdg-system" {
		return configFileTarget{}, usageBind(channelConfig, f.Name,
			fmt.Sprintf("configuration file %q is read from the system configuration directories, which aren't written; edit it by hand", f.Name), nil)
	}
	path, err := lf.writePath()
	if err != nil {
		return configFileTarget{}, err
	}
	if path == "" {
		return configFileTarget{}, usageBind(channelConfig, f.Name, fmt.Sprintf("configuration file %q has no location to write to", f.Name), nil)
	}
	t := configFileTarget{file: f, profile: lf.profile, path: path, target: path}
	if err := t.read(); err != nil {
		return configFileTarget{}, err
	}
	return t, nil
}

// writePath is the file a write goes to: the path a config_source input supplies, else the
// entry's fixed path or the file its discover search finds, else where that search looks first.
func (lf locatedConfigFile) writePath() (string, error) {
	f := lf.file
	if p, ok := lf.paths[f.Name]; ok {
		resolved, err := lf.view.expandPath(p)
		if err == nil {
			resolved, err = absPath(lf.view, resolved)
		}
		if err != nil {
			return "", usageBind(channelConfig, f.Name, fmt.Sprintf("could not resolve configuration file path %s: %v", p, err), err)
		}
		return resolved, nil
	}
	src, path, err := lf.reader.openFileSource(f, lf.paths, lf.view)
	if err != nil {
		return "", err
	}
	_ = src.Close() // the file is read again, as bytes
	return path, nil
}

// read loads the target's current content, following a symbolic link to the file it names.
func (t *configFileTarget) read() error {
	info, err := os.Lstat(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if t.target, err = filepath.EvalSymlinks(t.path); err == nil {
			info, err = os.Stat(t.target)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return usageBind(channelConfig, t.file.Name, fmt.Sprintf("configuration file %s is a symbolic link to a file that doesn't exist", t.path), err)
		}
	}
	if err != nil {
		return usageBind(channelConfig, t.file.Name, configFileProblem(t.path, err), err)
	}
	if !info.Mode().IsRegular() {
		return usageBind(channelConfig, t.file.Name, fmt.Sprintf("configuration file %s is not a regular file", t.path), nil)
	}
	if t.src, err = os.ReadFile(t.target); err != nil {
		return usageBind(channelConfig, t.file.Name, configFileProblem(t.path, err), err)
	}
	t.exists, t.mode = true, info.Mode().Perm()
	return nil
}

// format is the file's format as cfgedit names it: its declared format, else its extension's.
func (t configFileTarget) format() (cfgedit.Format, error) {
	if t.file.As == "env" {
		return cfgedit.Dotenv, nil
	}
	codec, ok := fileCodec(t.file.Format, t.path)
	if !ok {
		return "", internalBind(channelConfig, t.file.Name, fmt.Sprintf("configuration file %s has no format rotini can write; declare its format", t.path), nil)
	}
	return cfgedit.Format(codec.Name()), nil
}

// decode reads data in the file's format.
func (t configFileTarget) decode(data []byte) (map[string]any, error) {
	if t.file.As == "env" {
		return recon.Dotenv.Decode(stripBOM(data)) //nolint:wrapcheck // reported by the caller
	}
	codec, ok := fileCodec(t.file.Format, t.path)
	if !ok {
		return nil, recon.ErrUnsupportedFormat
	}
	return codec.Decode(stripBOM(data)) //nolint:wrapcheck // reported by the caller
}

// holdsSecrets reports whether any key the file may hold is secret.
func (t configFileTarget) holdsSecrets() bool {
	return slices.ContainsFunc(t.file.Keys, func(k ConfigKey) bool { return k.Secret })
}

// checkPrivate refuses to write a secret into an existing file other users can read.
func (t configFileTarget) checkPrivate(key string) error {
	if !t.exists || runtime.GOOS == "windows" || t.mode&0o077 == 0 {
		return nil
	}
	return usageBind(channelConfig, key, fmt.Sprintf("config key %s is secret, but configuration file %s is readable by others (%#o); chmod 600 it first", key, t.path, t.mode), nil)
}

// checkSchema checks the edited file against the file's declared schema, as the input reader
// will read it: for a file with profiles, its shared keys merged with profile (the one
// written, else the one the run selects).
func (t configFileTarget) checkSchema(key string, edited []byte, profile string) error {
	f := t.file
	if f.Schema == "" {
		return nil
	}
	m, err := t.decode(edited)
	if err != nil {
		return internalBind(channelConfig, key, fmt.Sprintf("could not read back configuration file %s after the edit", t.path), err)
	}
	where := t.path
	if f.Profiles != nil {
		if profile == "" {
			profile = t.profile.name
		}
		m = effectiveDocument(m, f.Profiles.Under, profile)
		if profile != "" {
			where += " (profile " + profile + ")"
		}
	}
	validator, err := schemaValidator(f.Schema)
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		applyPatternMessages(f.Schema, err)
		return usageBind(channelConfig, key, fmt.Sprintf("config key %s: configuration file %s would be invalid: %s", key, where, schemaDetail(err)), err)
	}
	return nil
}

// write replaces the file with data atomically: data goes to a temporary file beside it,
// which is renamed into place. An existing file keeps its permissions; a new one gets 0644, or
// 0600 when the file may hold a secret, and missing directories are created 0755 (0700).
func (t configFileTarget) write(key string, data []byte) error {
	perm, dirPerm := fs.FileMode(0o644), fs.FileMode(0o755)
	if t.holdsSecrets() {
		perm, dirPerm = 0o600, 0o700
	}
	if t.exists {
		perm = t.mode
	}
	fail := func(what string, err error) error {
		pe := &pathError{msg: fmt.Sprintf("could not %s configuration file %s", what, t.path), err: err, detail: true}
		return usageBind(channelConfig, key, pe.Error(), err)
	}
	if err := os.MkdirAll(filepath.Dir(t.target), dirPerm); err != nil {
		return fail("create the directory of", err)
	}
	f, tmp, err := createTemp(t.target, perm)
	if err != nil {
		return fail("write", err)
	}
	if t.exists {
		// The temporary file was created through the umask; give it the replaced file's mode.
		err = f.Chmod(perm)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameOutput(tmp, t.target)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fail("write", err)
	}
	return nil
}
