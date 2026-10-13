package rotini

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/toml"
)

// InputReader is the default multi-source input reader. It fills a command's typed inputs from
// argv (via a default [Parser]) and from the non-argv channels — environment variables,
// configuration files, and the leaf command's typed stdin payload — reconciled and decoded by
// recon. It is the engine behind [Context.Inputs]; [Context.ArgvInputs] and its siblings expose
// the channels individually.
//
//	reader := rotini.NewInputReader(settings)
//	var in WidgetCreateInputs
//	if err := reader.Read(rtx, &in); err != nil { /* handler owns it */ }
//
// Env values fill the generated <Prefix>Env struct and config-file values <Prefix>Config. The
// two channels use independent registries, so an env var never leaks into a config field or the
// reverse. Flags and arguments may also fall back to env and config.
type InputReader struct {
	parser       *Parser
	configFiles  []ConfigFile
	stdinSchemas map[string]string // "<Prefix>Stdin" type name → JSON Schema for payload validation
	envPrefix    string            // InputSettings.EnvPrefix: scopes derived env-var names
	sources      []recon.Source    // InputSettings.Sources: custom sources, after the declared files

	// described records whether an [InputSettings] was supplied at all, as distinct from
	// supplied empty; checkDescribed relies on it.
	described bool
}

// readerFor builds an InputReader from the Context's bound InputSettings, or from the zero
// InputSettings when none is bound.
func readerFor(rtx *Context) *InputReader {
	if rtx == nil {
		return NewInputReader(InputSettings{}) // the channel layer reports the nil context as a ParseError
	}
	meta, described := rtx.settingsForRun()
	// A [Program.WithInputReader] function wins. It receives the settings, so an override cannot
	// discard the configuration sources the spec declared.
	if fn := rtx.readerFn; fn != nil {
		if b := fn(meta); b != nil {
			b.described = described
			return b
		}
	}
	b := NewInputReader(meta)
	b.described = described
	return b
}

// NewInputReader returns the default input reader, configured from the generated
// [InputSettings]. It carries no features of its own: a read uses the features of the
// [Context] it reads, so a reader set with [Program.WithInputReader] reads with the program's.
func NewInputReader(meta InputSettings) *InputReader {
	return &InputReader{
		parser: NewParser(), configFiles: meta.ConfigFiles, stdinSchemas: meta.StdinSchemas,
		envPrefix: meta.EnvPrefix, sources: meta.Sources, described: true,
	}
}

// Read fills out, a non-nil pointer to the generated inputs struct, from every channel. Flag and
// argument validation runs once over the reconciled values, so a required flag is satisfiable
// from env or config and an env- or config-supplied value is still enum-checked.
//
// It returns the first error: a [*ParseError] from the argv channel or an [*InputError] from the
// others, with the recon cause reachable via errors.As, or a [*WiringError] when a command
// declares config inputs but the program has no [InputSettings], or when reading needs a
// [Feature] the program built with [NewWiredProgram] does not wire (its Feature names it).
func (b *InputReader) Read(rtx *Context, out any) error { return b.bind(rtx, out) }

func (b *InputReader) bind(rtx *Context, out any) error {
	if b == nil {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: nil input reader"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: Read out argument must be a non-nil pointer to an inputs struct"}
	}

	// 1. argv → a store, without validation: step 3 checks the reconciled store.
	store, chain, err := parseArgvStore(b.parser, rtx, out)
	if err != nil {
		return err
	}
	v := rv.Elem()

	// A short-circuit flag set on argv waives every declared requirement below. Each channel
	// is still read, so an env value or a command line that cannot be read is still an error;
	// a configuration file that cannot be read or holds a value of the wrong type is skipped.
	waived := shortCircuited(chain, store)
	view := rtx.osView().withFileCache()
	fs := rtx.features()

	// 1b. Two-phase bootstrap: a config_source flag or env names the file step 4 reads, and a
	// profile selector names the profile it reads. The .env files in scope join the environment
	// inputs read (under the run's own), and variable_file variables are read, before any other
	// config_source path is looked up or any profile selected.
	boot := fileBootstrap{paths: b.pathOverrides(chain, store, view)}
	if view, err = b.envFileView(fs, chain, v, boot.paths, waived, view); err != nil {
		return err
	}
	if view.hasInputEnv() {
		boot.paths = b.pathOverrides(chain, store, view)
	}
	if boot.profiles, err = b.selectProfiles(fs, chain, store, view); err != nil {
		return err
	}

	// 1c. Declared path expansion of the argv values and defaults, then binding them into
	//     Flags and Arguments.
	anchor := frameAnchor(v, chain, rtx.frameIndex())
	if err := expandInputs(fs, v, chain, store, anchor, view); err != nil {
		return err
	}
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return err
	}

	// 2. Flag fallback: argv-set > env > config, recorded back into the store so step 3
	//    validates it too. A flag with no recon key keeps the Parser's value.
	if v.Kind() == reflect.Struct && hasReconFlags(v) {
		if fs.fallbacks == nil {
			return missingIn(chain, "fallbacks", "Fallbacks()", fallbackSubject(v))
		}
		if err := fs.fallbacks.reconcile(b, v, chain, store, boot, anchor, waived, view); err != nil {
			return err
		}
	}

	// 3. Validate the reconciled flags and arguments, once. Argv errors surface before any
	//    channel error. Only the commands out describes are judged, so a parent collecting its
	//    own inputs is not held to the leaf's requirements.
	if anchor >= 0 {
		store.spanBuf = [2]int{anchor, anchor + v.NumField()}
		store.span = &store.spanBuf
	}
	if err := validateStore(fs, chain, store); err != nil {
		return err
	}

	// Checked after argv validation so a bad command line reports itself first.
	if err := checkFrameFit(v, chain, rtx.frameIndex()); err != nil {
		return err
	}
	if fs.profiles != nil {
		if err := fs.profiles.check(b, v, chain, boot, waived, view); err != nil {
			return err
		}
	}

	if err := b.readChannels(fs, chain, boot, v, waived, view); err != nil {
		return err
	}

	// 5. stdin → the leaf command's typed payload, its only consumer.
	if _, _, ok := stdinTarget(v, waived); !ok {
		return nil // before fillStdin, so no closure is built for a command without stdin
	}
	return b.fillStdin(rtx, v, waived, func(name string) ([]string, error) { return leafArgValues(chain, store, name), nil })
}

