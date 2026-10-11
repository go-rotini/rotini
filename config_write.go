package rotini

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-rotini/rotini/internal/cfgedit"
)

// ConfigKey is one key a configuration file may hold, as the input that reads it declares it.
// Generated code lists them in [ConfigFile.Keys].
type ConfigKey struct {
	Constraints // the input's bounds, pattern and item counts

	Key        string      // the dotted path as written in the file; a dotenv file's variable name
	Type       string      // the input's type, as [FlagDef.Type] spells it
	Enum       []string    // the allowed values; nil allows any
	EnumValues []EnumValue // each Enum value's aliases and notes; nil when none declares more
	IgnoreCase bool        // Enum matches without regard to case
	Layout     string      // a time input's layout; see [FlagDef.Layout]
	Layouts    []string    // every layout when there are several; see [FlagDef.Layouts]
	Separator  string      // what splits a list or map value given as one string; "" is ","
	Secret     bool        // the value is a secret
	Object     bool        // the value is an object, which is set by editing the file
}

// ConfigWriteOption adjusts [SetConfigValue] and [UnsetConfigValue].
type ConfigWriteOption func(*configWrite)

type configWrite struct {
	allowSecret bool
	profile     string
	toProfile   bool
	shared      bool
	parse       func(raw string) error
}

// AllowSecretWrite lets [SetConfigValue] write a key declared secret. The file must not be
// readable by other users: a new file is created with mode 0600, and an existing file readable
// by its group or by others is refused, not changed.
func AllowSecretWrite() ConfigWriteOption {
	return func(w *configWrite) { w.allowSecret = true }
}

// ToProfile writes into the named profile of a file that declares profiles, creating the
// profile when the file doesn't define it yet, instead of the profile the run selects.
func ToProfile(name string) ConfigWriteOption {
	return func(w *configWrite) { w.profile, w.toProfile = name, true }
}

// ToSharedKeys writes the file's top-level keys, which every profile shares, instead of the
// profile the run selects.
func ToSharedKeys() ConfigWriteOption {
	return func(w *configWrite) { w.shared = true }
}

// ParseConfigValue checks the raw value of a key whose type rotini can't parse itself, one a
// spec takes from `import:`; the value is then written as the string given. For any other key
// fn runs after rotini's own checks. An error from fn is reported as an invalid value.
func ParseConfigValue(fn func(raw string) error) ConfigWriteOption {
	return func(w *configWrite) { w.parse = fn }
}

// SetConfigValue writes raw as key's value in the configuration file declared as file (its
// config_files name), for a command such as `config set <key> <value>`. It writes nothing on
// any failure.
//
// The value is checked the way the command line checks a flag's: the key must be one an input
// reads from the file, raw is split on the key's separator for a list (map entries are
// key=value) and must parse as the key's type, and the enum, bounds, pattern and item counts
// apply. An enum value is written in its declared spelling, numbers and booleans as such, and
// everything else as a string. A key declared secret is refused without [AllowSecretWrite].
// When the file declares a schema, the edited file must still match it.
//
// The file is the one the command reads: its config_source path, else its fixed path or its
// discover search, from the run's environment and directory. A file that doesn't exist is
// created there, with any missing directories; a walk-up search creates it in the run's
// directory. A file found in the system configuration directories (xdg-system) is refused.
// A symbolic link is followed and the file it points to is written.
//
// The edit keeps the file's comments, key order and layout: only the value's text changes, or
// the key's lines are added at the end of its parent mapping. A layout that can't be edited
// safely, such as a key reached through a YAML alias, is refused. YAML, JSON, JSONC, TOML and
// dotenv files are written. The new file replaces the old one atomically and keeps its
// permissions; a new file gets 0644, or 0600 when any of its keys is secret. Two commands
// writing the same file at once can lose one of the changes.
//
// In a file that declares profiles, the value goes into the profile the run selects, unless
// [ToProfile] or [ToSharedKeys] says otherwise; with no profile selected it goes into the
// shared keys. A value written to the shared keys that the selected profile overrides is
// recorded as a warning.
//
// Errors are [*InputError] values: a usage error for what the user can fix (an unknown key,
// an invalid value, a file that can't be parsed or written), an internal one for the author's
// mistakes (an unknown file name, a key of a custom type without [ParseConfigValue]).
func SetConfigValue(rtx *Context, file, key, raw string, opts ...ConfigWriteOption) error {
	return writeConfigValue(rtx, file, key, &raw, opts)
}

// UnsetConfigValue removes key from the configuration file declared as file, as
// [SetConfigValue] would write it: from the selected profile of a file with profiles, unless
// [ToProfile] or [ToSharedKeys] says otherwise. Comment lines above the key stay, and mappings
// the removal leaves empty are removed, except a profile and the key holding the profiles.
// Removing a key the file doesn't hold, or from a file that doesn't exist, is not an error.
func UnsetConfigValue(rtx *Context, file, key string, opts ...ConfigWriteOption) error {
	return writeConfigValue(rtx, file, key, nil, opts)
}

// ConfigFilePath reports the file the configuration file declared as file resolves to for
// this run, the one [SetConfigValue] writes, and whether it exists. It suits a
// `config path` command. A file found in the system configuration directories is an error,
// as it is for writing.
func ConfigFilePath(rtx *Context, file string) (path string, exists bool, err error) {
	t, err := configTarget(rtx, file)
	if err != nil {
		return "", false, err
	}
	return t.path, t.exists, nil
}

