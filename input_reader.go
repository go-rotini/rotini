package rotini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-rotini/recon"
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
// reverse. Flags may additionally fall back to env and config.
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
// [InputSettings].
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
// declares config inputs but the program has no [InputSettings].
func (b *InputReader) Read(rtx *Context, out any) error { return b.bind(rtx, out) }

func (b *InputReader) bind(rtx *Context, out any) error {
	if b == nil {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: nil input reader"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: Read out argument must be a non-nil pointer to an inputs struct"}
	}

	// 1. argv → Flags + Arguments, without validation: step 3 checks the reconciled store.
	store, chain, err := b.parser.parseBind(rtx, out)
	if err != nil {
		return err
	}
	v := rv.Elem()

	// A short-circuit flag set on argv waives every declared requirement below. Each channel
	// is still read, so an env value or a command line that cannot be read is still an error;
	// a configuration file that cannot be read or holds a value of the wrong type is skipped.
	waived := shortCircuited(chain, store)
	view := rtx.osView()

	// 1b. Two-phase bootstrap: a config_source flag or env names the file step 4 reads.
	overrides := b.pathOverrides(chain, store, view)

	// 2. Flag fallback: argv-set > env > config, recorded back into the store so step 3
	//    validates it too. A flag with no recon key keeps the Parser's value.
	anchor := frameAnchor(v, chain, rtx.frameIndex())
	if err := b.reconcileFlags(v, chain, store, overrides, anchor, waived, view); err != nil {
		return err
	}

	// 3. Validate the reconciled flags and arguments, once. Argv errors surface before any
	//    channel error. Only the commands out describes are judged, so a parent collecting its
	//    own inputs is not held to the leaf's requirements.
	if anchor >= 0 {
		store.span = &[2]int{anchor, anchor + v.NumField()}
	}
	if err := validateStore(chain, store); err != nil {
		return err
	}

	// Checked after argv validation so a bad command line reports itself first.
	if err := checkFrameFit(v, chain, rtx.frameIndex()); err != nil {
		return err
	}

	// 4. env + config → the Env/Config sub-structs, from independent registries. A registry
	//    is built only when out describes inputs of that channel.
	var envReg *recon.Registry
	if hasChannel(v, "Env") {
		if envReg, err = recon.New(recon.WithSources(envSources(v, b.envPrefix, view)...)); err != nil {
			return internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer envReg.Close()
	}

	if err := b.checkDescribed(v); err != nil {
		return err
	}

	var cfg *cfgRegs
	if hasConfigChannel(v) {
		if cfg, err = b.configRegs(chain, overrides, v, waived, view); err != nil {
			return err
		}
		defer cfg.Close()
	}

	if err := fillChannels(v, envReg, cfg, waived, view); err != nil {
		return err
	}

	// 4b. The same constraint checks argv gets, over the values actually provided.
	if !waived {
		if err := validateChannels(v, envReg, cfg, view); err != nil {
			return err
		}
	}

	// 5. stdin → the leaf command's typed payload, its only consumer.
	return b.fillStdin(rtx, v, waived)
}

// validateStore runs the argv channel's declarative checks over a reconciled store — required,
// enum and constraints, then flag groups, then flag dependencies — returning the first failure.
// A short-circuited run ([shortCircuited]) skips every requirement and keeps only what reading
// the command line needs.
func validateStore(chain []Command, store *parsedInputs) error {
	if err := validate(chain, store); err != nil {
		return err
	}
	if shortCircuited(chain, store) {
		return nil
	}
	if err := validateFlagGroups(chain, store); err != nil {
		return err
	}
	return validateFlagDependencies(chain, store)
}