// readChannels is step 4 of a read: env + config → the Env/Config sub-structs, from
// independent registries. A registry is built only when v describes inputs of that channel.
func (b *InputReader) readChannels(fs *featureSet, chain []Command, boot fileBootstrap, v reflect.Value, waived bool, view *osView) error {
	var err error
	var envReg *recon.Registry
	if hasEnvChannel(v) {
		if fs.env == nil {
			return missingIn(chain, "env", "Env()", fmt.Sprintf("command %q declares env inputs", pathOf(chain)))
		}
		if envReg, err = fs.env.registry(v, b.envPrefix, view); err != nil {
			return internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer envReg.Close()
	}

	if err := b.checkDescribed(v); err != nil {
		return err
	}

	var cfg *cfgRegs
	if hasConfigChannel(v) {
		if fs.config == nil {
			return missingIn(chain, "config-files", "ConfigFiles()", fmt.Sprintf("command %q declares config inputs", pathOf(chain)))
		}
		if cfg, err = fs.config.regs(b, chain, boot, v, waived, view); err != nil {
			return err
		}
		defer cfg.Close()
	}

	if envReg != nil || cfg != nil {
		labels := channelLabels{env: envNames(v, "Env", b.envPrefix), view: view}
		if err := fs.channels.fill(v, envReg, cfg, waived, labels); err != nil {
			return err
		}
		if hasPathRules(v) {
			if fs.expand == nil {
				return missingIn(chain, "path-expansion", "PathExpansion()", expandSubject(v))
			}
			if err := fs.expand.channels(v, envReg, cfg, waived, labels); err != nil {
				return err
			}
		}

		// 4b. The same constraint checks argv gets, over the values actually provided.
		if !waived {
			if err := fs.channels.validate(v, envReg, cfg, view); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateStore runs the argv channel's declarative checks over a reconciled store — required,
// enum and constraints, then flag groups, then flag dependencies — returning the first failure.
// A short-circuited run ([shortCircuited]) skips every requirement and keeps only what reading
// the command line needs.
func validateStore(fs *featureSet, chain []Command, store *parsedInputs) error {
	if err := validate(chain, store); err != nil {
		return err
	}
	if shortCircuited(chain, store) {
		return nil
	}
	if err := checkGroups(fs, chain, store); err != nil {
		return err
	}
	return checkDependencies(fs, chain, store)
}

// trimAcquiredPayload is the single trimming rule for text rotini reads on the user's behalf,
// shared by the stdin channel and the argv value sentinels (`--flag @file`, `--flag -`) so the
// same bytes yield the same value on either path. It removes one leading UTF-8 byte-order mark
// and one trailing line ending (artifacts of delivery, such as an editor's) and nothing else:
// leading and interior whitespace is content.
func trimAcquiredPayload(s string) string {
	s = strings.TrimPrefix(s, utf8BOM)
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// reconcileFlags overrides each fallback flag with its reconciled value: argv-set > env >
// config files. A flag present in no source keeps what the Parser bound, and argv-only flags
// are untouched. Each reconciled value is written back into store so the deferred validate
// pass sees it as present. A profile selector flag is never read from a configuration file: it
// takes the profile the bootstrap selected.
func (b *InputReader) reconcileFlags(v reflect.Value, chain []Command, store *parsedInputs, boot fileBootstrap, anchor int, waived bool, view *osView) error {
	if v.Kind() != reflect.Struct || !hasReconFlags(v) {
		return nil // no fallback flags → nothing to reconcile (env included)
	}
	fs := view.clockRef().features()
	cfgFiles := b.chainFiles(chain, false)
	files, err := b.wiredFileSources(fs, chain, cfgFiles, boot, waived, view)
	if err != nil {
		return err
	}
	rd := fallbackRead{view: view, waiveFiles: waived, files: sourcePaths(files)}
	srcs := make([]recon.Source, 0, 2+len(files))
	srcs = append(srcs, recon.NewMapSource("flags", flagOverrides(v, chain, store, anchor)), flagEnvSource(v, b.envPrefix, view))
	srcs = append(srcs, files...)
	reg, err := newRegistry(fs, recon.WithoutWatch(), recon.WithSources(srcs...))
	if err != nil {
		return internalBind(channelFlag, "", "could not build the flag-fallback registry", err)
	}
	defer reg.Close()

	if anchor < 0 {
		return nil // a struct that does not fit the chain; checkFrameFit reports it
	}
	selectors := boot.selectorFlags(cfgFiles)
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
		if !flags.IsValid() {
			continue
		}
		for sf, f := range structLeaves(flags) {
			if choice, ok := selectors[sf.Tag.Get("rotini")]; ok {
				if err := applySelector(f, sf.Tag.Get("rotini"), chain, store, anchor+i, choice, view); err != nil {
					return err
				}
				continue
			}
			if err := reconcileFlag(reg, f, sf, chain, store, anchor+i, rd); err != nil {
				return err
			}
		}
	}
	return reconcileArgs(reg, v, chain, store, anchor, rd)
}

// firstSetEnv returns the first of an env tag's comma-separated variable names that is set
// (non-empty) in the run's environment, or "" when none is. `variable: [GH_TOKEN, GITHUB_TOKEN]`
// generates env:"GH_TOKEN,GITHUB_TOKEN": the first name is preferred, the rest are the
// spellings other tools use for the same thing.
func firstSetEnv(view *osView, names string) string {
	for name := range strings.SplitSeq(names, ",") {
		if name != "" && view.inputGetenv(name) != "" {
			return name
		}
	}
	return ""
}

// chosenEnv is the one variable an env tag binds to: the first of its names that is set, else
// the first name — so an unset input still reports the preferred spelling.
func chosenEnv(view *osView, names string) string {
	if set := firstSetEnv(view, names); set != "" {
		return set
	}
	first, _, _ := strings.Cut(names, ",")
	return first
}

// fallbackRead is how a flag's fallback is read: the run's view, which names the variable an
// error reports, whether a short-circuited run skips a configuration file's value that the
// flag's type cannot hold, and the file each configuration source read, by logical name.
type fallbackRead struct {
	view       *osView
	waiveFiles bool
	files      map[string]string
	selectors  map[string]profileChoice // profile selector flags, which a configuration file never sets
}

// reconcileFlag binds one fallback flag, at chain frame idx, from the registry: argv > env >
// config. A flag the user set on the command line is left as the Parser bound it.
func reconcileFlag(reg *recon.Registry, field reflect.Value, sf reflect.StructField, chain []Command, store *parsedInputs, idx int, rd fallbackRead) error {
	key := reconKey(sf.Tag.Get("recon"))
	if key == "" {
		return nil
	}
	name := sf.Tag.Get("rotini")
	var def FlagDef
	if idx < len(chain) {
		def, _ = findFlagDef(chain[idx].Flags, name)
	}
	// Argv outranks every fallback and the Parser already bound it; re-reading it through the
	// registry would collapse a list flag's values into one string.
	if store != nil && idx < len(store.argvSet) && store.argvSet[idx][name] {
		return nil
	}
	vals, origin, err := bindFlagFallback(reg, field, sf.Tag, key, def, chain, idx, rd)
	if err != nil || vals == nil {
		return err
	}
	recordFlag(store, idx, name, vals)
	recordOrigin(store, idx, name, origin)
	return nil
}

// bindFlagFallback reads one flag's fallback from reg and binds it into field, returning the
// argv-shaped values it bound, or nil when no source supplies one. The InputReader and the
// overlay's env and files layers share it: a list binds item by item, a map from its leaves, a
// string splits on the flag's separator, a case-insensitive enum binds its declared spelling,
// and a value the type cannot hold is an error naming where it came from. With rd.waiveFiles,
// such a value from a configuration file is skipped instead, and the field keeps what it held.
func bindFlagFallback(reg *recon.Registry, field reflect.Value, tag reflect.StructTag, key string, def FlagDef, chain []Command, idx int, rd fallbackRead) (vals []string, origin string, err error) {
	name := tag.Get("rotini")
	val, found, err := reg.Get(key)
	if err != nil {
		return nil, "", reconBind(channelFlag, err)
	}
	found = found && val.Kind() != recon.NullKind // a null (`mode:`) leaves the default in place
	source := val.Source()
	switch {
	case found:
		if vals, err = fallbackValues(val, def.Separator); err != nil {
			if rd.waiveFiles && source != osEnvSourceName {
				return nil, "", nil
			}
			return nil, "", fallbackCoerceError(chain, idx, name, fallbackOrigin(rd, source, tag.Get("env"), key), err)
		}
		if val.Kind() == recon.MapKind && isObjectFlag(&def) {
			quoteObjectPairs(vals) // an object's config block read as one map value
		}
	case field.Kind() == reflect.Map || (isObjectFlag(&def) && field.Kind() != reflect.Slice):
		// recon keeps a config map — or an object flag's config block — as its leaves
		// (labels.k, labels.x), not at the map's key. For an object each leaf is one
		// key=value occurrence, and the occurrences merge.
		if vals, source, err = mapLeaves(reg, key); err != nil {
			return nil, "", reconBind(channelFlag, err)
		}
		if isObjectFlag(&def) {
			quoteObjectPairs(vals)
		}
		if len(vals) == 0 {
			return nil, "", nil
		}
	default:
		return nil, "", nil
	}
	origin = fallbackOrigin(rd, source, tag.Get("env"), key)
	vals = flagEnum(&def).canonical(vals)
	waivable := rd.waiveFiles && source != osEnvSourceName
	if vals, err = expandFallbackWired(vals, tag, source, fallbackLabel(chain, idx, name), rd); err != nil {
		if we, ok := errors.AsType[*WiringError](err); ok {
			return nil, "", we
		}
		if waivable {
			return nil, "", nil
		}
		return nil, "", usageBind(channelFlag, name, err.Error()+fromSource(origin), err)
	}
	vals = fallbackTimes(field.Type(), def, vals)
	var prev reflect.Value
	if waivable {
		prev = reflect.New(field.Type()).Elem()
		prev.Set(field)
	}
	// A fallback value the flag's type cannot hold is a user error, as a bad argv value is.
	if err := coerceFlagValues(field, def, vals, rd.view.clockRef()); err != nil {
		if we, ok := errors.AsType[*WiringError](err); ok {
			return nil, "", we
		}
		if waivable {
			field.Set(prev)
			return nil, "", nil
		}
		return nil, "", fallbackCoerceError(chain, idx, name, origin, err)
	}
	if vals == nil {
		vals = []string{} // supplied, and empty: still present
	}
	return vals, origin, nil
}

// fallbackTimes reads the fallback values of a time flag that declares no layout or relative
// form as an env or config input of type time reads them, so the two agree on a value from the
// same variable or file: a value the config library binds to a time is rewritten in RFC 3339,
// and any other is kept for the flag's own check to report.
func fallbackTimes(t reflect.Type, def FlagDef, vals []string) []string {
	if t = derefType(t); t.Kind() == reflect.Slice {
		t = derefType(t.Elem())
	}
	if t != timeType || def.Layout != "" || len(def.Layouts) > 0 || def.Relative != "" {
		return vals
	}
	vals = slices.Clone(vals)
	for i, v := range vals {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
			continue
		}
		if tv, ok := reconTime(v); ok {
			vals[i] = tv.Format(time.RFC3339Nano)
		}
	}
	return vals
}

// reconTime binds text to a time as the config library binds an env or config input's value.
func reconTime(s string) (time.Time, bool) {
	reg, err := recon.NewWithCodecs(nil, recon.WithoutWatch(), recon.WithSources(recon.NewMapSource("time", map[string]any{"t": s})))
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = reg.Close() }()
	var dst struct {
		T time.Time `recon:"t"`
	}
	if err := reg.Bind(&dst); err != nil {
		return time.Time{}, false
	}
	return dst.T, true
}

// mapLeaves collects the registry's leaf keys under key as sorted key=value entries relative
// to it (labels.k → k=v; deeper leaves keep their dots), with the source of the last one read.
func mapLeaves(reg *recon.Registry, key string) (entries []string, source string, err error) {
	prefix := key + "."
	for _, k := range reg.AllKeys() {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		v, found, err := reg.Get(k)
		if err != nil {
			return nil, "", err
		}
		if found {
			entries = append(entries, rest+"="+valueText(v))
			source = v.Source()
		}
	}
	sort.Strings(entries)
	return entries, source, nil
}

// quoteObjectPairs quotes the value of each key=value leaf in place: an object occurrence is
// CSV-split, so a comma in a value must stay inside it.
func quoteObjectPairs(leaves []string) {
	for i, leaf := range leaves {
		k, v, _ := strings.Cut(leaf, "=")
		leaves[i] = quotePair(k, v)
	}
}

// fallbackValues renders a flag's reconciled fallback as the argv-shaped strings the Parser
// would have produced: a list's items one by one, a map's entries as key=value (nested maps as
// dotted keys), and anything else as one string, split on the flag's separator when it has
// one.
func fallbackValues(val recon.Value, sep string) ([]string, error) {
	if val.Kind() == recon.SliceKind {
		items, _ := val.AsSlice()
		out := make([]string, len(items))
		for i, it := range items {
			if it.Kind() == recon.NullKind {
				return nil, nullItem(i)
			}
			out[i] = valueText(it)
			// A list of objects (a config file's list of mounts) goes through as JSON, one
			// occurrence per element — the form an object flag decodes.
			if it.Kind() == recon.MapKind {
				if raw, err := marshalPlain(it); err == nil {
					out[i] = string(raw)
				}
			}
		}
		return out, nil
	}
	if val.Kind() == recon.MapKind {
		m, _ := val.AsMap()
		var out []string
		flattenMap("", m, &out)
		sort.Strings(out)
		return out, nil
	}
	// The value's text, not AsString: AsString returns "" for a numeric or bool value.
	return splitValue(valueText(val), sep)
}

// valueText is a reconciled value as a flag reads it: a time, such as a TOML date-time, as RFC
// 3339 text, the form a JSON or YAML file writes it in, and anything else as its own text.
func valueText(v recon.Value) string {
	if t, ok := v.Any().(time.Time); ok {
		return t.Format(time.RFC3339Nano)
	}
	return v.String()
}

// nullItem is the error for a configuration list whose item i (from 0) is null, which no
// input type holds.
func nullItem(i int) error {
	return fmt.Errorf("list item %d is null", i+1)
}

// checkNullItems refuses a configuration list, read by a list field of the Config struct s,
// that holds a null item. A field pinned to one file is read from that file's registry.
func checkNullItems(s reflect.Value, cfg *cfgRegs) error {
	if s.Kind() != reflect.Struct {
		return nil
	}
	for f := range s.Type().Fields() {
		key := reconKey(f.Tag.Get("recon"))
		if key == "" || derefType(f.Type).Kind() != reflect.Slice {
			continue
		}
		reg := cfg.merged
		if pin := f.Tag.Get("cfgfile"); pin != "" {
			var err error
			if reg, err = cfg.For(pin); err != nil {
				return err
			}
		}
		val, found, err := reg.Get(key)
		if err != nil || !found || val.Kind() != recon.SliceKind {
			continue
		}
		items, _ := val.AsSlice()
		for i, it := range items {
			if it.Kind() == recon.NullKind {
				l := cfg.labels()
				return usageBind(channelConfig, key, l.name(channelConfig, key)+": "+nullItem(i).Error()+l.origin(val.Source()), nil)
			}
		}
	}
	return nil
}

// flattenMap appends a reconciled map's leaves as dotted key=value pairs.
func flattenMap(prefix string, m map[string]recon.Value, out *[]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if v.Kind() == recon.MapKind {
			sub, _ := v.AsMap()
			flattenMap(key, sub, out)
			continue
		}
		*out = append(*out, key+"="+valueText(v))
	}
}

// coerceFlagValues binds fallback values into a flag's field the way [bindFlags] binds argv
// values, dotted-key maps included.
func coerceFlagValues(f reflect.Value, def FlagDef, vals []string, clock *runClock) error {
	if isObjectFlag(&def) {
		fs := clock.features()
		if fs.objects == nil {
			return missingFeature("object-flags", "ObjectFlags()", fmt.Sprintf("flag --%s takes an object", def.Name))
		}
		return fs.objects.bind(f, vals, def, fs)
	}
	if def.DottedKeys {
		return coerceMapDotted(f, vals)
	}
	return coerceTime(f, vals, flagTimeSpec(&def, clock))
}

// fallbackOrigin names where a flag's fallback value came from in the user's terms: the
// environment variable they set, or the configuration file it was read from and the key that
// held it. recon's own source names ("osenv") mean nothing to them.
func fallbackOrigin(rd fallbackRead, source, envVar, key string) string {
	switch source {
	case "":
		return ""
	case osEnvSourceName:
		if envVar == "" {
			return "the environment"
		}
		name := chosenEnv(rd.view, envVar)
		return "environment variable " + name + rd.view.inputOrigin(name)
	}
	file := rd.files[source]
	if file == "" {
		return fmt.Sprintf("configuration source %q", source)
	}
	if profile := sourceProfile(source); profile != "" {
		file += " (profile " + profile + ")"
	}
	return fmt.Sprintf("configuration file %s, key %s", file, key)
}

// fallbackCoerceError reports a flag's env or config fallback value that its type cannot hold,
// naming the flag the way argv errors do and the source the value came from, since the user
// did not type it and would otherwise look for it on the command line.
func fallbackCoerceError(chain []Command, idx int, name, source string, err error) error {
	label, secret := name, false
	if idx >= 0 && idx < len(chain) {
		label = labelForFlag(chain[idx].Flags, name)
		if def, ok := findFlagDef(chain[idx].Flags, name); ok {
			secret = def.Secret
		}
	}
	if we, ok := errors.AsType[*WiringError](err); ok {
		return we
	}
	if mistake, ok := errors.AsType[authorMistake](err); ok {
		return internalBind(channelFlag, name, fmt.Sprintf("%s: %s", label, mistake), err)
	}
	msg := fmt.Sprintf("%s: %s", label, coerceMessage(err, secret)) + fromSource(source)
	return usageBind(channelFlag, name, msg, err)
}

// recordOrigin notes where a reconciled fallback value came from (see scopeInputs.origin), so a
// later error about the value names it. An empty origin clears any earlier note.
func recordOrigin(store *parsedInputs, idx int, name, origin string) {
	if store == nil || idx < 0 || idx >= len(store.scopes) {
		return
	}
	if origin == "" {
		delete(store.scopes[idx].origin, name)
		return
	}
	if store.scopes[idx].origin == nil {
		store.scopes[idx].origin = map[string]string{}
	}
	store.scopes[idx].origin[name] = origin
}

// recordFlag writes a reconciled flag value into the parsed store at its chain frame, so
// deferred validation treats it as present.
func recordFlag(store *parsedInputs, idx int, name string, values []string) {
	if store == nil || idx < 0 || idx >= len(store.scopes) {
		return
	}
	if store.scopes[idx].flags == nil {
		store.scopes[idx].flags = map[string][]string{}
	}
	store.scopes[idx].flags[name] = values
}

// hasReconFlags reports whether any command has a flag or argument with an env/config fallback
// to reconcile. When false, the input reader skips building a registry entirely.
func hasReconFlags(v reflect.Value) bool { return typeFact(&reconFlagsFacts, v, reconFlagsOf) }

var reconFlagsFacts sync.Map // reflect.Type → bool

func reconFlagsOf(v reflect.Value) bool {
	for _, field := range v.Fields() {
		for _, s := range [2]reflect.Value{commandFlags(field), commandArgs(field)} {
			if !s.IsValid() {
				continue
			}
			for sf := range typeLeaves(s.Type()) {
				if reconKey(sf.Tag.Get("recon")) != "" {
					return true
				}
			}
		}
	}
	return false
}

// flagOverrides maps the canonical key of every fallback flag explicitly set on argv to its
// value — the highest-precedence reconciliation layer. "Set" is what the parser recorded, so
// an identifier among a passthrough command's raw words doesn't count.
func flagOverrides(v reflect.Value, chain []Command, store *parsedInputs, offset int) map[string]any {
	m := map[string]any{}
	if offset < 0 || offset+v.NumField() > len(chain) {
		return m
	}
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
		if !flags.IsValid() {
			continue
		}
		for sf, f := range structLeaves(flags) {
			key := reconKey(sf.Tag.Get("recon"))
			if key == "" {
				continue
			}
			if store.argvSetAt(offset + i)[sf.Tag.Get("rotini")] {
				setNested(m, key, f.Interface())
			}
		}
	}
	return m
}

