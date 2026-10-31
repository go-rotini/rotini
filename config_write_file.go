package rotini

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini/internal/cfgedit"
)

// configFileTarget is the file a config write edits: where it is, what it holds now, and the
// profile the run selects.
type configFileTarget struct {
	file    ConfigFileDef
	profile profileChoice
	path    string      // the file as resolved; a symbolic link is followed only when writing
	target  string      // the file written: path with symbolic links resolved
	exists  bool        // the file exists
	mode    fs.FileMode // an existing file's permissions
	src     []byte      // its content; nil when it doesn't exist
	feats   *featureSet // the run's features, whose codecs read and check the file
	view    *osView     // the run's view, whose file system holds the file
}

// configTarget resolves the named configuration file for writing and reads it.
func configTarget(rtx *Context, name string) (configFileTarget, error) {
	lf, err := locateFor(rtx, name)
	if err != nil {
		return configFileTarget{}, err
	}
	f := lf.file
	if f.Discover != nil && f.Discover.Strategy == "xdg-system" {
		return configFileTarget{}, usageBind(InputErrorKindWriteRefused, channelConfig, f.Name,
			fmt.Sprintf("configuration file %q is read from the system configuration directories, which aren't written; edit it by hand", f.Name), nil)
	}
	path, err := lf.writePath()
	if err != nil {
		return configFileTarget{}, err
	}
	if path == "" {
		return configFileTarget{}, usageBind(InputErrorKindWriteRefused, channelConfig, f.Name, fmt.Sprintf("configuration file %q has no location to write to", f.Name), nil)
	}
	t := configFileTarget{file: f, profile: lf.profile, path: path, target: path, feats: lf.view.clockRef().features(), view: lf.view}
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
		if p.emptyFlag != "" {
			return "", usageBind(InputErrorKindFileAccess, channelConfig, f.Name, p.emptyFlag+" was given an empty path; name a configuration file", nil)
		}
		resolved, err := p.resolve(lf.view)
		if err != nil {
			if isWiringError(err) {
				return "", err
			}
			return "", usageBind(InputErrorKindFileAccess, channelConfig, f.Name, fmt.Sprintf("could not resolve configuration file path %s: %v", p.path, err), err)
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
	info, err := t.view.lstat(t.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if t.target, err = t.view.evalSymlinks(t.path); err == nil {
			info, err = t.view.stat(t.target)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return usageBind(InputErrorKindFileAccess, channelConfig, t.file.Name, fmt.Sprintf("configuration file %s is a symbolic link to a file that doesn't exist", t.path), err)
		}
	}
	if err != nil {
		return usageBind(configFileKind(err), channelConfig, t.file.Name, configFileProblem(t.view, t.path, err), err)
	}
	if !info.Mode().IsRegular() {
		return usageBind(InputErrorKindFileAccess, channelConfig, t.file.Name, fmt.Sprintf("configuration file %s is not a regular file", t.path), nil)
	}
	if t.src, err = t.view.readFile(t.target); err != nil {
		return usageBind(configFileKind(err), channelConfig, t.file.Name, configFileProblem(t.view, t.path, err), err)
	}
	t.exists, t.mode = true, info.Mode().Perm()
	return nil
}

// format is the file's format as cfgedit names it: its declared format, else its extension's.
// For a [Codec] format with an encoder, it is the format's name and enc is its codec, whose
// files are rewritten whole rather than edited in place.
func (t configFileTarget) format() (format cfgedit.Format, enc *codecEntry, err error) {
	if t.file.As == "env" {
		return cfgedit.Dotenv, nil, nil
	}
	c, err := t.feats.codecFor(t.file.Format, t.path)
	if err != nil {
		return "", nil, err
	}
	if c == nil {
		return "", nil, internalBind(channelConfig, t.file.Name, fmt.Sprintf("configuration file %s has no format rotini can write; declare its format", t.path), nil)
	}
	if c.author {
		if c.encode == nil {
			return "", nil, usageBind(InputErrorKindWriteRefused, channelConfig, t.file.Name,
				fmt.Sprintf("configuration file %s is %s, which this program reads but doesn't write; edit it by hand", t.path, c.name), nil)
		}
		return cfgedit.Format(c.name), c, nil
	}
	return cfgedit.Format(c.name), nil, nil
}

// codec is the wired codec the file reads with.
func (t configFileTarget) codec() (*codecEntry, error) {
	if t.file.As == "env" {
		if c := t.feats.codec("dotenv"); c != nil {
			return c, nil
		}
		return nil, missingCodec("dotenv", fmt.Sprintf(".env file %q is written", t.file.Name))
	}
	c, err := t.feats.codecFor(t.file.Format, t.path)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, recon.ErrUnsupportedFormat
	}
	return c, nil
}