// fillStdin decodes piped stdin into the leaf command's Stdin payload field, per its
// `stdin:"<format>[,required]"` tag, validating a decoded document against the command's stdin
// schema before binding. With nothing piped the field stays nil, or a required payload is a
// usage error. A short-circuited run (waived) still decodes what was piped but skips the
// required and schema checks.
func (b *InputReader) fillStdin(rtx *Context, v reflect.Value, waived bool) error {
	if v.Kind() != reflect.Struct || v.NumField() == 0 {
		return nil
	}
	leaf := v.Field(v.NumField() - 1) // the running command's inputs
	if leaf.Kind() != reflect.Struct {
		return nil
	}
	sf := leaf.FieldByName("Stdin")
	field, ok := leaf.Type().FieldByName("Stdin")
	if !sf.IsValid() || sf.Kind() != reflect.Pointer || !ok {
		return nil
	}
	format := field.Tag.Get("stdin")
	if format == "" {
		return nil
	}
	format, required := parseStdinTag(format)

	data, err := readStdin(rtx.Stdin)
	if err != nil {
		return internalBind(channelStdin, "", "could not read stdin", err)
	}
	if len(data) == 0 {
		if required && !waived {
			noun := "document"
			if isRawStdinFormat(format) {
				noun = "payload"
			}
			return usageBind(channelStdin, "", fmt.Sprintf("required stdin payload is empty; pipe a %s %s", format, noun), nil)
		}
		return nil // nothing piped → leave Stdin nil
	}

	// A raw format binds the payload itself rather than decoding a document.
	if isRawStdinFormat(format) {
		return bindRawStdin(sf, format, data)
	}

	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return internalBind(channelStdin, "", fmt.Sprintf("unsupported stdin format %q", format), nil)
	}
	m, err := codec.Decode(data)
	if err != nil {
		return usageBind(channelStdin, "", fmt.Sprintf("could not decode stdin as %s", format), err)
	}

	if js := b.stdinSchemas[sf.Type().Elem().Name()]; js != "" && !waived {
		validator, err := schemaValidator(js)
		if err != nil {
			return internalBind(channelStdin, "", "invalid stdin schema", err)
		}
		if err := validator.Validate(m); err != nil {
			applyPatternMessages(js, err)
			return reconBind(channelStdin, err)
		}
	}

	reg, err := recon.New(recon.WithSource(recon.NewMapSource("stdin", m)))
	if err != nil {
		return internalBind(channelStdin, "", "could not build the stdin registry", err)
	}
	defer reg.Close()

	ptr := reflect.New(sf.Type().Elem()) // *<Prefix>Stdin
	if err := bindReconWaived(reg, ptr.Interface(), waived); err != nil {
		return reconBind(channelStdin, err)
	}
	sf.Set(ptr)
	return nil
}

// isRawStdinFormat reports whether format binds stdin directly instead of decoding it.
func isRawStdinFormat(format string) bool { return format == "text" || format == "lines" }

// trimAcquiredPayload is the single trimming rule for bytes rotini reads on the user's behalf,
// shared by the stdin channel and the argv value sentinels (`--flag @file`, `--flag -`) so the
// same bytes yield the same value on either path. It removes one trailing line ending (an
// artifact of delivery, such as an editor's final newline) and nothing else: leading and
// interior whitespace is content.
func trimAcquiredPayload(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// bindRawStdin sets a raw stdin field from the piped bytes: the whole payload as one string
// for "text", or its newline-separated lines for "lines". Only the trailing line ending is
// trimmed (see trimAcquiredPayload), so a final newline adds no empty element.
func bindRawStdin(sf reflect.Value, format string, data []byte) error {
	payload := trimAcquiredPayload(string(data))

	switch format {
	case "text":
		if sf.Type().Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format text needs a string payload type", nil)
		}
		p := reflect.New(sf.Type().Elem())
		p.Elem().SetString(payload)
		sf.Set(p)
		return nil
	case "lines":
		elem := sf.Type().Elem()
		if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format lines needs a []string payload type", nil)
		}
		lines := strings.Split(payload, "\n")
		for i := range lines {
			lines[i] = strings.TrimSuffix(lines[i], "\r") // CRLF input stays usable
		}
		p := reflect.New(elem)
		p.Elem().Set(reflect.ValueOf(lines).Convert(elem))
		sf.Set(p)
		return nil
	}
	return internalBind(channelStdin, "", fmt.Sprintf("unsupported raw stdin format %q", format), nil)
}

// parseStdinTag splits a `stdin:"<format>[,required]"` tag into its format and required marker.
func parseStdinTag(tag string) (format string, required bool) {
	format, opt, _ := strings.Cut(tag, ",")
	return format, opt == "required"
}

// readStdin returns the bytes available on r. When r is the real os.Stdin attached to a
// terminal it returns nil rather than blocking: the stdin channel is for piped input, not
// interactive typing. Any other reader is read to EOF, and a nil reader yields no data.
func readStdin(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if f, ok := r.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return nil, nil // an interactive terminal, not a pipe/redirect
		}
	}
	return io.ReadAll(r)
}