// setNested stores val at a dotted key path in m, creating nested maps as needed: recon walks
// nested maps, so a flat "create.color" key would not resolve.
func setNested(m map[string]any, key string, val any) {
	setObjectPath(m, strings.Split(key, "."), val, false)
}

// commandFlags returns the Flags sub-struct of a <Prefix>CommandInputs value, or an
// invalid Value when there is none.
func commandFlags(ci reflect.Value) reflect.Value {
	if ci.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	f := ci.FieldByName("Flags")
	if f.IsValid() && f.Kind() == reflect.Struct {
		return f
	}
	return reflect.Value{}
}

// reconKey extracts the canonical key (the part before the first comma) from a
// recon struct-tag body.
func reconKey(tag string) string {
	if tag == "" {
		return ""
	}
	if before, _, ok := strings.Cut(tag, ","); ok {
		return before
	}
	return tag
}

// reconHasSecret reports whether a recon struct-tag body carries the `secret` option,
// so the channel's value is redacted in constraint-violation errors (recon itself
// redacts it in the errors it raises).
func reconHasSecret(tag string) bool {
	for _, opt := range strings.Split(tag, ",")[1:] {
		if strings.TrimSpace(opt) == "secret" {
			return true
		}
	}
	return false
}

// findFlagDef finds a flag definition by its logical name.
func findFlagDef(defs []FlagDef, name string) (FlagDef, bool) {
	if d := flagDefRef(defs, name); d != nil {
		return *d, true
	}
	return FlagDef{}, false
}

// checkDescribed returns a [*WiringError] when the inputs struct has a Config channel but the
// program was built without any [InputSettings], which would otherwise leave every
// configuration value silently zero. An empty InputSettings is legal: it declares that the
// program has no configuration sources.
func (b *InputReader) checkDescribed(v reflect.Value) error {
	if b.described || !hasConfigChannel(v) {
		return nil
	}
	return &WiringError{Msg: "this command declares config: inputs, but the program " +
		"was built without InputSettings; call Program.WithInputSettings (the generated NewProgram " +
		"does) so the input reader knows where configuration lives"}
}

// hasConfigChannel reports whether any command frame in the inputs struct declares a
// non-empty Config sub-struct.
func hasConfigChannel(v reflect.Value) bool { return typeFact(&configChannelFacts, v, configChannelOf) }

var configChannelFacts sync.Map // reflect.Type → bool

func configChannelOf(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if cfg := ci.FieldByName("Config"); cfg.IsValid() && cfg.Kind() == reflect.Struct && cfg.NumField() > 0 {
			return true
		}
	}
	return false
}

// configRegistry builds a recon registry over the config_files, first (highest
// precedence) to last as declared. boot carries any config_source-supplied paths and the
// selected profiles.
func (b *InputReader) configRegistry(files []ConfigFile, boot fileBootstrap, keys valueKeys, waived bool, view *osView) (*recon.Registry, map[string]string, error) {
	srcs, err := b.fileSources(files, boot, waived, view)
	if err != nil {
		return nil, nil, err
	}
	paths := sourcePaths(srcs)
	for i, src := range srcs {
		srcs[i] = spellings{Source: src, keys: keys, clock: view.clockRef()}
	}
	reg, err := newRegistry(view.clockRef().features(), recon.WithoutWatch(), recon.WithSources(srcs...))
	if err != nil {
		return nil, nil, internalBind(channelConfig, "", "could not build the configuration registry", err)
	}
	return reg, paths, nil
}

// cfgRegs is the config channel's registries for one bind: the merged precedence chain plus
// lazily-built single-file registries for inputs the spec pins to one file.
type cfgRegs struct {
	reader  *InputReader
	files   []ConfigFile // sources in scope for the invoked chain, nearest-wins order
	boot    fileBootstrap
	keys    valueKeys // how config fields' text is read; see spellings
	merged  *recon.Registry
	perFile map[string]*recon.Registry
	waived  bool // a short-circuited run: a file that cannot be read, or a bad value, is skipped
	view    *osView
	paths   map[string]string // each source's file, by logical name, for messages
}

