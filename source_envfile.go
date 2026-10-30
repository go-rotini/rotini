package rotini

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// This file builds the environment a run's inputs read: the run's own environment, under
// which any `.env` files (config_files read `as: env`) sit, with variables whose value is read
// from the file another variable names (`variable_file`). Rotini's own variables (HOME, XDG_*,
// PATH) never read through it.

// inputEnv is what an input reads beyond the run's environment.
type inputEnv struct {
	files  map[string]envValue // a variable's value read from the file its variable_file names
	layers []dotenvLayer       // .env files, first wins, all under the real environment
}

// envValue is a variable's value and where it came from, for messages.
type envValue struct {
	val      string
	from     string // "the file named by APP_TOKEN_FILE"
	variable string // APP_TOKEN_FILE
}

// dotenvLayer is one .env file's variables.
type dotenvLayer struct {
	vals map[string]string // by view key
	path string
	name string // the config_files entry's logical name
}

// withInputEnv returns a copy of v whose input lookups also read in.
func (v *osView) withInputEnv(in *inputEnv) *osView {
	out := &osView{foldCase: foldCaseDefault()}
	if v != nil {
		*out = *v
	}
	out.input = in
	return out
}

// foldCaseDefault is whether environment names match case-insensitively on this platform.
func foldCaseDefault() bool { return runtime.GOOS == "windows" }

// inputLookup reads one variable as an input sees it: a value read through variable_file,
// then the run's environment, then each .env file. An empty value counts as unset while a
// lower layer sets the variable to something, so `APP_PORT=` in the environment leaves a .env
// file's APP_PORT in place; a variable no layer sets to anything but "" is set and empty.
func (v *osView) inputLookup(name string) (string, bool) {
	val, _, ok := v.inputFind(name)
	return val, ok
}

// inputFind is inputLookup plus the .env file the value came from (nil for the run's own
// environment or a variable_file file).
func (v *osView) inputFind(name string) (val string, dotenv *dotenvLayer, ok bool) {
	if v == nil || v.input == nil {
		val, ok = v.lookup(name)
		return val, nil, ok
	}
	if fv, ok := v.input.files[v.key(name)]; ok {
		return fv.val, nil, true
	}
	val, ok = v.lookup(name)
	if val != "" {
		return val, nil, true
	}
	for i := range v.input.layers {
		l := &v.input.layers[i]
		lv, set := l.vals[v.key(name)]
		switch {
		case lv != "":
			return lv, l, true
		case set && !ok:
			ok, dotenv = true, l
		}
	}
	return "", dotenv, ok
}

// dotenvSource is the logical name of the .env file that supplied name's value, or "".
func (v *osView) dotenvSource(name string) string {
	if v == nil || v.input == nil || name == "" {
		return ""
	}
	if _, dotenv, _ := v.inputFind(name); dotenv != nil {
		return dotenv.name
	}
	return ""
}

// inputGetenv is inputLookup without the presence report.
func (v *osView) inputGetenv(name string) string {
	val, _ := v.inputLookup(name)
	return val
}

// inputOrigin says where a variable an input read came from when it is not the environment
// itself: " (from .env)" or " (from the file named by APP_TOKEN_FILE)"; "" otherwise.
func (v *osView) inputOrigin(name string) string {
	if v == nil || v.input == nil || name == "" {
		return ""
	}
	if fv, ok := v.input.files[v.key(name)]; ok {
		return " (from " + fv.from + ")"
	}
	if _, dotenv, _ := v.inputFind(name); dotenv != nil {
		return " (from " + filepath.Base(dotenv.path) + ")"
	}
	return ""
}

// fileVariable is the variable_file variable whose file supplied name's value, or "".
func (v *osView) fileVariable(name string) string {
	if v == nil || v.input == nil {
		return ""
	}
	return v.input.files[v.key(name)].variable
}

// inputEnviron is the whole environment as an input sees it, KEY=value: the run's environment,
// then each .env variable it does not set to a value, as [osView.inputLookup] reads them.
func (v *osView) inputEnviron() []string {
	env := v.environ()
	if v == nil || v.input == nil || len(v.input.layers) == 0 {
		return env
	}
	seen := make(map[string]bool, len(env))
	for i, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if val == "" {
			if lv, _ := v.inputLookup(name); lv != "" {
				env[i] = name + "=" + lv
			}
		}
		seen[v.key(name)] = true
	}
	for _, l := range v.input.layers {
		names := make([]string, 0, len(l.vals))
		for k := range l.vals {
			names = append(names, k)
		}
		slices.Sort(names)
		for _, k := range names {
			if !seen[k] {
				seen[k] = true
				env = append(env, k+"="+l.vals[k])
			}
		}
	}
	return env
}

