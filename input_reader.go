package rotini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
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
	view := rtx.osView()

	// 1b. Two-phase bootstrap: a config_source flag or env names the file step 4 reads, and a
	// profile selector names the profile it reads. The .env files in scope join the environment
	// inputs read (under the run's own), and variable_file variables are read, before any other
	// config_source path is looked up or any profile selected.
	boot := fileBootstrap{paths: b.pathOverrides(chain, store, view)}
	if view, err = b.inputView(chain, v, boot.paths, waived, view); err != nil {
		return err
	}
	if view.hasInputEnv() {
		boot.paths = b.pathOverrides(chain, store, view)
	}
	boot.profiles = b.profileChoices(chain, store, view)

	// 1c. Declared path expansion of the argv values and defaults, then binding them into
	//     Flags and Arguments.
	anchor := frameAnchor(v, chain, rtx.frameIndex())
	if err := expandStore(v, chain, store, anchor, view); err != nil {
		return err
	}
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return err
	}

	// 2. Flag fallback: argv-set > env > config, recorded back into the store so step 3
	//    validates it too. A flag with no recon key keeps the Parser's value.
	if err := b.reconcileFlags(v, chain, store, boot, anchor, waived, view); err != nil {
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
	if err := b.checkProfiles(v, chain, boot, waived, view); err != nil {
		return err
	}

	// 4. env + config → the Env/Config sub-structs, from independent registries. A registry
	//    is built only when out describes inputs of that channel.
	var envReg *recon.Registry
	if hasEnvChannel(v) {
		if envReg, err = recon.New(recon.WithoutWatch(), recon.WithSources(envSources(v, b.envPrefix, view)...)); err != nil {
			return internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer envReg.Close()
	}

	if err := b.checkDescribed(v); err != nil {
		return err
	}

	var cfg *cfgRegs
	if hasConfigChannel(v) {
		if cfg, err = b.configRegs(chain, boot, v, waived, view); err != nil {
			return err
		}
		defer cfg.Close()
	}

	labels := channelLabels{env: envNames(v, "Env", b.envPrefix), view: view}
	if err := fillChannels(v, envReg, cfg, waived, labels); err != nil {
		return err
	}
	if err := expandChannels(v, envReg, cfg, waived, labels); err != nil {
		return err
	}

	// 4b. The same constraint checks argv gets, over the values actually provided.
	if !waived {
		if err := validateChannels(v, envReg, cfg, view); err != nil {
			return err
		}
	}

	// 5. stdin → the leaf command's typed payload, its only consumer.
	return b.fillStdin(rtx, v, waived, func(name string) ([]string, error) { return leafArgValues(chain, store, name), nil })
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

// fillStdin binds piped stdin to the leaf command's Stdin field, per its
// `stdin:"<format>[,stream][,nul][,required][,unless=<argument>]"` tag (see stdinTag),
// validating a decoded document against the command's stdin schema before binding. With
// nothing piped the field stays nil, or a required payload is a usage error.
//
// Stdin is not read, and the field stays nil, when a short-circuit flag is set (waived), or
// when the tag's unless argument has a value other than "-". argValues returns the leaf's
// values of a named argument as the command line gave them; it is called only for such a tag.
//
// Stdin is read once per run (see stdinState), so a later call binds the same payload. A
// streamed field is an iterator over the run's one shared stream, read as the handler ranges
// over it. A read of piped stdin ends when the run is canceled, with an [*InputError] whose
// cause is the cancellation's.
func (b *InputReader) fillStdin(rtx *Context, v reflect.Value, waived bool, argValues func(name string) ([]string, error)) error {
	if v.Kind() != reflect.Struct || v.NumField() == 0 {
		return nil
	}
	leaf := v.Field(v.NumField() - 1) // the running command's inputs
	if leaf.Kind() != reflect.Struct {
		return nil
	}
	sf := leaf.FieldByName("Stdin")
	field, ok := leaf.Type().FieldByName("Stdin")
	if !sf.IsValid() || !ok || (sf.Kind() != reflect.Pointer && sf.Kind() != reflect.Func) {
		return nil
	}
	raw := field.Tag.Get("stdin")
	if raw == "" || waived {
		return nil // a short-circuited run never waits on stdin
	}
	tag := parseStdinTag(raw)
	if skip, err := tag.fileGiven(argValues); skip || err != nil {
		return err // the file argument is the input; stdin is not read
	}

	if tag.stream {
		return b.bindStdinStream(rtx, sf, tag)
	}

	text, err := rtx.slurpStdin()
	if err != nil {
		return err
	}
	if text == "" {
		if tag.required {
			return usageBind(channelStdin, "", tag.emptyMessage(), nil)
		}
		return nil // nothing piped → leave Stdin nil
	}
	if tag.format == "bytes" {
		return bindRawStdin(sf, tag, text)
	}
	if strings.HasPrefix(text, "\xff\xfe") || strings.HasPrefix(text, "\xfe\xff") {
		return usageBind(channelStdin, "", "stdin is UTF-16 encoded; pipe UTF-8 instead", nil)
	}

	// A raw format binds the payload itself rather than decoding a document.
	if isRawStdinFormat(tag.format) {
		return bindRawStdin(sf, tag, text)
	}
	if tag.format == "jsonl" {
		return bindJSONL(sf, text, b.stdinSchemas, tag)
	}
	return b.bindStdinDocument(sf, tag.format, text)
}

// bindStdinDocument decodes a stdin document into the Stdin field, checking it against the
// command's stdin schema first.
func (b *InputReader) bindStdinDocument(sf reflect.Value, format, text string) error {
	data := stripBOM([]byte(text))
	js := b.stdinSchemas[sf.Type().Elem().Name()]
	if sf.Type().Elem().Kind() != reflect.Struct {
		return bindStdinValue(sf, format, data, js)
	}

	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return internalBind(channelStdin, "", fmt.Sprintf("unsupported stdin format %q", format), nil)
	}
	m, err := codec.Decode(data)
	if err != nil {
		return stdinDecodeError(format, data, err)
	}

	if js != "" {
		validator, err := schemaValidator(js)
		if err != nil {
			return internalBind(channelStdin, "", "invalid stdin schema", err)
		}
		if err := validator.Validate(m); err != nil {
			applyPatternMessages(js, err)
			return reconBind(channelStdin, err)
		}
	}

	reg, err := recon.New(recon.WithoutWatch(), recon.WithSource(recon.NewMapSource("stdin", m)))
	if err != nil {
		return internalBind(channelStdin, "", "could not build the stdin registry", err)
	}
	defer reg.Close()

	ptr := reflect.New(sf.Type().Elem()) // *<Prefix>Stdin
	if err := bindReconWaived(reg, ptr.Interface(), false); err != nil {
		return reconBind(channelStdin, err)
	}
	sf.Set(ptr)
	return nil
}

// bindStdinStream sets a streamed Stdin field to its iterator. Nothing is read here: with
// nothing that can be piped (a terminal, or no reader) the field stays nil, or a required
// stdin is a usage error.
func (b *InputReader) bindStdinStream(rtx *Context, sf reflect.Value, tag stdinTag) error {
	if sf.Kind() != reflect.Func {
		return internalBind(channelStdin, "", "a streamed stdin needs an iterator payload type", nil)
	}
	if !rtx.stdinState().piped() {
		if tag.required {
			return usageBind(channelStdin, "", tag.emptyMessage(), nil)
		}
		return nil
	}
	switch tag.format {
	case "lines":
		seq := reflect.ValueOf(rtx.streamLines(tag))
		if !seq.Type().AssignableTo(sf.Type()) {
			return internalBind(channelStdin, "", "stdin format lines with stream needs an iter.Seq2[string, error] payload type", nil)
		}
		sf.Set(seq)
		return nil
	case "jsonl":
		yieldType := sf.Type()
		name := ""
		if yieldType.NumIn() == 1 && yieldType.In(0).Kind() == reflect.Func && yieldType.In(0).NumIn() == 2 {
			name = yieldType.In(0).In(0).Name()
		}
		fn, ok := rtx.streamJSONL(sf.Type(), tag, b.stdinSchemas[name])
		if !ok {
			return internalBind(channelStdin, "", "stdin format jsonl with stream needs an iter.Seq2[<record>, error] payload type", nil)
		}
		sf.Set(fn)
		return nil
	}
	return internalBind(channelStdin, "", fmt.Sprintf("stdin format %q can't be streamed", tag.format), nil)
}

// bindStdinValue binds a stdin document whose type is not an object, such as a list: decoded
// to JSON, checked against schema (when given), then unmarshaled into the field.
func bindStdinValue(sf reflect.Value, format string, data []byte, schema string) error {
	raw, err := decodeDocumentJSON(format, data)
	if err != nil {
		return stdinDecodeError(format, data, err)
	}
	if schema != "" {
		if err := validateDocumentJSON(schema, raw); err != nil {
			return err
		}
	}
	ptr := reflect.New(sf.Type().Elem())
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return usageBind(channelStdin, "", fmt.Sprintf("could not bind stdin: %s", decodeMessage(err)), err)
	}
	sf.Set(ptr)
	return nil
}