// configRegs builds the merged config registry and the lazy per-file cache over the sources in
// scope for chain. waived marks a short-circuited run ([shortCircuited]): a file that cannot be
// read or parsed is skipped, and none is checked against its schema.
func (b *InputReader) configRegs(chain []Command, boot fileBootstrap, v reflect.Value, waived bool, view *osView) (*cfgRegs, error) {
	files := b.chainFiles(chain, false)
	keys := channelValueKeys(v, "Config")
	merged, paths, err := b.configRegistry(files, boot, keys, waived, view)
	if err != nil {
		return nil, err
	}
	return &cfgRegs{reader: b, files: files, boot: boot, keys: keys, merged: merged, perFile: map[string]*recon.Registry{}, waived: waived, view: view, paths: paths}, nil
}

// chainConfigFiles returns the config_files in scope for the resolved chain, ordered
// nearest-wins: the invoked command's sources first, then each ancestor up to the root, each
// command's own declared order preserved. Off-branch sources are excluded; an unscoped source
// is always in scope and appended last.
func (b *InputReader) chainConfigFiles(chain []Command) []ConfigFile {
	if len(b.configFiles) == 0 {
		return nil
	}
	paths := make([]string, len(chain))
	for i := range chain {
		if i == 0 {
			paths[i] = chain[i].Name
		} else {
			paths[i] = paths[i-1] + "/" + chain[i].Name
		}
	}
	var out []ConfigFile
	for _, p := range slices.Backward(paths) { // leaf → root
		for _, f := range b.configFiles {
			if f.Scope == p {
				out = append(out, f)
			}
		}
	}
	for _, f := range b.configFiles {
		if f.Scope == "" {
			out = append(out, f)
		}
	}
	return out
}

// For returns the registry over only the named config_files entry — what a pinned
// input's value and required marker are judged against.
func (c *cfgRegs) For(name string) (*recon.Registry, error) {
	if reg, ok := c.perFile[name]; ok {
		return reg, nil
	}
	for _, f := range c.files {
		if f.Name != name {
			continue
		}
		srcs, err := c.reader.readFileSource(nil, f, c.boot, c.waived, c.view)
		if err != nil {
			return nil, err
		}
		if c.paths == nil {
			c.paths = map[string]string{}
		}
		maps.Copy(c.paths, sourcePaths(srcs))
		for i, src := range srcs {
			srcs[i] = spellings{Source: src, keys: c.keys, clock: c.view.clockRef()}
		}
		reg, err := newRegistry(c.view.clockRef().features(), recon.WithoutWatch(), recon.WithSources(srcs...))
		if err != nil {
			return nil, internalBind(channelConfig, name, fmt.Sprintf("could not build the registry for configuration file %q", name), err)
		}
		c.perFile[name] = reg
		return reg, nil
	}
	return nil, internalBind(channelConfig, name, fmt.Sprintf("input pinned to unknown configuration file %q", name), nil)
}

// labels names this channel's inputs and files for messages.
func (c *cfgRegs) labels() channelLabels {
	if c == nil {
		return channelLabels{}
	}
	return channelLabels{files: c.paths, view: c.view}
}

// Close closes the merged registry and every per-file registry built so far.
func (c *cfgRegs) Close() {
	if c == nil {
		return
	}
	c.merged.Close()
	for _, r := range c.perFile {
		r.Close()
	}
}

// bindReconWaived binds target from reg. In a short-circuited run (waived) it drops recon's
// requirement errors (a missing required key, an empty notEmpty key, a schema rule) and keeps
// those about reading a value, such as one of the wrong type. recon collects every field's
// error, so the fields that did resolve are still bound.
func bindReconWaived(reg *recon.Registry, target any, waived bool) error {
	err := reg.Bind(target)
	if err == nil || !waived {
		return err
	}
	if me, ok := err.(*recon.MultiError); ok { //nolint:errorlint // only recon's top-level aggregate is split
		var kept []error
		for _, e := range me.Errors {
			if !isRequirementError(e) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			return nil
		}
		return &recon.MultiError{Errors: kept}
	}
	if isRequirementError(err) {
		return nil
	}
	return err
}

// bindConfigWaived binds a Config struct from reg. A short-circuited run (waived) skips every
// failure, a value of the wrong type included: a configuration file never blocks --help.
// recon collects every field's error, so the fields that did resolve are still bound.
func bindConfigWaived(reg *recon.Registry, target any, waived bool) error {
	err := reg.Bind(target)
	anyAsJSONShape(reflect.ValueOf(target))
	if err != nil && !waived {
		return err
	}
	return nil
}

// anyAsJSONShape gives the values a configuration file binds into an untyped field (any, or a
// pointer, list or map holding any) the shape a JSON or YAML file gives them, whatever the
// format: a time, such as a TOML date-time, as RFC 3339 text. v is the bound struct, or a
// pointer to it.
func anyAsJSONShape(v reflect.Value) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for _, f := range v.Fields() {
		if !f.CanSet() || f.IsZero() {
			continue
		}
		if out, changed := shapeAny(f); changed {
			f.Set(out)
		}
	}
}

// holdsAny reports whether a value of type t can hold an untyped value: t is an interface, or a
// pointer, list or map of one.
func holdsAny(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return holdsAny(t.Elem())
	}
	return false
}