// envFileCap is the largest file a variable_file variable may name, in bytes.
const envFileCap = 1 << 20

// inputView returns view with the environment the inputs in v read: the .env files in scope
// for the chain, and the values of variable_file variables. It returns view itself when v
// reads no environment variable. In a short-circuited run (waived) a .env file that cannot be
// read and a variable_file file that cannot be read are skipped.
//
// A .env file's own config_source is read from the command line and the run's environment
// only; overrides holds those paths.
func (b *InputReader) inputView(chain []Command, v reflect.Value, overrides map[string]pathOverride, waived bool, view *osView) (*osView, error) {
	if !readsEnv(v) && !b.envPathSource(chain) {
		return view, nil
	}
	in := &inputEnv{}
	layered := view.withInputEnv(in)
	for _, f := range b.chainFiles(chain, true) {
		layer, err := b.readDotenv(f, overrides, waived, layered)
		if err != nil {
			return nil, err
		}
		if layer != nil {
			in.layers = append(in.layers, *layer)
		}
	}
	files, err := resolveFileVars(v, layered, waived)
	if err != nil {
		return nil, err
	}
	if len(in.layers) == 0 && len(files) == 0 {
		return view, nil
	}
	in.files = files
	return layered, nil
}

// envFileView is [InputReader.inputView] through the env-files hook. With no .env file in scope
// and no input reading a variable_file, the view is the run's own.
func (b *InputReader) envFileView(fs *featureSet, chain []Command, v reflect.Value, overrides map[string]pathOverride, waived bool, view *osView) (*osView, error) {
	files := b.chainFiles(chain, true)
	if len(files) == 0 && !hasFileVars(v) {
		return view, nil
	}
	if fs.envFiles == nil {
		subject := "an input reads a variable_file"
		if len(files) > 0 {
			subject = fmt.Sprintf(".env file %q is in scope", files[0].Name)
		}
		return nil, missingIn(chain, "env-files", "EnvFiles()", subject)
	}
	return fs.envFiles.view(b, chain, v, overrides, waived, view)
}

// readsEnv reports whether the inputs struct v reads an environment variable: an env input, or
// a flag with an environment fallback.
func readsEnv(v reflect.Value) bool {
	return v.Kind() == reflect.Struct && (hasEnvChannel(v) || hasReconFlags(v))
}

// envPathSource reports whether a configuration file in scope takes its path from an
// environment variable (config_source on an env input), or its profile (a profile selector's
// variables).
func (b *InputReader) envPathSource(chain []Command) bool {
	return slices.ContainsFunc(b.chainConfigFiles(chain), func(f ConfigFile) bool {
		return (f.PathFrom != nil && f.PathFrom.Env != "") || (f.Profiles != nil && f.Profiles.Env != "")
	})
}

// readDotenv reads one .env file into a layer, or nil when the file is absent.
func (b *InputReader) readDotenv(f ConfigFile, overrides map[string]pathOverride, waived bool, view *osView) (*dotenvLayer, error) {
	src, path, err := b.openFileSource(f, overrides, view)
	if err != nil {
		if waived && !isWiringError(err) {
			return nil, nil //nolint:nilnil // a short-circuited run skips a file it can't read
		}
		return nil, err
	}
	defer src.Close()
	if !waived {
		check := func() error { return validateConfigFile(view.clockRef().features(), f, src) }
		if err := view.files.check(checkedKey{name: f.Name, path: path}, check); err != nil {
			return nil, err
		}
	}
	keys := src.Keys()
	if len(keys) == 0 {
		return nil, nil //nolint:nilnil // an absent or empty file adds nothing
	}
	layer := &dotenvLayer{vals: make(map[string]string, len(keys)), path: path, name: f.Name}
	for _, k := range keys {
		val, ok, err := src.Get(k)
		if err != nil || !ok {
			continue
		}
		layer.vals[view.key(k.String())] = channelString(val)
	}
	return layer, nil
}