// decode reads data in the file's format.
func (t configFileTarget) decode(data []byte) (map[string]any, error) {
	c, err := t.codec()
	if err != nil {
		return nil, err
	}
	return c.codec().Decode(stripBOM(data)) //nolint:wrapcheck // reported by the caller
}

// holdsSecrets reports whether any key the file may hold is secret.
func (t configFileTarget) holdsSecrets() bool {
	return slices.ContainsFunc(t.file.Keys, func(k ConfigKeyDef) bool { return k.Secret })
}

// checkPrivate refuses to write a secret into an existing file other users can read.
func (t configFileTarget) checkPrivate(key string) error {
	if !t.exists || runtime.GOOS == "windows" || t.mode&0o077 == 0 {
		return nil
	}
	return usageBind(InputErrorKindWriteRefused, channelConfig, key, fmt.Sprintf("config key %s is secret, but configuration file %s is readable by others (%#o); chmod 600 it first", key, t.path, t.mode), nil)
}

// checkSchema checks the edited file against the file's declared schema, as the input reader
// will read it: for a file with profiles, its shared keys merged with profile (the one
// written, else the one the run selects). A violation is reported as a read reports it, with
// where the rejected value would be in the edited file and, unless it is secret, the value.
func (t configFileTarget) checkSchema(key string, edited []byte, profile string) error {
	f := t.file
	if f.Schema == "" {
		return nil
	}
	c, err := t.codec()
	if err != nil {
		return err
	}
	m, err := c.codec().Decode(stripBOM(edited))
	if err != nil {
		return internalBind(channelConfig, key, fmt.Sprintf("could not read back configuration file %s after the edit", t.path), err)
	}
	if f.Profiles != nil && profile == "" {
		profile = t.profile.name
	}
	if f.Profiles != nil {
		m = effectiveDocument(m, f.Profiles.Under, profile)
	}
	if t.feats.schemas == nil {
		return missingFeature("schemas", "Schemas()", fmt.Sprintf("configuration file %q has a schema", f.Name))
	}
	validator, err := t.feats.schemas.validator(f.Schema)
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		t.feats.schemas.patternMessages(f.Schema, err)
		where, got := t.path, ""
		ve, isVE := asError[*recon.ValidationError](err)
		if isVE {
			if at := violationAt(t.view, c, edited, false, t.path, ve.Path, f, profile); at != "" {
				where += ":" + at
			}
			got = violationValue(m, ve, f)
		}
		if f.Profiles != nil && profile != "" {
			where += " (profile " + profile + ")"
		}
		if isVE && forbiddenKey(ve) {
			// As a read reports a key the schema forbids, in the future tense.
			known := schemaKeysAt(f.Schema, ve.Path[:len(ve.Path)-1])
			msg := fmt.Sprintf("config key %s: configuration file %s would have unknown key %s", key, where, ve.Path.String())
			if len(known) > 0 {
				msg += " (keys: " + strings.Join(known, ", ") + ")"
			}
			e := usageBind(InputErrorKindUnknownKey, channelConfig, key, msg, err)
			e.Token, e.Candidates = ve.Path[len(ve.Path)-1], known
			return e
		}
		return usageBind(InputErrorKindSchemaViolation, channelConfig, key,
			fmt.Sprintf("config key %s: configuration file %s %s%s", key, where, schemaVerdict(err, "would be invalid"), got), err)
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
		return usageBind(InputErrorKindFileAccess, channelConfig, key, pe.Error(), err)
	}
	if !t.view.writable() {
		return usageBind(InputErrorKindWriteRefused, channelConfig, key, fmt.Sprintf("configuration file %s can't be written: %v", t.path, readOnlyError{}), nil)
	}
	if err := t.view.mkdirAll(filepath.Dir(t.target), dirPerm); err != nil {
		return fail("create the directory of", err)
	}
	f, tmp, err := createTemp(t.view, t.target, perm)
	if err != nil {
		return fail("write", err)
	}
	if t.exists {
		// The temporary file was created through the umask; give it the replaced file's mode.
		err = chmodFile(f, perm)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		// On disk before the rename, so a crash can't leave an empty file in the config's place.
		err = syncWritten(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameOutput(t.view, tmp, t.target)
	}
	if err != nil {
		_ = t.view.remove(tmp)
		return fail("write", err)
	}
	t.view.syncDir(filepath.Dir(t.target))
	return nil
}

// syncFile flushes f's content to stable storage.
var syncFile = (*os.File).Sync

// syncDir flushes a directory's entries, so a rename into it survives a crash. It is best
// effort: some file systems can't sync a directory, Windows can't open one for it, and a
// directory a [Program.WithFS] file system opens is synced only when it has a Sync method.
func (v *osView) syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := v.open(dir); err == nil {
		_ = syncWritten(d) //nolint:errcheck // best effort, as the doc says
		_ = d.Close()
	}
}