// reconcileFlags overrides each fallback flag with its reconciled value: argv-set > env >
// config files. A flag present in no source keeps what the Parser bound, and argv-only flags
// are untouched. Each reconciled value is written back into store so the deferred validate
// pass sees it as present.
func (b *InputReader) reconcileFlags(v reflect.Value, chain []Command, store *parsedInputs, overrides map[string]string, anchor int, waived bool, view *osView) error {
	if v.Kind() != reflect.Struct || !hasReconFlags(v) {
		return nil // no fallback flags → nothing to reconcile (env included)
	}
	files, err := b.fileSources(b.chainConfigFiles(chain), overrides, waived, view)
	if err != nil {
		return err
	}
	srcs := make([]recon.Source, 0, 2+len(files))
	srcs = append(srcs, recon.NewMapSource("flags", flagOverrides(v, chain, store, anchor)), flagEnvSource(v, b.envPrefix, view))
	srcs = append(srcs, files...)
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return internalBind(channelFlag, "", "could not build the flag-fallback registry", err)
	}
	defer reg.Close()

	if anchor < 0 {
		return nil // a struct that does not fit the chain; checkFrameFit reports it
	}
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
		if !flags.IsValid() {
			continue
		}
		ft := flags.Type()
		for j := range flags.NumField() {
			if err := reconcileFlag(reg, flags.Field(j), ft.Field(j), chain, store, anchor+i, fallbackRead{view: view, waiveFiles: waived}); err != nil {
				return err
			}
		}
	}
	return nil
}

// firstSetEnv returns the first of an env tag's comma-separated variable names that is set
// (non-empty) in the run's environment, or "" when none is. `variable: [GH_TOKEN, GITHUB_TOKEN]`
// generates env:"GH_TOKEN,GITHUB_TOKEN": the first name is preferred, the rest are the
// spellings other tools use for the same thing.
func firstSetEnv(view *osView, names string) string {
	for name := range strings.SplitSeq(names, ",") {
		if name != "" && view.getenv(name) != "" {
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
// error reports, and whether a short-circuited run skips a configuration file's value that the
// flag's type cannot hold.
type fallbackRead struct {
	view       *osView
	waiveFiles bool
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
	source := val.Source()
	switch {
	case found:
		if vals, err = fallbackValues(val, def.Separator); err != nil {
			if rd.waiveFiles && source != osEnvSourceName {
				return nil, "", nil
			}
			return nil, "", fallbackCoerceError(chain, idx, name, fallbackOrigin(rd.view, source, tag.Get("env")), err)
		}
	case field.Kind() == reflect.Map || (isObjectFlag(def) && field.Kind() != reflect.Slice):
		// recon keeps a config map — or an object flag's config block — as its leaves
		// (labels.k, labels.x), not at the map's key. For an object each leaf is one
		// key=value occurrence, and the occurrences merge.
		if vals, source, err = mapLeaves(reg, key); err != nil {
			return nil, "", reconBind(channelFlag, err)
		}
		if isObjectFlag(def) {
			for i, leaf := range vals {
				k, v, _ := strings.Cut(leaf, "=")
				vals[i] = quotePair(k, v) // an object occurrence is CSV-split; keep a comma in a value
			}
		}
		if len(vals) == 0 {
			return nil, "", nil
		}
	default:
		return nil, "", nil
	}
	origin = fallbackOrigin(rd.view, source, tag.Get("env"))
	if def.IgnoreCase {
		vals = canonicalEnum(def.Enum, vals)
	}
	waivable := rd.waiveFiles && source != osEnvSourceName
	var prev reflect.Value
	if waivable {
		prev = reflect.New(field.Type()).Elem()
		prev.Set(field)
	}
	// A fallback value the flag's type cannot hold is a user error, as a bad argv value is.
	if err := coerceFlagValues(field, def, vals); err != nil {
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
			entries = append(entries, rest+"="+v.String())
			source = v.Source()
		}
	}
	sort.Strings(entries)
	return entries, source, nil
}

// fallbackValues renders a flag's reconciled fallback as the argv-shaped strings the Parser
// would have produced: a list's items one by one, a map's entries as key=value (nested maps as
// dotted keys), and anything else as one string, split on the flag's separator when it has
// one.
func fallbackValues(val recon.Value, sep string) ([]string, error) {
	if items, err := val.AsSlice(); err == nil {
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.String()
			// A list of objects (a config file's list of mounts) goes through as JSON, one
			// occurrence per element — the form an object flag decodes.
			if _, err := it.AsMap(); err == nil {
				if raw, err := json.Marshal(plainValue(it)); err == nil {
					out[i] = string(raw)
				}
			}
		}
		return out, nil
	}
	if m, err := val.AsMap(); err == nil {
		var out []string
		flattenMap("", m, &out)
		sort.Strings(out)
		return out, nil
	}
	// Value.String, not AsString: AsString returns "" for a numeric or bool value.
	return splitValue(val.String(), sep)
}

// plainValue converts a recon value to plain JSON-shaped Go data — maps and slices of plain
// values all the way down — since a recon map holds recon values, which encode as nothing.
func plainValue(v recon.Value) any {
	if m, err := v.AsMap(); err == nil {
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = plainValue(e)
		}
		return out
	}
	if items, err := v.AsSlice(); err == nil {
		out := make([]any, len(items))
		for i, e := range items {
			out[i] = plainValue(e)
		}
		return out
	}
	return v.Any()
}