// resolveFileVars reads the value of each variable_file variable set for the env inputs and
// flag and argument fallbacks of v, keyed by the plain variable (the first of an input's names)
// it stands for. A pair resolves within one layer, the run's environment first: the layer that
// sets either the plain names or the file variable decides, and setting both there is a usage
// error.
// An empty variable counts as unset.
func resolveFileVars(v reflect.Value, view *osView, waived bool) (map[string]envValue, error) {
	out := map[string]envValue{}
	if v.Kind() != reflect.Struct {
		return out, nil
	}
	var firstErr error
	visit := func(channel string, f reflect.StructField) {
		file := f.Tag.Get("envfile")
		if firstErr != nil || file == "" {
			return
		}
		names := strings.Split(f.Tag.Get("env"), ",")
		if names[0] == "" {
			return
		}
		layer := pairLayer(view, names, file)
		if layer == nil {
			return
		}
		plain := slices.IndexFunc(names, func(n string) bool { return layer(n) != "" })
		path := layer(file)
		switch {
		case plain >= 0 && path != "":
			firstErr = usageBind(channel, f.Tag.Get("rotini"), fmt.Sprintf("set %s or %s, not both", names[plain], file), nil)
		case path != "":
			val, err := readEnvFile(view, file, path)
			if err != nil {
				if !waived {
					firstErr = usageBind(channel, f.Tag.Get("rotini"), err.Error(), err)
				}
				return
			}
			out[view.key(names[0])] = envValue{val: val, from: "the file named by " + file, variable: file}
		}
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if s := ci.FieldByName("Env"); s.IsValid() && s.Kind() == reflect.Struct {
			for f := range s.Type().Fields() {
				visit(channelEnv, f)
			}
		}
		if s := commandFlags(ci); s.IsValid() {
			for f := range typeLeaves(s.Type()) {
				visit(channelFlag, f)
			}
		}
		if s := commandArgs(ci); s.IsValid() {
			for f := range s.Type().Fields() {
				visit(channelArgument, f)
			}
		}
	}
	return out, firstErr
}

// hasFileVars reports whether an input of v reads a variable_file: an env input, or a flag or
// argument fallback, with an envfile tag.
func hasFileVars(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	if cached, ok := fileVarTypes.Load(v.Type()); ok {
		if found, ok := cached.(bool); ok {
			return found
		}
	}
	found := typeHasFileVars(v)
	fileVarTypes.Store(v.Type(), found)
	return found
}

// fileVarTypes caches hasFileVars by inputs type, since the answer depends only on tags.
var fileVarTypes sync.Map // reflect.Type -> bool

func typeHasFileVars(v reflect.Value) bool {
	has := func(f reflect.StructField) bool { return f.Tag.Get("envfile") != "" }
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if s := ci.FieldByName("Env"); s.IsValid() && s.Kind() == reflect.Struct && slices.ContainsFunc(slices.Collect(s.Type().Fields()), has) {
			return true
		}
		if s := commandFlags(ci); s.IsValid() && slices.ContainsFunc(slices.Collect(typeLeaves(s.Type())), has) {
			return true
		}
		if s := commandArgs(ci); s.IsValid() && slices.ContainsFunc(slices.Collect(s.Type().Fields()), has) {
			return true
		}
	}
	return false
}

// pairLayer returns the lookup of the first layer that sets any of names or file: the run's
// environment, then each .env file. nil when none does.
func pairLayer(view *osView, names []string, file string) func(string) string {
	sets := func(get func(string) string) bool {
		return get(file) != "" || slices.ContainsFunc(names, func(n string) bool { return get(n) != "" })
	}
	if sets(view.getenv) {
		return view.getenv
	}
	if view.input == nil {
		return nil
	}
	for _, l := range view.input.layers {
		get := func(n string) string { return l.vals[view.key(n)] }
		if sets(get) {
			return get
		}
	}
	return nil
}

// readEnvFile reads the file a variable_file variable names, relative to the run's directory,
// with one trailing line ending removed.
func readEnvFile(view *osView, variable, path string) (string, error) {
	f, err := os.Open(view.abs(path))
	if err != nil {
		return "", fmt.Errorf("%s: %s", variable, unreadableFile(path, view.abs(path), err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, envFileCap+1))
	if err != nil {
		return "", fmt.Errorf("%s: %s", variable, unreadableFile(path, view.abs(path), err))
	}
	if len(data) > envFileCap {
		return "", fmt.Errorf("%s: %s is larger than 1 MiB", variable, quotePath(path))
	}
	return trimAcquiredPayload(string(data)), nil
}

// chainFiles is [InputReader.chainConfigFiles] narrowed to the files read as environment
// (asEnv) or to the configuration files.
func (b *InputReader) chainFiles(chain []Command, asEnv bool) []ConfigFile {
	all := b.chainConfigFiles(chain)
	out := all[:0:0]
	for _, f := range all {
		if (f.As == "env") == asEnv {
			out = append(out, f)
		}
	}
	return out
}

// hasInputEnv reports whether inputs read anything beyond the run's environment.
func (v *osView) hasInputEnv() bool { return v != nil && v.input != nil }