// shapeAny is v with the untyped values it holds in JSON's shape (see [jsonShape]). Whatever
// it changes is copied, never changed in place; changed reports whether v held a time.
func shapeAny(v reflect.Value) (out reflect.Value, changed bool) {
	if !holdsAny(v.Type()) {
		return v, false
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		o, ok := jsonShape(v.Interface())
		if !ok {
			return v, false
		}
		out = reflect.New(v.Type()).Elem()
		out.Set(reflect.ValueOf(o))
		return out, true
	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		e, ok := shapeAny(v.Elem())
		if !ok {
			return v, false
		}
		out = reflect.New(v.Type().Elem())
		out.Elem().Set(e)
		return out, true
	case reflect.Slice:
		for i := range v.Len() {
			if e, ok := shapeAny(v.Index(i)); ok {
				if !changed {
					out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
					reflect.Copy(out, v)
					changed = true
				}
				out.Index(i).Set(e)
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if e, ok := shapeAny(iter.Value()); ok {
				if !changed {
					out = reflect.MakeMapWithSize(v.Type(), v.Len())
					for k, e := range v.Seq2() {
						out.SetMapIndex(k, e)
					}
					changed = true
				}
				out.SetMapIndex(iter.Key(), e)
			}
		}
	}
	if !changed {
		return v, false
	}
	return out, true
}

// jsonShape is v with each time.Time as its RFC 3339 text. A map or list holding one is
// copied, never changed; changed reports whether v held one.
func jsonShape(v any) (out any, changed bool) {
	switch t := v.(type) {
	case time.Time:
		return t.Format(time.RFC3339Nano), true
	case map[string]any:
		var m map[string]any
		for k, e := range t {
			if o, ok := jsonShape(e); ok {
				if m == nil {
					m = maps.Clone(t)
				}
				m[k] = o
			}
		}
		if m == nil {
			return v, false
		}
		return m, true
	case []any:
		var s []any
		for i, e := range t {
			if o, ok := jsonShape(e); ok {
				if s == nil {
					s = slices.Clone(t)
				}
				s[i] = o
			}
		}
		if s == nil {
			return v, false
		}
		return s, true
	}
	return v, false
}

// isRequirementError reports whether a recon error is about a requirement rather than reading
// a value: a missing required key, an empty notEmpty key, or a schema rule.
func isRequirementError(err error) bool {
	var mre *recon.MissingRequiredError
	var eve *recon.EmptyValueError
	var ve *recon.ValidationError
	return errors.As(err, &mre) || errors.As(err, &eve) || errors.As(err, &ve)
}

// bindPinnedConfig re-binds each pinned field of one Config struct against only its own
// file's registry, required marker included. A pinned key absent from its file zeroes the
// field even when another file holds it.
func bindPinnedConfig(cs reflect.Value, regs *cfgRegs) error {
	st := cs.Type()
	byFile := map[string][]int{}
	for j := range st.NumField() {
		if name := st.Field(j).Tag.Get("cfgfile"); name != "" {
			byFile[name] = append(byFile[name], j)
		}
	}
	for file, idxs := range byFile {
		reg, err := regs.For(file)
		if err != nil {
			return err
		}
		fields := make([]reflect.StructField, 0, len(idxs))
		for _, j := range idxs {
			f := st.Field(j)
			fields = append(fields, reflect.StructField{Name: f.Name, Type: f.Type, Tag: f.Tag})
		}
		tmp := reflect.New(reflect.StructOf(fields))
		if err := bindConfigWaived(reg, tmp.Interface(), regs.waived); err != nil {
			return regs.labels().bindStruct(channelConfig, err, reg, tmp.Elem())
		}
		for i, j := range idxs {
			cs.Field(j).Set(tmp.Elem().Field(i))
		}
	}
	return nil
}

// fileSources builds one recon file source per config_files entry, in declared
// precedence order. Missing files are tolerated, and ~ and $VAR are expanded from the run's
// environment. A Discover strategy resolves its search directories now, the first directory
// containing the file winning. A path supplied through config_source is not optional, so a
// missing one is an error. Custom InputSettings.Sources follow the declared files and so rank
// below them.
func (b *InputReader) fileSources(files []ConfigFile, boot fileBootstrap, waived bool, view *osView) ([]recon.Source, error) {
	srcs := make([]recon.Source, 0, len(files)+len(b.sources))
	for _, f := range files {
		var err error
		if srcs, err = b.readFileSource(srcs, f, boot, waived, view); err != nil {
			return nil, err
		}
	}
	return append(srcs, b.sources...), nil
}

// namedSource renames a recon source to its config_files logical name, since recon source
// names must be unique and two entries may share a basename. The wrapper drops live-watch
// support, which the input reader does not use. path is the file it read, for messages.
// A key set to null (`mode:` or `mode: null`) reads as absent, so it supplies nothing and the
// next source or the declared default applies.
type namedSource struct {
	recon.Source

	name string
	path string
}

func (s namedSource) Name() string { return s.name }

func (s namedSource) Get(p recon.Path) (recon.Value, bool, error) {
	v, ok, err := s.Source.Get(p)
	if ok && err == nil && v.Kind() == recon.NullKind {
		return recon.Value{}, false, nil
	}
	return v, ok, err
}

// sourcePaths maps each configuration file source's logical name to the file it read.
func sourcePaths(srcs []recon.Source) map[string]string {
	out := map[string]string{}
	for _, s := range srcs {
		if ns, ok := s.(namedSource); ok && ns.path != "" {
			out[ns.name] = ns.path
		}
	}
	return out
}

// wiredFileSources is [InputReader.fileSources] through the configuration files hook: no
// sources when no file is in scope and no custom source is declared.
func (b *InputReader) wiredFileSources(fs *featureSet, chain []Command, files []ConfigFile, boot fileBootstrap, waived bool, view *osView) ([]recon.Source, error) {
	if len(files) == 0 && len(b.sources) == 0 {
		return nil, nil
	}
	if fs.config == nil {
		subject := "a flag falls back to a custom configuration source"
		if len(files) > 0 {
			subject = fmt.Sprintf("a flag falls back to configuration file %q", files[0].Name)
		}
		return nil, missingIn(chain, "config-files", "ConfigFiles()", subject)
	}
	return fs.config.fileSources(b, files, boot, waived, view)
}

// readFileSource builds the recon sources for one config_files entry, schema-checked: paths
// are expanded and made absolute with the run's view. It appends to dst the sources the file
// contributes, highest first: one, or for a file with profiles the selected profile's keys and
// then the shared keys. In a short-circuited run (waived) a file that cannot be located, read
// or parsed is skipped, standing in as an empty source under its own name so precedence holds,
// and no file is checked against its schema.
func (b *InputReader) readFileSource(dst []recon.Source, f ConfigFile, boot fileBootstrap, waived bool, view *osView) ([]recon.Source, error) {
	skipped := func() []recon.Source {
		return append(dst, namedSource{Source: recon.NewMapSource(f.Name, nil), name: f.Name})
	}
	fs := view.clockRef().features()
	if f.Profiles != nil && fs.profiles == nil {
		return nil, profilesMissing(f)
	}
	src, path, err := b.openFileSource(f, boot.paths, view)
	if err != nil {
		if waived && !isWiringError(err) {
			return skipped(), nil
		}
		return nil, err
	}
	var names []string
	choice := boot.profiles[f.Name]
	if f.Profiles != nil {
		if names, err = fs.profiles.names(f, src, path); err != nil {
			src.Close()
			if waived {
				return skipped(), nil
			}
			return nil, err
		}
	}
	if !waived {
		check := func() error { return validateConfigFileAs(fs, f, src, choice.name) }
		if err := view.files.check(checkedKey{name: f.Name, path: path, profile: choice.name}, check); err != nil {
			return nil, err
		}
	}
	if f.Profiles != nil {
		return append(dst, fs.profiles.sources(f, src, path, names, choice)...), nil
	}
	return append(dst, namedSource{Source: src, name: f.Name, path: path}), nil
}

// profilesMissing is the error for a profiled configuration file in a program that does not
// wire profiles.
func profilesMissing(f ConfigFile) *WiringError {
	return missingFeature("config-profiles", "Profiles()", fmt.Sprintf("configuration file %q has profiles", f.Name))
}

// isWiringError reports whether err is a [*WiringError], which no short-circuited run waives.
func isWiringError(err error) bool {
	_, ok := errors.AsType[*WiringError](err)
	return ok
}

// openFileSource locates and parses one config_files entry, returning the source and the file
// it resolved to. Rotini expands the path and runs the discover search itself, from the run's
// view, so recon reads exactly the absolute path it is given.
func (b *InputReader) openFileSource(f ConfigFile, overrides map[string]pathOverride, view *osView) (recon.Source, string, error) {
	opts := []recon.FileOption{recon.WithPathExpansion(false)}
	if f.Format != "" {
		opts = append(opts, recon.WithFileFormat(f.Format))
	}
	override, overridden := overrides[f.Name]
	var path string
	var dirs []string
	switch {
	case overridden:
		if override.emptyFlag != "" {
			return nil, "", usageBind(channelConfig, f.Name, override.emptyFlag+" was given an empty path; name a configuration file", nil)
		}
		path = override.path
		opts = append(opts, recon.WithOptional(false))
	case f.Discover != nil:
		var err error
		if dirs, err = discoverDirs(f.Discover, view); err != nil {
			if isWiringError(err) {
				return nil, "", err
			}
			return nil, "", internalBind(channelConfig, f.Name, fmt.Sprintf("could not resolve the search path for configuration file %q", f.Name), err)
		}
		if len(dirs) == 0 {
			return recon.NewMapSource(f.Name, nil), "", nil // nowhere to search: the file is absent
		}
		path = f.Discover.File
		opts = append(opts, recon.WithOptional(true))
	default:
		path = f.Path
		opts = append(opts, recon.WithOptional(true))
	}
	if !overridden {
		override = pathOverride{path: path, declared: true}
	}
	resolved, err := override.resolve(view)
	if err != nil {
		if isWiringError(err) {
			return nil, "", err
		}
		return nil, "", usageBind(channelConfig, f.Name, fmt.Sprintf("could not resolve configuration file path %s: %v", path, err), err)
	}
	if dirs != nil {
		resolved = searchFile(dirs, filepath.Base(resolved))
	}
	fs := view.clockRef().features()
	c, err := fs.codecFor(f.Format, resolved)
	if err != nil {
		return nil, "", err
	}
	key := openedKey{name: f.Name, path: resolved, format: f.Format, optional: !overridden}
	if o, ok := view.files.opened(key); ok {
		return o.src, resolved, o.err
	}
	src, err := newFileSource(fs, resolved, c, opts...)
	if err != nil {
		// The file's content or path is the user's to fix.
		err = usageBind(channelConfig, f.Name, configFileProblem(resolved, err), err)
		view.files.keep(key, openedFile{err: err})
		return nil, "", err
	}
	view.files.keep(key, openedFile{src: src})
	return src, resolved, nil
}

// fileCache holds the configuration files one read of the inputs has opened, so each is read,
// decoded and checked against its schema once however many registries the read builds. A
// file source is read only after it is opened, so one can serve every registry. A nil cache
// keeps nothing.
type fileCache struct {
	files   map[openedKey]openedFile
	checked map[checkedKey]error
}

// openedKey identifies one opening of a configuration file: the entry, the file it resolved to,
// and how it was opened.
type openedKey struct {
	name, path, format string
	optional           bool
}

// openedFile is the result of opening one: its source, or why it could not be opened.
type openedFile struct {
	src recon.Source
	err error
}

// checkedKey identifies one schema check of a configuration file, as read under a profile.
type checkedKey struct{ name, path, profile string }

// withFileCache returns a copy of v that keeps the configuration files it opens, for one read.
func (v *osView) withFileCache() *osView {
	c := &cachedView{view: osView{foldCase: foldCaseDefault()}} // one allocation for both
	if v != nil {
		c.view = *v
	}
	c.view.files = &c.cache // its maps are made on first use
	return &c.view
}

// cachedView is a view and its file cache, allocated together.
type cachedView struct {
	view  osView
	cache fileCache
}

func (c *fileCache) opened(k openedKey) (openedFile, bool) {
	if c == nil {
		return openedFile{}, false
	}
	o, ok := c.files[k]
	return o, ok
}

func (c *fileCache) keep(k openedKey, o openedFile) {
	if c != nil {
		if c.files == nil {
			c.files = map[openedKey]openedFile{}
		}
		c.files[k] = o
	}
}

// check runs a schema check once per key, returning its first result after.
func (c *fileCache) check(k checkedKey, run func() error) error {
	if c == nil {
		return run()
	}
	if err, ok := c.checked[k]; ok {
		return err
	}
	err := run()
	if c.checked == nil {
		c.checked = map[checkedKey]error{}
	}
	c.checked[k] = err
	return err
}

// searchFile returns the first of dirs holding name, else name in the first directory, as a
// discover search resolves it.
func searchFile(dirs []string, name string) string {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(dirs) == 0 {
		return name
	}
	return filepath.Join(dirs[0], name)
}

// configFileProblem says why a configuration file could not be used: where a syntax error is
// and what it is, or why the file could not be read.
func configFileProblem(path string, err error) string {
	if de, ok := errors.AsType[*toml.DuplicateKeyError](err); ok {
		at := strconv.Itoa(de.Pos.Line)
		if de.Pos.Column > 0 {
			at += ":" + strconv.Itoa(de.Pos.Column)
		}
		return fmt.Sprintf("could not parse configuration file %s:%s: duplicate key %q", path, at, de.Key)
	}
	if pe, ok := errors.AsType[*recon.ParseError](err); ok {
		loc := path
		if at := pe.Position.String(); at != "" {
			loc += ":" + at
		}
		return fmt.Sprintf("could not parse configuration file %s: %s", loc, decodeMessage(pe))
	}
	reason := "cannot be read: " + innermostCause(err).Error()
	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info.IsDir():
		reason = "is a directory"
	case errors.Is(err, fs.ErrNotExist):
		reason = "no such file"
	case errors.Is(err, fs.ErrPermission):
		reason = "permission denied"
	case errors.Is(err, recon.ErrFileTooLarge):
		reason = fmt.Sprintf("is larger than the %d MiB limit", recon.MaxFileSize>>20)
	case errors.Is(err, recon.ErrUnsupportedFormat):
		reason = "unsupported format"
	}
	return fmt.Sprintf("could not open configuration file %s: %s", path, reason)
}

// innermostCause returns the error at the end of err's chain, which names the cause without
// the paths and prefixes the layers above it add.
func innermostCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if multi, ok := errors.AsType[interface {
			error
			Unwrap() []error
		}](err); ok && next == nil {
			if errs := multi.Unwrap(); len(errs) > 0 {
				next = errs[0]
			}
		}
		if next == nil {
			return err
		}
		err = next
	}
}

// absPath makes p absolute against the run's directory, or the process working directory when
// none was injected.
func absPath(view *osView, p string) (string, error) {
	if p == "" {
		return "", nil
	}
	return filepath.Abs(view.abs(p))
}

// validateConfigFile checks one config_files entry's loaded document against its
// declared schema — the same load-time gate stdin applies to its payload — against the file
// recon actually resolved. An absent optional file passes vacuously: shape validation gates
// what is loaded, and absence is the per-input required marker's concern.
func validateConfigFile(fs *featureSet, f ConfigFile, src recon.Source) error {
	return validateConfigFileAs(fs, f, src, "")
}