// writeConfigValue sets key to *raw, or removes it when raw is nil.
func writeConfigValue(rtx *Context, file, key string, raw *string, opts []ConfigWriteOption) error {
	var w configWrite
	for _, opt := range opts {
		if opt != nil {
			opt(&w)
		}
	}
	t, err := configTarget(rtx, file)
	if err != nil {
		return err
	}
	f := t.file
	if err := w.checkTarget(f); err != nil {
		return err
	}
	keys := configKeysFor(f.Keys, key)
	if len(keys) == 0 {
		return unknownConfigKey(f, key)
	}
	profile := t.profile.name
	switch {
	case f.Profiles == nil || w.shared:
		profile = ""
	case w.toProfile:
		profile = w.profile
	}
	format, err := t.format()
	if err != nil {
		return err
	}
	path := configKeyPath(format, f, profile, key)

	var edited []byte
	if raw == nil {
		keep := 0
		if profile != "" {
			keep = 2 // the profiles key and the profile
		}
		edited, err = cfgedit.Unset(t.src, format, path, cfgedit.KeepParents(keep))
	} else {
		secret := slices.ContainsFunc(keys, func(k ConfigKey) bool { return k.Secret })
		if secret && !w.allowSecret {
			return usageBind(channelConfig, key, fmt.Sprintf("config key %s is secret and isn't written to a file", key), nil)
		}
		value, verr := configValue(rtx, keys, *raw, format, w.parse)
		if verr != nil {
			return verr
		}
		if secret {
			if err := t.checkPrivate(key); err != nil {
				return err
			}
		}
		edited, err = cfgedit.Set(t.src, format, path, value)
	}
	if err != nil {
		return t.editError(key, err)
	}
	if bytes.Equal(edited, t.src) {
		return nil // nothing changes: the file isn't touched
	}
	if err := t.checkSchema(key, edited, profile); err != nil {
		return err
	}
	if err := t.write(key, edited); err != nil {
		return err
	}
	if raw != nil && profile == "" {
		rtx.RecordWarning(t.shadowed(key, edited))
	}
	return nil
}

// checkTarget rejects options that contradict each other or the file.
func (w configWrite) checkTarget(f ConfigFile) error {
	switch {
	case w.toProfile && w.shared:
		return internalBind(channelConfig, f.Name, "ToProfile and ToSharedKeys can't be used together", nil)
	case w.toProfile && f.Profiles == nil:
		return internalBind(channelConfig, f.Name, fmt.Sprintf("configuration file %q declares no profiles", f.Name), nil)
	case w.toProfile && w.profile == "":
		return internalBind(channelConfig, f.Name, "ToProfile needs a profile name", nil)
	}
	return nil
}

// configKeysFor is every entry of keys declaring key.
func configKeysFor(keys []ConfigKey, key string) []ConfigKey {
	var out []ConfigKey
	for _, k := range keys {
		if k.Key == key {
			out = append(out, k)
		}
	}
	return out
}

// unknownConfigKey is the usage error for a key no input reads from f. Its Token and
// Candidates are the key and the file's keys, which [SuggestionFacts] reads.
func unknownConfigKey(f ConfigFile, key string) *InputError {
	var names []string
	for _, k := range f.Keys {
		if !slices.Contains(names, k.Key) {
			names = append(names, k.Key)
		}
	}
	msg := fmt.Sprintf("unknown config key %s", key)
	if len(names) > 0 {
		msg += " (keys: " + strings.Join(names, ", ") + ")"
	}
	e := usageBind(channelConfig, key, msg, nil)
	e.Token, e.Candidates = key, names
	return e
}

// configKeyPath is where key sits in the file: its dotted segments, under the profile when
// one is the target. A dotenv key is one variable name.
func configKeyPath(format cfgedit.Format, f ConfigFile, profile, key string) []string {
	if format == cfgedit.Dotenv {
		return []string{key}
	}
	path := strings.Split(key, ".")
	if profile != "" {
		path = append([]string{f.Profiles.Under, profile}, path...)
	}
	return path
}

// editError reports a failed edit: a layout the user edits by hand, a file that can't be
// parsed, or a bug that left the file untouched.
func (t configFileTarget) editError(key string, err error) error {
	switch {
	case errors.Is(err, cfgedit.ErrRefused):
		return usageBind(channelConfig, key, fmt.Sprintf("configuration file %s: %v", t.path, err), err)
	case errors.Is(err, cfgedit.ErrVerify), errors.Is(err, cfgedit.ErrUnsupported):
		return internalBind(channelConfig, key, fmt.Sprintf("could not write config key %s to configuration file %s: %v", key, t.path, err), err)
	}
	return usageBind(channelConfig, key, configFileProblem(t.path, err), err)
}

// shadowed is the warning for a value written to a profiled file's shared keys that the
// selected profile overrides, or nil.
func (t configFileTarget) shadowed(key string, edited []byte) error {
	f, selected := t.file, t.profile.name
	if f.Profiles == nil || selected == "" {
		return nil
	}
	m, err := t.decode(edited)
	if err != nil {
		return nil //nolint:nilerr // the edit already decoded; no warning is better than a wrong one
	}
	sections, _ := m[f.Profiles.Under].(map[string]any)
	section, _ := sections[selected].(map[string]any)
	if !hasPath(section, strings.Split(key, ".")) {
		return nil
	}
	return fmt.Errorf("config key %s is set in the shared keys of configuration file %s, but profile %q overrides it", key, t.path, selected)
}

// hasPath reports whether m holds a value at path.
func hasPath(m map[string]any, path []string) bool {
	for i, seg := range path {
		v, ok := m[seg]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		if m, ok = v.(map[string]any); !ok {
			return false
		}
	}
	return false
}