// stdinDecodeError reports a stdin document that did not decode, with the decoder's position
// and reason.
func stdinDecodeError(format string, data []byte, err error) error {
	msg := "could not parse stdin as " + format
	if line, col, ok := recon.ParsePosition(err, data); ok {
		msg += fmt.Sprintf(" at line %d, column %d", line, col)
	}
	return usageBind(channelStdin, "", msg+": "+decodeMessage(err), err)
}

// isRawStdinFormat reports whether format binds stdin directly instead of decoding it.
func isRawStdinFormat(format string) bool {
	return format == "text" || format == "lines" || format == "bytes"
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

// bindRawStdin sets a raw stdin field from the piped text: the whole payload as one string
// for "text", its lines for "lines" (see splitStdinLines), or its bytes exactly as piped for
// "bytes". For "text" only a leading byte-order mark and the trailing line ending are trimmed
// (see trimAcquiredPayload).
func bindRawStdin(sf reflect.Value, tag stdinTag, text string) error {
	switch tag.format {
	case "text":
		if sf.Type().Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format text needs a string payload type", nil)
		}
		p := reflect.New(sf.Type().Elem())
		p.Elem().SetString(trimAcquiredPayload(text))
		sf.Set(p)
		return nil
	case "lines":
		elem := sf.Type().Elem()
		if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format lines needs a []string payload type", nil)
		}
		p := reflect.New(elem)
		p.Elem().Set(reflect.ValueOf(splitStdinLines(text, tag.nul)).Convert(elem))
		sf.Set(p)
		return nil
	case "bytes":
		elem := sf.Type().Elem()
		if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.Uint8 {
			return internalBind(channelStdin, "", "stdin format bytes needs a []byte payload type", nil)
		}
		p := reflect.New(elem)
		p.Elem().SetBytes([]byte(text))
		sf.Set(p)
		return nil
	}
	return internalBind(channelStdin, "", fmt.Sprintf("unsupported raw stdin format %q", tag.format), nil)
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
// pass sees it as present. A profile selector flag is never read from a configuration file: it
// takes the profile the bootstrap selected.
func (b *InputReader) reconcileFlags(v reflect.Value, chain []Command, store *parsedInputs, boot fileBootstrap, anchor int, waived bool, view *osView) error {
	if v.Kind() != reflect.Struct || !hasReconFlags(v) {
		return nil // no fallback flags → nothing to reconcile (env included)
	}
	cfgFiles := b.chainFiles(chain, false)
	files, err := b.fileSources(cfgFiles, boot, waived, view)
	if err != nil {
		return err
	}
	rd := fallbackRead{view: view, waiveFiles: waived, files: sourcePaths(files)}
	srcs := make([]recon.Source, 0, 2+len(files))
	srcs = append(srcs, recon.NewMapSource("flags", flagOverrides(v, chain, store, anchor)), flagEnvSource(v, b.envPrefix, view))
	srcs = append(srcs, files...)
	reg, err := recon.New(recon.WithoutWatch(), recon.WithSources(srcs...))
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
	source := val.Source()
	switch {
	case found:
		if vals, err = fallbackValues(val, def.Separator); err != nil {
			if rd.waiveFiles && source != osEnvSourceName {
				return nil, "", nil
			}
			return nil, "", fallbackCoerceError(chain, idx, name, fallbackOrigin(rd, source, tag.Get("env"), key), err)
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
	origin = fallbackOrigin(rd, source, tag.Get("env"), key)
	vals = flagEnum(def).canonical(vals)
	waivable := rd.waiveFiles && source != osEnvSourceName
	if vals, err = expandFallback(vals, tag, source, fallbackLabel(chain, idx, name), rd); err != nil {
		if waivable {
			return nil, "", nil
		}
		return nil, "", usageBind(channelFlag, name, err.Error()+fromSource(origin), err)
	}
	var prev reflect.Value
	if waivable {
		prev = reflect.New(field.Type()).Elem()
		prev.Set(field)
	}
	// A fallback value the flag's type cannot hold is a user error, as a bad argv value is.
	if err := coerceFlagValues(field, def, vals, rd.view.clockRef()); err != nil {
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
func coerceFlagValues(f reflect.Value, def FlagDef, vals []string, clock *runClock) error {
	if isObjectFlag(def) {
		return bindObjectFlag(f, vals, def)
	}
	if def.DottedKeys {
		return coerceMapDotted(f, vals)
	}
	return coerceTime(f, vals, flagTimeSpec(def, clock))
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
func hasReconFlags(v reflect.Value) bool {
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
	reg, err := recon.New(recon.WithoutWatch(), recon.WithSources(srcs...))
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
		reg, err := recon.New(recon.WithoutWatch(), recon.WithSources(srcs...))
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
			return regs.labels().bind(channelConfig, err)
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
type namedSource struct {
	recon.Source

	name string
	path string
}

func (s namedSource) Name() string { return s.name }

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

// fileSource builds the recon source for one config_files entry, schema-checked, reading the
// process environment and working directory.
func (b *InputReader) fileSource(f ConfigFile, overrides map[string]string) (recon.Source, error) {
	srcs, err := b.readFileSource(nil, f, fileBootstrap{paths: overrides}, false, nil)
	if err != nil {
		return nil, err
	}
	return srcs[0], nil
}

// readFileSource is [InputReader.fileSource] for a run: paths are expanded and made absolute
// with the run's view. It appends to dst the sources the file contributes, highest first: one,
// or for a file with profiles the selected profile's keys and then the shared keys. In a
// short-circuited run (waived) a file that cannot be located, read or parsed is skipped,
// standing in as an empty source under its own name so precedence holds, and no file is checked
// against its schema.
func (b *InputReader) readFileSource(dst []recon.Source, f ConfigFile, boot fileBootstrap, waived bool, view *osView) ([]recon.Source, error) {
	skipped := func() []recon.Source {
		return append(dst, namedSource{Source: recon.NewMapSource(f.Name, nil), name: f.Name})
	}
	src, path, err := b.openFileSource(f, boot.paths, view)
	if err != nil {
		if waived {
			return skipped(), nil
		}
		return nil, err
	}
	var names []string
	choice := boot.profiles[f.Name]
	if f.Profiles != nil {
		if names, err = profileNames(f, src, path); err != nil {
			src.Close()
			if waived {
				return skipped(), nil
			}
			return nil, err
		}
	}
	if !waived {
		if err := validateConfigFileAs(f, src, choice.name); err != nil {
			return nil, err
		}
	}
	if f.Profiles != nil {
		return append(dst, profiledSources(f, src, path, names, choice)...), nil
	}
	return append(dst, namedSource{Source: src, name: f.Name, path: path}), nil
}

// openFileSource locates and parses one config_files entry, returning the source and the file
// it resolved to. rotini expands the path and runs the discover search itself, from the run's
// view, so recon reads exactly the absolute path it is given.
func (b *InputReader) openFileSource(f ConfigFile, overrides map[string]string, view *osView) (recon.Source, string, error) {
	opts := []recon.FileOption{recon.WithPathExpansion(false)}
	if f.Format != "" {
		opts = append(opts, recon.WithFileFormat(f.Format))
	}
	path, overridden := overrides[f.Name]
	var dirs []string
	switch {
	case overridden:
		opts = append(opts, recon.WithOptional(false))
	case f.Discover != nil:
		var err error
		if dirs, err = discoverDirs(f.Discover, view); err != nil {
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
	resolved, err := view.expandPath(path)
	if err == nil {
		resolved, err = absPath(view, resolved)
	}
	if err != nil {
		return nil, "", usageBind(channelConfig, f.Name, fmt.Sprintf("could not resolve configuration file path %s: %v", path, err), err)
	}
	if dirs != nil {
		resolved = searchFile(dirs, filepath.Base(resolved))
	}
	if c, ok := fileCodec(f.Format, resolved); ok {
		opts = append(opts, recon.WithFileCodec(c))
	}
	src, err := recon.NewFileSource(resolved, opts...)
	if err != nil {
		// The file's content or path is the user's to fix.
		return nil, "", usageBind(channelConfig, f.Name, configFileProblem(resolved, err), err)
	}
	return src, resolved, nil
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
	if pe, ok := errors.AsType[*recon.ParseError](err); ok {
		loc := path
		if at := pe.Position.String(); at != "" {
			loc += ":" + at
		}
		return fmt.Sprintf("could not parse configuration file %s: %s", loc, decodeMessage(pe))
	}
	reason := "cannot be read"
	switch {
	case errors.Is(err, fs.ErrNotExist):
		reason = "no such file"
	case errors.Is(err, fs.ErrPermission):
		reason = "permission denied"
	case errors.Is(err, recon.ErrUnsupportedFormat):
		reason = "unsupported format"
	default:
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			reason = "is a directory"
		}
	}
	return fmt.Sprintf("could not open configuration file %s: %s", path, reason)
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
	return validateConfigFileAs(f, src, "")
}

// validateConfigFileAs is [validateConfigFile] for a run that selected profile: a file with
// profiles is checked as the run reads it, its shared keys merged with the selected profile's
// (see effectiveDocument). Profiles that aren't selected are not checked.
func validateConfigFileAs(f ConfigFile, src recon.Source, profile string) error {
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
	codec, ok := fileCodec(fsrc.Format(), path)
	if !ok {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("unsupported format %q for configuration file %s", fsrc.Format(), path), nil)
	}
	m, err := codec.Decode(stripBOM(data))
	if err != nil {
		pe := fileParseError(path, data, err)
		return usageBind(channelConfig, f.Name, configFileProblem(path, pe), pe)
	}
	if f.Profiles != nil {
		m = effectiveDocument(m, f.Profiles.Under, profile)
	}
	validator, err := schemaValidator(f.Schema)
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		applyPatternMessages(f.Schema, err)
		where := path
		if f.Profiles != nil && profile != "" {
			where += " (profile " + profile + ")"
		}
		return usageBind(channelConfig, f.Name,
			fmt.Sprintf("configuration file %s is invalid: %s", where, schemaDetail(err)), err)
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
			out[f.Name] = view.inputGetenv(firstSetEnv(view, pf.Env))
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
// the run's working directory up to the filesystem root, "xdg" is $XDG_CONFIG_HOME/<app> when
// that is absolute, else ~/.config/<app>, from the run's environment.
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

// spellings gives env and config inputs the value spellings flags accept and recon's decode
// does not: days and weeks in a duration (7d), yes/no, on/off and y/n for a bool (see
// parseBool), and a declared layout or relative form for a time field instead of RFC 3339. Only
// the keys of those typed fields are rewritten; a string input whose value is "yes" keeps it.
type spellings struct {
	recon.Source

	keys  valueKeys
	clock *runClock // what relative times are measured from
}

// valueKeys maps the recon keys of an Env or Config struct's bool fields, its duration fields,
// and its time fields that declare layouts or relative forms, to how their text is read.
type valueKeys struct {
	bools     map[string]bool
	times     map[string]timeSpec
	timeMaps  map[string]timeSpec // map[string]time.Time fields, whose values are times
	maps      map[string]bool     // every map field, which a configuration file holds as leaves
	durations map[string]bool
}

func (s spellings) Get(path recon.Path) (recon.Value, bool, error) {
	v, found, err := s.Source.Get(path)
	if !found || err != nil {
		return v, found, err
	}
	if tv, ok := s.timeMapValue(path, v); ok {
		return tv, true, nil
	}
	if v.Kind() != recon.StringKind {
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
	keys := valueKeys{bools: map[string]bool{}, times: map[string]timeSpec{}, timeMaps: map[string]timeSpec{}, maps: map[string]bool{}, durations: map[string]bool{}}
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
		fam := envFamily(view, base, sep)
		if len(fam) == 0 {
			if opt == "required" {
				name := et.Field(j).Tag.Get("rotini")
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

// envFamily collects the BASE<sep>… variables of the run's environment into a nested map.
func envFamily(view *osView, base, sep string) map[string]any {
	out := map[string]any{}
	if sep == "" {
		return out
	}
	prefix := base + sep
	for _, kv := range view.inputEnviron() {
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
					return labels.bind(channelEnv, err)
				}
				if _, err := fillEnvNested(ci.Field(j), labels.view); err != nil {
					return err
				}
			case "Config":
				if cfg == nil {
					continue
				}
				if err := bindConfigWaived(cfg.merged, ci.Field(j).Addr().Interface(), waived); err != nil {
					return cfg.labels().bind(channelConfig, err)
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
		enum := channelEnum(f.Tag)
		if !has && !enum.declared() && f.Tag.Get("path") == "" {
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
		// The user set a variable, not an input: name what they typed, and where it came from
		// when that was not the environment itself.
		if v := f.Tag.Get("env"); v != "" && cfg == nil {
			label = chosenEnv(view, v)
			label += view.inputOrigin(label)
		}
		secret := reconHasSecret(f.Tag.Get("recon"))
		vals := channelValues(val, typ)
		if tagExpandRule(f.Tag).declared() {
			vals = expandedStrings(s.Field(j), vals) // checked as expanded and bound
		}
		if len(vals) == 1 && vals[0] == "" && isPathType(typ) {
			typ = channelGoType(s.Field(j).Type()) // an empty path is no path: nothing to check
		}
		if err := checkChannelEnum(channelOf(cfg), label, enum, boundStrings(s.Field(j), vals), secret); err != nil {
			return err
		}
		if enum.rewrites() {
			canonicalizeField(s.Field(j), enum)
		}
		// A list is checked element by element as bound, since an environment variable's list
		// arrives as one string.
		if elems, count, ok := typedElems(s.Field(j)); ok && isArrayType(typ) {
			if err := checkTypedConstraints(label, typ, c, count, elems, secret, view.base()); err != nil {
				return err
			}
			continue
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

// channelEnum reads an env or config field's enum tags: enum:"<json array>" (the main values),
// ignorecase:"true", enumalias:"<json object>" (each alias to its value) and
// enumunlisted:"<json array>" (the hidden and deprecated values, which messages leave out).
// Codegen writes them as JSON so a member may contain any character; a malformed tag reads as
// absent.
func channelEnum(tag reflect.StructTag) enumSet {
	var e enumSet
	if v := tag.Get("enum"); v != "" {
		_ = json.Unmarshal([]byte(v), &e.values)
	}
	e.ignoreCase = tag.Get("ignorecase") == "true"
	var aliases map[string]string
	if v := tag.Get("enumalias"); v != "" {
		_ = json.Unmarshal([]byte(v), &aliases)
	}
	var unlisted []string
	if v := tag.Get("enumunlisted"); v != "" {
		_ = json.Unmarshal([]byte(v), &unlisted)
	}
	if len(aliases) == 0 && len(unlisted) == 0 {
		return e
	}
	for _, m := range e.values {
		d := EnumValue{Value: m, Hidden: slices.Contains(unlisted, m)}
		for a, to := range aliases {
			if to == m {
				d.Aliases = append(d.Aliases, a)
			}
		}
		sort.Strings(d.Aliases)
		e.described = append(e.described, d)
	}
	return e
}

// checkChannelEnum is the env and config counterpart of the argv enum check in [validate], so an
// enum advertised in help is enforced whichever channel supplied the value.
func checkChannelEnum(channel, label string, enum enumSet, vals []string, secret bool) error {
	if !enum.declared() {
		return nil
	}
	for _, v := range vals {
		if !enum.has(v) {
			listed := enum.listed()
			e := usageBind(channel, label, fmt.Sprintf("invalid value %q for %s (one of: %s)",
				redactValue(v, secret), label, strings.Join(listed, ", ")), nil)
			e.Token, e.Candidates = redactValue(v, secret), listed
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
// main value it matched: through an alias, or case-insensitively.
func canonicalizeField(f reflect.Value, enum enumSet) {
	switch {
	case !f.CanSet():
	case f.Kind() == reflect.String:
		f.SetString(enum.canonical([]string{f.String()})[0])
	case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
		for i := range f.Len() {
			e := f.Index(i)
			e.SetString(enum.canonical([]string{e.String()})[0])
		}
	}
}

// channelConstraints reads the validation struct-tags codegen emits on a channel field into a
// [Constraints]. A pointer bound is set whenever its tag is present, so min:"0" enforces >= 0.
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
	intPtrTag := func(name string, dst **int) {
		if n, err := strconv.Atoi(tag.Get(name)); err == nil {
			*dst, has = new(n), true
		}
	}
	floatTag("min", &c.Minimum)
	floatTag("max", &c.Maximum)
	floatTag("xmin", &c.ExclusiveMinimum)
	floatTag("xmax", &c.ExclusiveMaximum)
	floatTag("multipleof", &c.MultipleOf)
	intTag("minlen", &c.MinLength)
	intPtrTag("maxlen", &c.MaxLength)
	intTag("minitems", &c.MinItems)
	intPtrTag("maxitems", &c.MaxItems)
	if v := tag.Get("pattern"); v != "" {
		c.Pattern, has = v, true
	}
	c.PatternMessage = tag.Get("patternmsg")
	if tag.Get("unique") == "true" {
		c.UniqueItems, has = true, true
	}
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
		// The element type, so per-element bounds apply; a byte-backed type is one value.
		if t.Elem().Kind() == reflect.Uint8 {
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