// validateConfigFileAs is [validateConfigFile] for a run that selected profile: a file with
// profiles is checked as the run reads it, its shared keys merged with the selected profile's
// (see effectiveDocument). Profiles that aren't selected are not checked.
func validateConfigFileAs(fs *featureSet, f ConfigFile, src recon.Source, profile string) error {
	if f.Schema == "" {
		return nil
	}
	fsrc, ok := src.(*recon.FileSource)
	if !ok {
		return nil
	}
	path := fsrc.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // absent: vacuous
		}
		// A present-but-unreadable file the user controls.
		return usageBind(channelConfig, f.Name, configFileProblem(path, err), err)
	}
	if fs.schemas == nil {
		return missingFeature("schemas", "Schemas()", fmt.Sprintf("configuration file %q has a schema", f.Name))
	}
	c, err := fs.codecFor(fsrc.Format(), path)
	if err != nil {
		return err
	}
	if c == nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("unsupported format %q for configuration file %s", fsrc.Format(), path), nil)
	}
	m, err := c.codec().Decode(stripBOM(data))
	if err != nil {
		pe := fileParseError(path, err)
		return usageBind(channelConfig, f.Name, configFileProblem(path, pe), pe)
	}
	if f.Profiles != nil {
		m = effectiveDocument(m, f.Profiles.Under, profile)
	}
	validator, err := fs.schemas.validator(f.Schema)
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		fs.schemas.patternMessages(f.Schema, err)
		where := path
		if f.Profiles != nil && profile != "" {
			where += " (profile " + profile + ")"
		}
		return usageBind(channelConfig, f.Name,
			fmt.Sprintf("configuration file %s %s", where, schemaVerdict(err, "is invalid")), err)
	}
	return nil
}

// pathOverrides resolves each ConfigFile's config_source inputs to the path they supply: the
// flag explicitly set on argv, then the env variable, then the flag's default. An entry none
// of them supplies keeps its own path or discover strategy.
func (b *InputReader) pathOverrides(chain []Command, store *parsedInputs, view *osView) map[string]pathOverride {
	var out map[string]pathOverride // made by the first override: read-only, so nil serves none
	for _, f := range b.chainConfigFiles(chain) {
		pf := f.PathFrom
		if pf == nil {
			continue
		}
		if out == nil {
			out = map[string]pathOverride{}
		}
		explicit, defaulted := storeFlagValue(store, pf.Flag)
		switch {
		case explicit == "" && storeFlagSet(store, pf.Flag):
			out[f.Name] = pathOverride{emptyFlag: chainFlagLabel(chain, pf.Flag)}
		case explicit != "":
			out[f.Name] = pathOverride{path: explicit}
		case firstSetEnv(view, pf.Env) != "":
			out[f.Name] = pathOverride{path: view.inputGetenv(firstSetEnv(view, pf.Env))}
		case defaulted != "":
			out[f.Name] = pathOverride{path: defaulted, declared: true}
		}
	}
	return out
}

// pathOverride is the path a config_source input supplies. A path the user gave, on the
// command line or in a variable, has only a leading ~ expanded (a shell leaves ~ alone in
// --config=~/app.yaml and in a variable set outside a shell), and ~user only when the program
// has path expansion; any $ is kept as written. The flag's declared default is expanded like a
// declared path.
type pathOverride struct {
	path     string
	declared bool

	// emptyFlag is the flag given an empty path on the command line, which names no file: a
	// usage error, not a fall back to the variable or the default.
	emptyFlag string
}

// resolve makes the path absolute against the run's directory, expanding it first.
func (o pathOverride) resolve(view *osView) (string, error) {
	p := o.path
	if p != "" {
		var err error
		if o.declared {
			p, err = view.expandPath(p)
		} else {
			p, err = view.expandGivenTilde(p)
		}
		if err != nil {
			return "", err
		}
	}
	return absPath(view, p)
}