// flattenMap appends a reconciled map's leaves as dotted key=value pairs.
func flattenMap(prefix string, m map[string]recon.Value, out *[]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, err := v.AsMap(); err == nil {
			flattenMap(key, sub, out)
			continue
		}
		*out = append(*out, key+"="+v.String())
	}
}

// coerceFlagValues binds fallback values into a flag's field the way [bindFlags] binds argv
// values, dotted-key maps included.
func coerceFlagValues(f reflect.Value, def FlagDef, vals []string) error {
	if isObjectFlag(def) {
		return bindObjectFlag(f, vals, def)
	}
	if def.DottedKeys {
		return coerceMapDotted(f, vals)
	}
	return coerceWithLayout(f, vals, def.Layout)
}

// fallbackOrigin names where a flag's fallback value came from in the user's terms: the
// environment variable they set, or the configuration file by its declared name. recon's own
// source names ("osenv") mean nothing to them.
func fallbackOrigin(view *osView, source, envVar string) string {
	switch source {
	case "":
		return ""
	case osEnvSourceName:
		if envVar == "" {
			return "the environment"
		}
		return "environment variable " + chosenEnv(view, envVar)
	}
	return fmt.Sprintf("configuration file %q", source)
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

// hasReconFlags reports whether any command has an env/config fallback flag to reconcile.
// When false, the input reader skips building a registry entirely.
func hasReconFlags(v reflect.Value) bool {
	for _, field := range v.Fields() {
		flags := commandFlags(field)
		if !flags.IsValid() {
			continue
		}
		ft := flags.Type()
		for j := range flags.NumField() {
			if reconKey(ft.Field(j).Tag.Get("recon")) != "" {
				return true
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
		ft := flags.Type()
		for j := range flags.NumField() {
			key := reconKey(ft.Field(j).Tag.Get("recon"))
			if key == "" {
				continue
			}
			if store.argvSetAt(offset + i)[ft.Field(j).Tag.Get("rotini")] {
				setNested(m, key, flags.Field(j).Interface())
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
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
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
func hasConfigChannel(v reflect.Value) bool {
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
// precedence) to last as declared. overrides carries any config_source-supplied paths.
func (b *InputReader) configRegistry(files []ConfigFile, overrides map[string]string, keys valueKeys, waived bool, view *osView) (*recon.Registry, error) {
	srcs, err := b.fileSources(files, overrides, waived, view)
	if err != nil {
		return nil, err
	}
	for i, src := range srcs {
		srcs[i] = spellings{Source: src, keys: keys}
	}
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return nil, internalBind(channelConfig, "", "could not build the configuration registry", err)
	}
	return reg, nil
}

// cfgRegs is the config channel's registries for one bind: the merged precedence chain plus
// lazily-built single-file registries for inputs the spec pins to one file.
type cfgRegs struct {
	reader    *InputReader
	files     []ConfigFile // sources in scope for the invoked chain, nearest-wins order
	overrides map[string]string
	keys      valueKeys // how config fields' text is read; see spellings
	merged    *recon.Registry
	perFile   map[string]*recon.Registry
	waived    bool // a short-circuited run: a file that cannot be read, or a bad value, is skipped
	view      *osView
}

// configRegs builds the merged config registry and the lazy per-file cache over the sources in
// scope for chain. waived marks a short-circuited run ([shortCircuited]): a file that cannot be
// read or parsed is skipped, and none is checked against its schema.
func (b *InputReader) configRegs(chain []Command, overrides map[string]string, v reflect.Value, waived bool, view *osView) (*cfgRegs, error) {
	files := b.chainConfigFiles(chain)
	keys := channelValueKeys(v, "Config")
	merged, err := b.configRegistry(files, overrides, keys, waived, view)
	if err != nil {
		return nil, err
	}
	return &cfgRegs{reader: b, files: files, overrides: overrides, keys: keys, merged: merged, perFile: map[string]*recon.Registry{}, waived: waived, view: view}, nil
}

// chainConfigFiles returns the config_files in scope for the resolved chain, ordered
// nearest-wins: the invoked command's sources first, then each ancestor up to the root, each
// command's own declared order preserved. Off-branch sources are excluded; an unscoped source
// is always in scope and appended last.
func (b *InputReader) chainConfigFiles(chain []Command) []ConfigFile {
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
		src, err := c.reader.readFileSource(f, c.overrides, c.waived, c.view)
		if err != nil {
			return nil, err
		}
		reg, err := recon.New(recon.WithSource(spellings{Source: src, keys: c.keys}))
		if err != nil {
			return nil, internalBind(channelConfig, name, fmt.Sprintf("could not build the registry for configuration file %q", name), err)
		}
		c.perFile[name] = reg
		return reg, nil
	}
	return nil, internalBind(channelConfig, name, fmt.Sprintf("input pinned to unknown configuration file %q", name), nil)
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
	if err := reg.Bind(target); err != nil && !waived {
		return err
	}
	return nil
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
			return reconBind(channelConfig, err)
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
func (b *InputReader) fileSources(files []ConfigFile, overrides map[string]string, waived bool, view *osView) ([]recon.Source, error) {
	srcs := make([]recon.Source, 0, len(files)+len(b.sources))
	for _, f := range files {
		src, err := b.readFileSource(f, overrides, waived, view)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, src)
	}
	return append(srcs, b.sources...), nil
}

// namedSource renames a recon source to its config_files logical name, since recon source
// names must be unique and two entries may share a basename. The wrapper drops live-watch
// support, which the input reader does not use.
type namedSource struct {
	recon.Source

	name string
}

func (s namedSource) Name() string { return s.name }

// fileSource builds the recon source for one config_files entry, schema-checked, reading the
// process environment and working directory.
func (b *InputReader) fileSource(f ConfigFile, overrides map[string]string) (recon.Source, error) {
	return b.readFileSource(f, overrides, false, nil)
}

// readFileSource is [InputReader.fileSource] for a run: paths are expanded and made absolute
// with the run's view. In a short-circuited run (waived) a file that cannot be located, read or
// parsed is skipped, standing in as an empty source under its own name so precedence holds,
// and no file is checked against its schema.
func (b *InputReader) readFileSource(f ConfigFile, overrides map[string]string, waived bool, view *osView) (recon.Source, error) {
	src, err := b.openFileSource(f, overrides, view)
	if err != nil {
		if waived {
			return namedSource{Source: recon.NewMapSource(f.Name, nil), name: f.Name}, nil
		}
		return nil, err
	}
	if !waived {
		if err := validateConfigFile(f, src); err != nil {
			return nil, err
		}
	}
	return namedSource{Source: src, name: f.Name}, nil
}

// openFileSource locates and parses one config_files entry. rotini expands the path itself,
// from the run's view, so recon reads exactly the absolute path it is given.
func (b *InputReader) openFileSource(f ConfigFile, overrides map[string]string, view *osView) (recon.Source, error) {
	opts := []recon.FileOption{recon.WithPathExpansion(false)}
	if f.Format != "" {
		opts = append(opts, recon.WithFileFormat(f.Format))
	}
	path, overridden := overrides[f.Name]
	switch {
	case overridden:
		opts = append(opts, recon.WithOptional(false))
	case f.Discover != nil:
		dirs, err := discoverDirs(f.Discover, view)
		if err != nil {
			return nil, internalBind(channelConfig, f.Name, fmt.Sprintf("could not resolve the search path for configuration file %q", f.Name), err)
		}
		path = f.Discover.File
		opts = append(opts, recon.WithOptional(true), recon.WithSearchPaths(dirs...))
	default:
		path = f.Path
		opts = append(opts, recon.WithOptional(true))
	}
	resolved, err := view.expandPath(path)
	if err == nil {
		resolved, err = absPath(view, resolved)
	}
	if err == nil {
		var src recon.Source
		if src, err = recon.NewFileSource(resolved, opts...); err == nil {
			return src, nil
		}
	}
	// The file's content or path is the user's to fix.
	return nil, usageBind(channelConfig, f.Name, fmt.Sprintf("could not open configuration file %q (%s)", f.Name, path), err)
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
func validateConfigFile(f ConfigFile, src recon.Source) error {
	if f.Schema == "" {
		return nil
	}
	fs, ok := src.(*recon.FileSource)
	if !ok {
		return nil
	}
	path := fs.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // absent: vacuous
		}
		// A present-but-unreadable file the user controls.
		return usageBind(channelConfig, f.Name, fmt.Sprintf("could not read configuration file %q (%s)", f.Name, path), err)
	}
	codec, ok := recon.DefaultCodecs().ByName(fs.Format())
	if !ok {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("unsupported format %q for configuration file %q", fs.Format(), f.Name), nil)
	}
	m, err := codec.Decode(data)
	if err != nil {
		return usageBind(channelConfig, f.Name, fmt.Sprintf("configuration file %q (%s) is not valid %s", f.Name, path, fs.Format()), err)
	}
	validator, err := schemaValidator(f.Schema)
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		applyPatternMessages(f.Schema, err)
		return usageBind(channelConfig, f.Name,
			fmt.Sprintf("configuration file %q (%s) is invalid: %s", f.Name, path, schemaDetail(err)), err)
	}
	return nil
}

// pathOverrides resolves each ConfigFile's config_source inputs to the path they supply: the
// flag explicitly set on argv, then the env variable, then the flag's default. An entry none
// of them supplies keeps its own path or discover strategy.
func (b *InputReader) pathOverrides(chain []Command, store *parsedInputs, view *osView) map[string]string {
	out := map[string]string{}
	for _, f := range b.chainConfigFiles(chain) {
		pf := f.PathFrom
		if pf == nil {
			continue
		}
		explicit, defaulted := storeFlagValue(store, pf.Flag)
		switch {
		case explicit != "":
			out[f.Name] = explicit
		case firstSetEnv(view, pf.Env) != "":
			out[f.Name] = view.getenv(firstSetEnv(view, pf.Env))
		case defaulted != "":
			out[f.Name] = defaulted
		}
	}
	return out
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

// discoverDirs resolves a Discover strategy to the ordered directories searched: "walk-up" is
// the run's working directory up to the filesystem root, "xdg" is $XDG_CONFIG_HOME/<app>,
// defaulting to ~/.config/<app>, from the run's environment.
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
	default:
		return nil, fmt.Errorf("unknown discover strategy %q", d.Strategy)
	}
}

// envSources builds the env channel's recon source: each Env input's declared variable, read
// through the run's view, by its explicit name or the SNAKE_UPPER projection of its key under
// the optional env prefix. Nested env families are not registry data — recon resolves leaf
// keys only, so fillEnvNested sets those fields directly.
func envSources(v reflect.Value, envPrefix string, view *osView) []recon.Source {
	return []recon.Source{spellings{Source: newDeclaredEnv(v, "Env", envPrefix, view), keys: channelValueKeys(v, "Env")}}
}

// spellings gives env and config inputs the value spellings flags accept and recon's decode
// does not: days and weeks in a duration (7d), yes/no, on/off and y/n for a bool (see
// parseBool), and a declared layout for a time field instead of RFC 3339. Only the keys of
// those typed fields are rewritten; a string input whose value is "yes" keeps it.
type spellings struct {
	recon.Source

	keys valueKeys
}

// valueKeys maps the recon keys of an Env or Config struct's bool fields, its duration fields,
// and its time fields that declare a layout, to how their text is read.
type valueKeys struct {
	bools     map[string]bool
	layouts   map[string]string
	durations map[string]bool
}

func (s spellings) Get(path recon.Path) (recon.Value, bool, error) {
	v, found, err := s.Source.Get(path)
	if !found || err != nil || v.Kind() != recon.StringKind {
		return v, found, err
	}
	key := path.String()
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
	if layout := s.keys.layouts[key]; layout != "" {
		// An unparseable value passes through as text so recon reports it as a usage error
		// naming the input; an error returned from Get would be swallowed.
		if t, perr := parseTimeLayout(v.String(), layout); perr == nil {
			return recon.NewValue(t), true, nil
		}
	}
	return v, found, err
}

// channelValueKeys collects [valueKeys] from each command's Env or Config struct.
func channelValueKeys(v reflect.Value, structName string) valueKeys {
	keys := valueKeys{bools: map[string]bool{}, layouts: map[string]string{}, durations: map[string]bool{}}
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
			switch ft := derefType(sf.Type); {
			case ft.Kind() == reflect.Bool:
				keys.bools[key] = true
			case ft == durationType:
				keys.durations[key] = true
			case ft == timeType && sf.Tag.Get("layout") != "":
				keys.layouts[key] = sf.Tag.Get("layout")
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
		fam := envFamily(view, base, sep)
		if len(fam) == 0 {
			if opt == "required" {
				name := et.Field(j).Tag.Get("rotini")
				return filled, usageBind(channelEnv, name,
					fmt.Sprintf("environment input %q is required; set %s%s* variables", name, base, sep), nil)
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

// envFamily collects the BASE<sep>… variables of the run's environment into a nested map.
func envFamily(view *osView, base, sep string) map[string]any {
	out := map[string]any{}
	if sep == "" {
		return out
	}
	prefix := base + sep
	for _, kv := range view.environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			continue
		}
		segs := strings.Split(strings.ToLower(name[len(prefix):]), strings.ToLower(sep))
		setNested(out, strings.Join(segs, "."), val)
	}
	return out
}

// fillChannels walks a <Cmd>Inputs struct and recon-binds each command's Env struct from
// envReg and its Config struct from cfg. A short-circuited run (waived) binds what resolves and
// ignores missing required values, and skips a configuration value of the wrong type. A nil
// registry belongs to a channel the struct describes no inputs of.
func fillChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs, waived bool, view *osView) error {
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
				if err := bindReconWaived(envReg, ci.Field(j).Addr().Interface(), waived); err != nil {
					return reconBind(channelEnv, err)
				}
				if _, err := fillEnvNested(ci.Field(j), view); err != nil {
					return err
				}
			case "Config":
				if cfg == nil {
					continue
				}
				if err := bindConfigWaived(cfg.merged, ci.Field(j).Addr().Interface(), waived); err != nil {
					return reconBind(channelConfig, err)
				}
				if err := bindPinnedConfig(ci.Field(j), cfg); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
		enum, ignoreCase := channelEnum(f.Tag)
		if !has && len(enum) == 0 {
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
		typ := channelGoType(s.Field(j).Type())
		label := f.Tag.Get("rotini")
		if label == "" {
			label = key
		}
		// The user set a variable, not an input: name what they typed.
		if v := f.Tag.Get("env"); v != "" && cfg == nil {
			label = chosenEnv(view, v)
		}
		secret := reconHasSecret(f.Tag.Get("recon"))
		vals := channelValues(val, typ)
		if err := checkChannelEnum(channelOf(cfg), label, enum, ignoreCase, boundStrings(s.Field(j), vals), secret); err != nil {
			return err
		}
		if ignoreCase {
			canonicalizeField(s.Field(j), enum)
		}
		if err := checkConstraints(label, typ, c, vals, secret, view.base()); err != nil {
			return err
		}
	}
	return nil
}

// channelOf names the channel an Env/Config pass is working on: config when it was handed the
// config registries, env otherwise.
func channelOf(cfg *cfgRegs) string {
	if cfg != nil {
		return channelConfig
	}
	return channelEnv
}

// channelEnum reads an env or config field's enum:"<json array>" and ignorecase:"true" tags.
// Codegen writes the members as JSON so a member may contain any character.
func channelEnum(tag reflect.StructTag) (enum []string, ignoreCase bool) {
	if v := tag.Get("enum"); v != "" {
		_ = json.Unmarshal([]byte(v), &enum) // codegen-written; a malformed tag means no enum
	}
	return enum, tag.Get("ignorecase") == "true"
}

// checkChannelEnum is the env and config counterpart of the argv enum check in [validate], so an
// enum advertised in help is enforced whichever channel supplied the value.
func checkChannelEnum(channel, label string, enum []string, ignoreCase bool, vals []string, secret bool) error {
	if len(enum) == 0 {
		return nil
	}
	for _, v := range vals {
		if !enumHas(enum, v, ignoreCase) {
			e := usageBind(channel, label, fmt.Sprintf("invalid value %q for %s (one of: %s)",
				redactValue(v, secret), label, strings.Join(enum, ", ")), nil)
			e.Token, e.Candidates = redactValue(v, secret), enum
			return e
		}
	}
	return nil
}

// boundStrings is the values an enum is checked against: a bound []string field's elements,
// since an env list arrives as one "a,b" string that only binding splits; else the channel's
// own rendering.
func boundStrings(f reflect.Value, fallback []string) []string {
	if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
		out := make([]string, f.Len())
		for i := range f.Len() {
			out[i] = f.Index(i).String()
		}
		return out
	}
	return fallback
}

// canonicalizeField rewrites a bound string, or each element of a bound []string, to the
// declared spelling of the enum member it matched case-insensitively.
func canonicalizeField(f reflect.Value, enum []string) {
	switch {
	case !f.CanSet():
	case f.Kind() == reflect.String:
		f.SetString(canonicalEnum(enum, []string{f.String()})[0])
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
		for i := range f.Len() {
			e := f.Index(i)
			e.SetString(canonicalEnum(enum, []string{e.String()})[0])
		}
	}
}

// channelConstraints reads the validation struct-tags codegen emits on a channel field into a
// [Constraints]. A numeric bound is set whenever its tag is present, so min:"0" enforces >= 0.
func channelConstraints(tag reflect.StructTag) (Constraints, bool) {
	var c Constraints
	has := false
	floatTag := func(name string, dst **float64) {
		if f, err := strconv.ParseFloat(tag.Get(name), 64); err == nil {
			*dst, has = new(f), true
		}
	}
	intTag := func(name string, dst *int) {
		if n, err := strconv.Atoi(tag.Get(name)); err == nil {
			*dst, has = n, true
		}
	}
	floatTag("min", &c.Minimum)
	floatTag("max", &c.Maximum)
	floatTag("xmin", &c.ExclusiveMinimum)
	floatTag("xmax", &c.ExclusiveMaximum)
	floatTag("multipleof", &c.MultipleOf)
	intTag("minlen", &c.MinLength)
	intTag("maxlen", &c.MaxLength)
	intTag("minitems", &c.MinItems)
	intTag("maxitems", &c.MaxItems)
	if v := tag.Get("pattern"); v != "" {
		c.Pattern, has = v, true
	}
	c.PatternMessage = tag.Get("patternmsg")
	return c, has
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
		return "[]string"
	default:
		return "string"
	}
}

// channelValues renders a reconciled value as the strings checkConstraints consumes: the
// elements of an array type, else the single canonical string.
func channelValues(val recon.Value, typ string) []string {
	if isArrayType(typ) {
		if items, err := val.AsSlice(); err == nil {
			out := make([]string, len(items))
			for i, it := range items {
				out[i] = channelString(it)
			}
			return out
		}
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
// channels: environment variables, configuration files, a typed stdin payload, or a flag's
// env/config fallback. It is the counterpart of the argv channel's [*ParseError].
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
	Channel string // one of "env", "config", "stdin", "flag"
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

// Error returns Msg: the channel, the input, and what went wrong. A secret input's value is
// redacted.
func (e *InputError) Error() string { return e.Msg }

// Unwrap exposes the Cause and the category sentinel, so errors.Is/As reach both the original
// recon error and [ErrUsage]/[ErrInternal].
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
func reconBind(channel string, err error) error {
	if err == nil {
		return nil
	}
	// An empty path means the failure concerns the payload as a whole; name the channel
	// rather than a field called "".
	label := func(path string) string {
		if path == "" {
			return channelDesc(channel)
		}
		return fmt.Sprintf("%s %q", channelNoun(channel), path)
	}
	var ce *recon.CoercionError
	var mre *recon.MissingRequiredError
	var ve *recon.ValidationError
	var eve *recon.EmptyValueError
	switch {
	case errors.As(err, &ce):
		return usageBind(channel, ce.Path.String(),
			fmt.Sprintf("%s: expected %s", label(ce.Path.String()), cleanType(ce.Target)), err)
	case errors.As(err, &mre):
		return usageBind(channel, mre.Path.String(),
			fmt.Sprintf("%s is required", label(mre.Path.String())), err)
	case errors.As(err, &ve):
		return usageBind(channel, ve.Path.String(),
			fmt.Sprintf("%s: %s", label(ve.Path.String()), ve.Msg), err)
	case errors.As(err, &eve):
		return usageBind(channel, eve.Path.String(),
			fmt.Sprintf("%s must not be empty", label(eve.Path.String())), err)
	default:
		return usageBind(channel, "", fmt.Sprintf("could not read %s input", channelDesc(channel)), err)
	}
}

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