// expandGivenTilde expands a leading ~ of a path the user gave. ~user is expanded only when
// the program has path expansion, and is otherwise kept as written.
func (v *osView) expandGivenTilde(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	if end := strings.IndexAny(p, `/\`); end != 1 && len(p) > 1 {
		if x := v.clockRef().features().expand; x == nil || x.lookupUser == nil {
			return p, nil
		}
	}
	return v.expandTilde(p)
}

// storeFlagSet reports whether flag name was set on argv anywhere on the chain.
func storeFlagSet(store *parsedInputs, name string) bool {
	if name == "" || store == nil {
		return false
	}
	for i := range store.scopes {
		if len(store.scopes[i].flags[name]) > 0 && store.setOnArgv(i, name) {
			return true
		}
	}
	return false
}

// chainFlagLabel is flag name as messages name it, from the nearest command that declares it.
func chainFlagLabel(chain []Command, name string) string {
	for _, c := range slices.Backward(chain) {
		if flagDefRef(c.Flags, name) != nil {
			return labelForFlag(c.Flags, name)
		}
	}
	return "--" + name
}

// storeFlagValue finds a flag's parsed value across the chain, split by whether it was set on
// argv or filled by its default. The last value wins for a repeated flag.
func storeFlagValue(store *parsedInputs, name string) (explicit, defaulted string) {
	if name == "" || store == nil {
		return "", ""
	}
	for i := range store.scopes {
		vals := store.scopes[i].flags[name]
		if len(vals) == 0 {
			continue
		}
		if store.setOnArgv(i, name) {
			explicit = vals[len(vals)-1]
		} else {
			defaulted = vals[len(vals)-1]
		}
	}
	return explicit, defaulted
}

// discoverDirs resolves a Discover strategy to the ordered directories searched, from the run's
// view; see [DiscoverDef.Strategy].
func discoverDirs(d *DiscoverDef, view *osView) ([]string, error) {
	switch d.Strategy {
	case "walk-up":
		dir, err := view.getwd()
		if err != nil {
			return nil, fmt.Errorf("walk-up discovery: %w", err)
		}
		var dirs []string
		for {
			dirs = append(dirs, dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				return dirs, nil
			}
			dir = parent
		}
	case "xdg":
		// XDG-literal on every platform.
		dir, err := view.xdgConfigDir(d.App)
		if err != nil {
			return nil, fmt.Errorf("xdg discovery: %w", err)
		}
		dir, err = absPath(view, dir)
		if err != nil {
			return nil, fmt.Errorf("xdg discovery: %w", err)
		}
		return []string{dir}, nil
	case "native":
		return discoverNativeDirs(d, view)
	case "xdg-system":
		return discoverSystemDirs(d, view)
	default:
		return nil, fmt.Errorf("unknown discover strategy %q", d.Strategy)
	}
}

// envSources builds the env channel's recon source: each Env input's declared variable, read
// through the run's view, by its explicit name or the SNAKE_UPPER projection of its key under
// the optional env prefix. Nested env families are not registry data — recon resolves leaf
// keys only, so fillEnvNested sets those fields directly.
func envSources(v reflect.Value, envPrefix string, view *osView) []recon.Source {
	return []recon.Source{spellings{Source: newDeclaredEnv(v, "Env", envPrefix, view), keys: channelValueKeys(v, "Env"), clock: view.clockRef()}}
}

// envRegistry is the env channel's registry over [envSources].
func envRegistry(v reflect.Value, envPrefix string, view *osView) (*recon.Registry, error) {
	return newRegistry(view.clockRef().features(), recon.WithoutWatch(), recon.WithSources(envSources(v, envPrefix, view)...))
}

// spellings gives env and config inputs the value spellings flags accept and recon's decode
// does not: days and weeks in a duration (7d), yes/no, on/off and y/n for a bool, and the
// numbers 1 and 0 a typed configuration format holds for one (see parseBool), a declared
// layout or relative form for a time field instead of RFC 3339, and
// decimal integers, where a leading zero does not mean octal, plus the 0x, 0o and 0b literals
// of an integer field that declares base: prefixed. Only the keys of those typed fields are
// rewritten; a string input whose value is "yes" keeps it.
type spellings struct {
	recon.Source

	keys  valueKeys
	clock *runClock // what relative times are measured from
}

// valueKeys maps the recon keys of an Env or Config struct's bool fields, its duration fields,
// its time fields that declare layouts or relative forms, its map fields, and its plain integer
// fields, to how their text is read.
type valueKeys struct {
	bools     map[string]bool
	times     map[string]timeSpec
	timeMaps  map[string]timeSpec // map[string]time.Time fields, whose values are times
	maps      map[string]bool     // every map field, which a configuration file holds as leaves
	durations map[string]bool
	prefixed  map[string]prefixedKey
}

// prefixedKey is how an integer field's text arrives: one value, or a list, which a string
// holds split on sep. prefixed is the field's base: prefixed; without it the text is decimal.
type prefixedKey struct {
	list     bool
	bytes    bool // a []uint8, which a string would otherwise bind as its bytes
	sep      string
	prefixed bool
}

// text is one value of the field in decimal: a prefixed literal converted, or decimal digits
// without the leading zeros the config library would read as octal. Anything else is kept, for
// validation to report.
func (k prefixedKey) text(s string) string {
	if k.prefixed {
		return prefixedText(s)
	}
	return plainDecimalText(s)
}

// decimal spells v, an integer field's value, in decimal, so binding reads it as the command
// line does: a string, each part of a list string, or each string in a list.
func (k prefixedKey) decimal(v recon.Value) recon.Value {
	switch v.Kind() {
	case recon.StringKind:
		s := v.String()
		if !k.list {
			return recon.NewValue(k.text(s))
		}
		if s == "" {
			return v
		}
		sep := k.sep
		if sep == "" {
			sep = "," // recon's default
		}
		parts := strings.Split(s, sep)
		if k.bytes {
			items := make([]recon.Value, len(parts))
			for i, p := range parts {
				items[i] = recon.NewValue(k.text(strings.TrimSpace(p)))
			}
			return recon.NewValue(items)
		}
		for i, p := range parts {
			parts[i] = k.text(strings.TrimSpace(p))
		}
		return recon.NewValue(strings.Join(parts, sep))
	case recon.SliceKind:
		items, err := v.AsSlice()
		if err != nil {
			return v
		}
		out := make([]recon.Value, len(items))
		for i, it := range items {
			out[i] = it
			if it.Kind() == recon.StringKind {
				out[i] = recon.NewValue(k.text(it.String()))
			}
		}
		return recon.NewValue(out)
	}
	return v
}

func (s spellings) Get(path recon.Path) (recon.Value, bool, error) {
	v, found, err := s.Source.Get(path)
	if !found || err != nil {
		return v, found, err
	}
	if tv, ok := s.timeMapValue(path, v); ok {
		return tv, true, nil
	}
	if k, ok := s.keys.prefixed[path.String()]; ok {
		return k.decimal(v), true, nil
	}
	key := path.String()
	if v.Kind() == recon.IntKind && s.keys.bools[key] {
		// A number reads as the command line reads its text: 1 and 0 are true and false.
		if b, perr := parseBool(v.String()); perr == nil {
			return recon.NewValue(b), true, nil
		}
	}
	if v.Kind() != recon.StringKind {
		return v, found, err
	}
	if s.keys.bools[key] {
		if b, perr := parseBool(v.String()); perr == nil {
			return recon.NewValue(b), true, nil
		}
	}
	if s.keys.durations[key] {
		if d, perr := parseDuration(strings.TrimSpace(v.String())); perr == nil {
			return recon.NewValue(d), true, nil
		}
	}
	if ts, ok := s.keys.times[key]; ok {
		// An unparseable value passes through as text so recon reports it as a usage error
		// naming the input; an error returned from Get would be swallowed.
		ts.clock = s.clock
		if t, perr := parseTimeValue(v.String(), ts); perr == nil {
			return recon.NewValue(t), true, nil
		}
	}
	return v, found, err
}

// channelValueKeys collects [valueKeys] from each command's Env or Config struct.
func channelValueKeys(v reflect.Value, structName string) valueKeys {
	keys := valueKeys{bools: map[string]bool{}, times: map[string]timeSpec{}, timeMaps: map[string]timeSpec{}, maps: map[string]bool{}, durations: map[string]bool{}, prefixed: map[string]prefixedKey{}}
	if v.Kind() != reflect.Struct {
		return keys
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		ch := ci.FieldByName(structName)
		if !ch.IsValid() || ch.Kind() != reflect.Struct {
			continue
		}
		ct := ch.Type()
		for j := range ch.NumField() {
			sf := ct.Field(j)
			key := reconKey(sf.Tag.Get("recon"))
			if key == "" {
				continue
			}
			if plainIntegerField(sf.Type) {
				ft := derefType(sf.Type)
				list := ft.Kind() == reflect.Slice
				keys.prefixed[key] = prefixedKey{list: list, bytes: list && ft.Elem().Kind() == reflect.Uint8, sep: recon.ParseTag(sf.Tag.Get("recon")).Separator, prefixed: sf.Tag.Get("base") == basePrefixed}
				continue
			}
			switch ft := derefType(sf.Type); {
			case ft.Kind() == reflect.Bool:
				keys.bools[key] = true
			case ft == durationType:
				keys.durations[key] = true
			case ft == timeType:
				if ts := tagTimeSpec(sf.Tag); !ts.plain() {
					keys.times[key] = ts
				}
			case ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String:
				keys.maps[key] = true
				if ts := tagTimeSpec(sf.Tag); ft.Elem() == timeType && !ts.plain() {
					keys.timeMaps[key] = ts
				}
			}
		}
	}
	return keys
}

// flagEnvSource is the env source flag fallbacks read: the SNAKE_UPPER projection of each
// recon key, scoped under env_prefix when one is declared, except that a flag naming its own
// variable (schema `variable:`) reads that exact name. As with env inputs, an explicitly named
// variable is exempt from env_prefix. A variable that is set but empty counts as unset, so the
// fallback falls through to the configuration file and default, matching [firstSetEnv].
func flagEnvSource(v reflect.Value, envPrefix string, view *osView) recon.Source {
	src := newDeclaredEnv(v, "Flags", envPrefix, view)
	src.emptyAbsent = true
	return src
}

// fillEnvNested fills each nested env input of one Env struct from its variable family: with
// BASE=ACME_HTTP and sep=__, ACME_HTTP__RETRY__MAX=9 binds {retry: {max: "9"}}. Segments are
// lowercased and values stay strings. It returns the recon keys it filled and errors on a
// required family with no variables.
func fillEnvNested(env reflect.Value, view *osView) (map[string]bool, error) {
	filled := map[string]bool{}
	if env.Kind() != reflect.Struct {
		return filled, nil
	}
	et := env.Type()
	for j := range env.NumField() {
		base, rest, ok := strings.Cut(et.Field(j).Tag.Get("envnest"), ",")
		if !ok || base == "" {
			continue
		}
		sep, opt, _ := strings.Cut(rest, ",")
		name := et.Field(j).Tag.Get("rotini")
		fam, err := envFamily(view, base, sep)
		if err != nil {
			return filled, usageBind(channelEnv, name, err.Error(), nil)
		}
		if len(fam) == 0 {
			if opt == "required" {
				return filled, usageBind(channelEnv, name,
					fmt.Sprintf("environment variables %s%s* are required for %s", base, sep, name), nil)
			}
			continue
		}
		f := env.Field(j)
		if f.CanSet() && reflect.TypeFor[map[string]any]().AssignableTo(f.Type()) {
			f.Set(reflect.ValueOf(fam))
			filled[reconKey(et.Field(j).Tag.Get("recon"))] = true
		}
	}
	return filled, nil
}

// envFamily collects the BASE<sep>… variables of the run's environment into a nested map. The
// prefix matches as the environment compares names (ignoring case on Windows). An empty
// segment (a doubled separator), or a variable that sets a key another one nests under, is an
// error naming the variables.
func envFamily(view *osView, base, sep string) (map[string]any, error) {
	out := map[string]any{}
	if sep == "" {
		return out, nil
	}
	prefix := view.key(base + sep)
	type member struct{ name, val string }
	var fam []member
	for _, kv := range view.inputEnviron() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || len(name) <= len(prefix) || view.key(name[:len(prefix)]) != prefix {
			continue
		}
		fam = append(fam, member{name, val})
	}
	slices.SortFunc(fam, func(a, b member) int { return strings.Compare(a.name, b.name) })
	type use struct {
		name  string // the first variable that set the key or a key under it
		value bool   // that variable set the key itself
	}
	owner := map[string]use{}
	for _, m := range fam {
		segs := strings.Split(strings.ToLower(m.name[len(prefix):]), strings.ToLower(sep))
		if slices.Contains(segs, "") {
			return nil, fmt.Errorf("environment variable %s has an empty key segment (a doubled %s)", m.name, sep)
		}
		for k := range segs {
			path := strings.Join(segs[:k+1], ".")
			value := k == len(segs)-1
			other, seen := owner[path]
			switch {
			case !seen:
				owner[path] = use{name: m.name, value: value}
			case value || other.value:
				return nil, fmt.Errorf("environment variables %s and %s both set %s", other.name, m.name, strings.Join(segs[:k+1], sep))
			}
		}
		setNested(out, strings.Join(segs, "."), m.val)
	}
	return out, nil
}

// fillChannels walks a <Cmd>Inputs struct and recon-binds each command's Env struct from
// envReg and its Config struct from cfg. A short-circuited run (waived) binds what resolves and
// ignores missing required values, and skips a configuration value of the wrong type. A nil
// registry belongs to a channel the struct describes no inputs of. labels names the env inputs
// in errors.
func fillChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		t := ci.Type()
		for j := range ci.NumField() {
			switch t.Field(j).Name {
			case "Env":
				if envReg == nil {
					continue
				}
				if err := checkEnvMaps(ci.Field(j), envReg, labels); err != nil {
					return err
				}
				if err := bindReconWaived(envReg, ci.Field(j).Addr().Interface(), waived); err != nil {
					return labels.bindStruct(channelEnv, err, envReg, ci.Field(j))
				}
				if _, err := fillEnvNested(ci.Field(j), labels.view); err != nil {
					return err
				}
			case "Config":
				if cfg == nil {
					continue
				}
				if err := fillConfig(ci.Field(j), cfg, waived); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// fillConfig recon-binds one Config struct from cfg (see fillChannels).
func fillConfig(s reflect.Value, cfg *cfgRegs, waived bool) error {
	if !waived {
		if err := checkNullItems(s, cfg); err != nil {
			return err
		}
	}
	if err := bindConfigWaived(cfg.merged, s.Addr().Interface(), waived); err != nil {
		return cfg.labels().bindStruct(channelConfig, err, cfg.merged, s)
	}
	return bindPinnedConfig(s, cfg)
}

// validateChannels enforces the declared constraints on each command's Env and Config fields,
// reusing the argv channel's [checkConstraints]. It is presence-aware: a field is checked only
// when its source provided a value, so absence stays the required marker's concern.
func validateChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, view *osView) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		t := ci.Type()
		for j := range ci.NumField() {
			switch t.Field(j).Name {
			case "Env":
				if envReg == nil {
					continue
				}
				if err := validateChannelStruct(ci.Field(j), envReg, nil, view); err != nil {
					return err
				}
			case "Config":
				if cfg == nil {
					continue
				}
				if err := validateChannelStruct(ci.Field(j), cfg.merged, cfg, view); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateChannelStruct checks every constrained field of an Env or Config sub-struct against
// the value its registry resolved. A pinned field is judged against its own file's registry.
func validateChannelStruct(s reflect.Value, reg *recon.Registry, cfg *cfgRegs, view *osView) error {
	if s.Kind() != reflect.Struct {
		return nil
	}
	st := s.Type()
	for j := range s.NumField() {
		f := st.Field(j)
		c, has := channelConstraints(f.Tag)
		enum := channelEnum(f.Tag)
		decimal := decimalOnly(f)
		if !channelChecked(f.Tag, has, enum, decimal) {
			continue
		}
		key := reconKey(f.Tag.Get("recon"))
		if key == "" {
			continue
		}
		fieldReg := reg
		if pin := f.Tag.Get("cfgfile"); pin != "" && cfg != nil {
			var err error
			if fieldReg, err = cfg.For(pin); err != nil {
				return err
			}
		}
		val, found, err := fieldReg.Get(key)
		if err != nil {
			return reconBind(channelOf(cfg), err)
		}
		if !found {
			continue // only provided values are constraint-checked
		}
		typ := channelFieldType(f.Tag, s.Field(j).Type())
		label := f.Tag.Get("rotini")
		if label == "" {
			label = key
		}
		// The user set a variable or a key, not an input: name what they set, and where it came
		// from when that was not the environment itself.
		origin := ""
		switch v := f.Tag.Get("env"); {
		case cfg != nil:
			label = "config key " + key
			origin = cfg.labels().origin(val.Source())
		case v != "":
			name := chosenEnv(view, v)
			label = "environment variable " + name + view.inputOrigin(name)
		}
		secret := reconHasSecret(f.Tag.Get("recon"))
		vals := channelValues(val, typ)
		if tagExpandRule(f.Tag).declared() {
			vals = expandedStrings(s.Field(j), vals) // checked as expanded and bound
		}
		if len(vals) == 1 && vals[0] == "" && isPathType(typ) {
			typ = channelGoType(s.Field(j).Type()) // an empty path is no path: nothing to check
		}
		if err := checkDecimal(decimal, f, label, vals, secret); err != nil {
			return withOrigin(err, origin)
		}
		if err := checkChannelEnum(channelOf(cfg), label, enum, boundStrings(s.Field(j), vals), secret); err != nil {
			return withOrigin(err, origin)
		}
		if enum.rewrites() {
			canonicalizeField(s.Field(j), enum)
		}
		// A list is checked element by element as bound, since an environment variable's list
		// arrives as one string.
		if elems, count, ok := typedElems(s.Field(j)); ok && isArrayType(typ) {
			if err := checkTypedConstraints(label, typ, c, count, elems, secret, view.base()); err != nil {
				return withOrigin(err, origin)
			}
			continue
		}
		if err := checkConstraints(view.clockRef().features(), label, typ, f.Tag.Get("base"), c, vals, secret, view.base()); err != nil {
			return withOrigin(err, origin)
		}
	}
	return nil
}

// channelChecked reports whether a field of an Env or Config struct has anything to check:
// constraints, an enum, a path kind, or a decimal-only integer.
func channelChecked(tag reflect.StructTag, constrained bool, enum enumSet, decimal bool) bool {
	return constrained || enum.declared() || decimal || tag.Get("path") != ""
}

// decimalOnly reports whether f is an integer field read as decimal only: one that doesn't
// declare base: prefixed.
func decimalOnly(f reflect.StructField) bool {
	return f.Tag.Get("base") != basePrefixed && plainIntegerField(f.Type)
}

// checkDecimal refuses a value of integer field f that isn't decimal, or doesn't fit the field's
// type, when decimal says the field doesn't declare base: prefixed: without it an integer is
// decimal on every channel, as on the command line.
func checkDecimal(decimal bool, f reflect.StructField, label string, vals []string, secret bool) error {
	if !decimal {
		return nil
	}
	typ := derefType(f.Type)
	if typ.Kind() == reflect.Slice {
		typ = derefType(typ.Elem())
	}
	shape := typ.Kind().String()
	if derefType(f.Type).Kind() == reflect.Slice {
		shape = "[]" + shape
	}
	return checkValueShape(shape, "", label, "", listItems(f.Tag, shape, vals), secret)
}

// listItems splits each of vals on the recon separator of a list field, since an environment
// variable or a config string holds the whole list; other fields' vals are returned as they are.
func listItems(tag reflect.StructTag, typ string, vals []string) []string {
	if !isArrayType(typ) {
		return vals
	}
	sep := recon.ParseTag(tag.Get("recon")).Separator
	if sep == "" {
		sep = "," // recon's default
	}
	var out []string
	for _, v := range vals {
		if v == "" {
			continue
		}
		for part := range strings.SplitSeq(v, sep) {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

// withOrigin appends origin, a " (from configuration file …)" suffix, to a value error's message.
func withOrigin(err error, origin string) error {
	if origin == "" {
		return err
	}
	switch e := err.(type) { //nolint:errorlint // the value checks return a bare error
	case *ParseError:
		cp := *e
		cp.Msg += origin
		return &cp
	case *InputError:
		cp := *e
		cp.Msg += origin
		return &cp
	}
	return err
}

// channelOf names the channel an Env/Config pass is working on: config when it was handed the
// config registries, env otherwise.
func channelOf(cfg *cfgRegs) string {
	if cfg != nil {
		return channelConfig
	}
	return channelEnv
}

// channelGoType maps a channel field's Go type to the type string checkConstraints expects.
func channelGoType(t reflect.Type) string {
	// Measured types first: a duration or size is an int64 underneath, but its text ("5m",
	// "1Gi") does not parse as a plain integer.
	switch t {
	case durationType:
		return "time.Duration"
	case reflect.TypeFor[ByteSize]():
		return "rotini.ByteSize"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return channelGoType(t.Elem())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	case reflect.Float32, reflect.Float64:
		return "float64"
	case reflect.Slice:
		// The element type, so per-element bounds apply; a named byte type is one value.
		if t.Elem().Kind() == reflect.Uint8 && t.Name() != "" {
			return "[]string"
		}
		return "[]" + channelGoType(t.Elem())
	default:
		return "string"
	}
}

// channelValues renders a reconciled value as the strings checkConstraints consumes: the
// elements of an array type, else the single canonical string.
func channelValues(val recon.Value, typ string) []string {
	if isArrayType(typ) && val.Kind() == recon.SliceKind {
		items, _ := val.AsSlice()
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = channelString(it)
		}
		return out
	}
	return []string{channelString(val)}
}

// channelString renders a reconciled value as text, writing a decoded duration back in user
// form ("8d", not "192h0m0s").
func channelString(val recon.Value) string {
	if d, ok := val.Any().(time.Duration); ok {
		return formatDuration(d)
	}
	return val.String()
}

// ── InputError ───────────────────────────────────────────────.

// The non-argv input channels an [*InputError] can report on.
const (
	channelEnv    = "env"
	channelConfig = "config"
	channelStdin  = "stdin"
	channelFlag   = "flag"
)

// InputError reports a failure acquiring or decoding one of a command's non-argv input
// channels: environment variables, configuration files, a typed stdin payload, or a flag's or
// argument's env/config fallback. It is the counterpart of the argv channel's [*ParseError].
//
// Error names the channel, the input, and what went wrong. The underlying recon, decode or OS
// Cause is reachable via errors.As but kept out of the message, and the value of an input
// marked secret is redacted.
//
// A bad value, a missing required input, or a malformed document the user supplied is
// [CategoryUsage]; a registry build, schema compile, IO read, or codegen mismatch is
// [CategoryInternal].
//
//	var be *rotini.InputError
//	if errors.As(err, &be) {
//	    fmt.Fprintf(os.Stderr, "bad %s input %q: %s\n", be.Channel, be.Input, be.Error())
//	}
type InputError struct {
	Channel string // one of "env", "config", "stdin", "flag", "argument"
	Input   string // the offending input key/path, when a single one is known (else "")
	Msg     string // the message Error returns; it never includes Cause's text
	Cause   error  // the underlying recon/decode/OS error, reachable via errors.As (may be nil)

	// Token and Candidates are set when a value is not one of an env or config input's enum:
	// the rejected value ("[redacted]" for a secret input) and the allowed values. They are
	// empty for any other failure. [SuggestionFacts] reads them.
	Token      string
	Candidates []string

	usage bool // true → CategoryUsage (ErrUsage); false → CategoryInternal
}

// Error returns Msg.
func (e *InputError) Error() string { return e.Msg }

// Unwrap returns Cause, when set, and the category sentinel, [ErrUsage] or [ErrInternal].
func (e *InputError) Unwrap() []error {
	sentinel := ErrInternal
	if e.usage {
		sentinel = ErrUsage
	}
	if e.Cause == nil {
		return []error{sentinel}
	}
	return []error{e.Cause, sentinel}
}

// usageBind builds a [CategoryUsage] *InputError: bad input the end user can fix.
func usageBind(channel, input, msg string, cause error) *InputError {
	return &InputError{Channel: channel, Input: input, Msg: msg, Cause: cause, usage: true}
}

// internalBind builds a [CategoryInternal] *InputError: a failure the author must fix.
func internalBind(channel, input, msg string, cause error) *InputError {
	return &InputError{Channel: channel, Input: input, Msg: msg, Cause: cause}
}

// reconBind converts a recon error for one channel into a clean, categorized [*InputError]. It
// inspects recon's typed errors to name the offending input and phrase a non-leaky message,
// keeping the whole error as the Cause. Every result is usage-class: a value the user supplied
// could not be used.
func reconBind(channel string, err error) error { return channelLabels{}.bind(channel, err) }

// channelNoun names a single input on a channel, for per-input messages.
func channelNoun(channel string) string {
	switch channel {
	case channelEnv:
		return "environment variable"
	case channelConfig:
		return "config key"
	case channelStdin:
		return "stdin field"
	default:
		return "flag"
	}
}

// channelDesc names the channel as a whole, for generic messages.
func channelDesc(channel string) string {
	switch channel {
	case channelEnv:
		return "environment"
	case channelConfig:
		return "configuration"
	case channelStdin:
		return "stdin"
	default:
		return "flag"
	}
}

// errSchemaUnfinished marks a schema check that stopped before it finished, so the document
// is reported as not fully checked rather than invalid.
var errSchemaUnfinished = errors.New("the schema check did not finish")

// stdinUnfinished reports a stdin document whose schema check stopped before it finished.
const stdinUnfinished = "stdin could not be fully checked against its schema"

// schemaVerdict is what a failed schema check says of a document: "is invalid: <detail>", or
// "could not be fully checked against its schema" when the check stopped before it finished.
func schemaVerdict(err error, invalid string) string {
	if errors.Is(err, errSchemaUnfinished) {
		return "could not be fully checked against its schema"
	}
	return invalid + ": " + schemaDetail(err)
}

// schemaDetail extracts a non-leaky detail from a [recon.ValidationError] — the property and
// rule it names — falling back to a generic phrase for any other error, so the message never
// echoes raw recon text.
func schemaDetail(err error) string {
	if ve, ok := errors.AsType[*recon.ValidationError](err); ok {
		if ve.Path.String() != "" {
			return fmt.Sprintf("%s: %s", ve.Path.String(), ve.Msg)
		}
		return ve.Msg
	}
	return "does not match its schema"
}

// cleanType renders a recon target Go type for an end-user message, trimming a nullable
// scalar's pointer star.
func cleanType(t string) string { return strings.TrimPrefix(t, "*") }
