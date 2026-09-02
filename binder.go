package rotini

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/recon"
)

// The multi-source [Binder]: fill a command's typed inputs from every declared channel
// — argv, environment, configuration files, stdin, defaults — reconciled through recon
// in the documented precedence. It is the engine behind [Collect]; the à-la-carte
// per-channel surface is in overlay.go.

// KeyBinder is the conventional registry key a main binds the [Binder] under
// (and handlers retrieve it by). The binder is opt-in like every service —
// the generated main binds it only when the spec declares non-argv channels.
const KeyBinder = "binder"

// Binder is the default multi-source input binder: it fills a command's typed
// inputs from argv (flags + positional arguments, via the embedded [Parser]) and
// from the non-argv channels — environment variables and configuration files —
// reconciled and decoded by recon. It is a service, bound like the parser:
//
//	// main.go
//	rth.Program.
//	    Bind(rotini.KeyBinder, rotini.NewBinder(BindMeta)).
//	    Execute()
//
//	// a handler
//	binder := rotini.MustGet[*rotini.Binder](rtx, rotini.KeyBinder)
//	var in WidgetCreateInputs
//	if err := binder.Bind(rtx, &in); err != nil { /* handler owns it */ }
//
// Env values fill the generated <Prefix>Env struct (recon's env source maps a
// field's recon key to its SNAKE_UPPER form); config-file values fill <Prefix>Config
// from the document's configuration_files (carried in [BindMeta]). The two
// channels use independent registries, so an env var never leaks into a config field
// or vice versa. Flags may also fall back to env/config, and a leaf command may decode
// a typed stdin payload.
type Binder struct {
	parser       *Parser
	configFiles  []ConfigFile
	stdinSchemas map[string]string // "<Prefix>Stdin" type name → JSON Schema for payload validation
	envPrefix    string            // BindMeta.EnvPrefix: scopes derived env-var names
	sources      []recon.Source    // BindMeta.Sources: custom sources, after the declared files
}

// KeyBindMeta is the registry key the generated NewProgram binds the CLI's
// [BindMeta] descriptor under, so the per-channel functions ([ParseEnv],
// [ParseFiles], [ParseStdin], …) can derive their configuration from the
// [Context] alone. A standalone Context (tests, [NewContextFor]) opts in the
// same way: rtx.Bind(rotini.KeyBindMeta, meta).
const KeyBindMeta = "bindmeta"

// binderFor builds a Binder from the Context's bound BindMeta — the zero meta
// when none is bound, so a CLI with no config files/stdin schemas/env prefix
// needs no ceremony at all.
func binderFor(rtx *Context) *Binder {
	if rtx == nil {
		return NewBinder(BindMeta{}) // the channel layer reports the nil context as a ParseError
	}
	meta, _ := Get[BindMeta](rtx, KeyBindMeta)
	return NewBinder(meta)
}

// NewBinder returns the default binder, configured from the generated descriptor
// (the generated package's BindMeta var) — its configuration_files sources and per-command
// stdin payload schemas.
func NewBinder(meta BindMeta) *Binder {
	return &Binder{parser: NewParser(), configFiles: meta.ConfigFiles, stdinSchemas: meta.StdinSchemas, envPrefix: meta.EnvPrefix, sources: meta.Sources}
}

// Bind fills out — a non-nil pointer to the typed inputs struct codegen emits — from
// every wired channel: argv flags + positional arguments (via the parser), env- and
// config-file fallbacks for flags that declare them, the pure Env/Config channels,
// and a leaf command's typed stdin payload. Required/enum/constraint validation of
// the argv channel runs once over the fully-reconciled values — so a required flag is
// satisfiable from env or config, not only from argv, and an env/config-supplied value
// is enum-checked. It returns the first error: a [*ParseError] from the argv channel
// (parsing/validation), or a [*BindError] from the env/config/stdin channels — both
// categorized ([CategoryOf]) and non-leaky, with the underlying recon cause reachable
// via errors.As.
func (b *Binder) Bind(rtx *Context, out any) error {
	if b == nil {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: nil binder"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &ParseError{Kind: ParseKindInternal, Msg: "rotini: Bind out argument must be a non-nil pointer to an inputs struct"}
	}

	// 1. argv → Flags + Arguments, WITHOUT validation: required/enum (and, later,
	//    declared constraints) are checked once in step 3, over the fully-reconciled
	//    store, so a required flag can be satisfied by env/config — not only by argv.
	store, chain, err := b.parser.parseBind(rtx, out)
	if err != nil {
		return err
	}
	v := rv.Elem()

	// 1b. two-phase bootstrap: a config_source flag/env names the config file
	//     the file channel then reads (the path is data the argv+env phase owns).
	overrides := b.pathOverrides(chain, store)

	// 2. flag fallback: for flags that declare a recon key, reconcile
	//    argv-set > env (SNAKE_UPPER of the key) > config; otherwise keep the
	//    Parser's value (an explicit argv value or the flag's default). Each
	//    reconciled value is recorded back into the store so step 3 validates it too.
	if err := b.reconcileFlags(v, chain, rtx.Args, store, overrides); err != nil {
		return err
	}

	// 3. validate the reconciled flags + arguments (required + enum) — the single
	//    validation locus for the argv channel, run after fallback so it sees every
	//    source. Argv errors surface before any channel error.
	if err := validate(chain, store); err != nil {
		return err
	}
	if err := validateFlagGroups(chain, store); err != nil {
		return err
	}
	if err := validateFlagDependencies(chain, store); err != nil {
		return err
	}

	// 4. env + config → the Env/Config sub-structs, from independent registries
	//    (so an env var never leaks into a config field, or vice versa); recon
	//    enforces each channel's own required/validator. The env source honors an
	//    input's explicit `variable` (else recon's SNAKE_UPPER).
	envReg, err := recon.New(recon.WithSources(envSources(v, b.envPrefix)...))
	if err != nil {
		return internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer envReg.Close()

	cfgRegs, err := b.configRegs(chain, overrides)
	if err != nil {
		return err
	}
	defer cfgRegs.Close()

	if err := fillChannels(v, envReg, cfgRegs); err != nil {
		return err
	}

	// 4b. validate the env/config channel values against their declared constraints
	//     (the same checks A1 applies to argv), over the values actually provided.
	if err := validateChannels(v, envReg, cfgRegs); err != nil {
		return err
	}

	// 5. stdin → the leaf command's typed payload (decoded by its declared format).
	return b.fillStdin(rtx, v)
}

// fillStdin decodes piped stdin into the leaf command's Stdin payload field, when it
// declares one, using its `stdin:"<format>[,required]"` tag. Stdin is a single
// stream, so only the leaf (the running command) consumes it; when nothing is piped
// the Stdin field is left nil — unless the spec marked the payload required, in
// which case an empty stdin is an error. The bytes are read from os.Stdin — see
// readStdin. The decoded payload is validated against the command's stdin JSON
// Schema (from BindMeta) before binding, so a malformed document is rejected with
// a clear error.
func (b *Binder) fillStdin(rtx *Context, v reflect.Value) error {
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
		if required {
			return usageBind(channelStdin, "", fmt.Sprintf("required stdin payload is empty — pipe a %s document", format), nil)
		}
		return nil // nothing piped → leave Stdin nil
	}
	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return internalBind(channelStdin, "", fmt.Sprintf("unsupported stdin format %q", format), nil)
	}
	m, err := codec.Decode(data)
	if err != nil {
		return usageBind(channelStdin, "", fmt.Sprintf("could not decode stdin as %s", format), err)
	}

	// Validate the decoded payload against the command's stdin schema (when one was
	// generated for this <Prefix>Stdin type), before binding.
	if js := b.stdinSchemas[sf.Type().Elem().Name()]; js != "" {
		validator, err := recon.NewJSONSchemaValidator([]byte(js))
		if err != nil {
			return internalBind(channelStdin, "", "invalid stdin schema", err)
		}
		if err := validator.Validate(m); err != nil {
			return reconBind(channelStdin, err)
		}
	}

	reg, err := recon.New(recon.WithSource(recon.NewMapSource("stdin", m)))
	if err != nil {
		return internalBind(channelStdin, "", "could not build the stdin registry", err)
	}
	defer reg.Close()

	ptr := reflect.New(sf.Type().Elem()) // *<Prefix>Stdin
	if err := reg.Bind(ptr.Interface()); err != nil {
		return reconBind(channelStdin, err)
	}
	sf.Set(ptr)
	return nil
}

// parseStdinTag splits a generated `stdin:"<format>[,required]"` tag value into
// the decode format and the required marker (the spec's stdin schema declared
// required: true — an empty stdin is then an error rather than a nil payload).
func parseStdinTag(tag string) (format string, required bool) {
	format, opt, _ := strings.Cut(tag, ",")
	return format, opt == "required"
}

// readStdin returns the bytes available on r — the run's stdin ([Context.Stdin], which
// the Program sets from [Program.WithStdin], default os.Stdin). When r is the real
// os.Stdin attached to an interactive terminal it returns nil rather than blocking — a
// stdin channel is for piped/redirected input, not interactive typing. Any other reader
// (e.g. a test's strings.Reader) is read to EOF. A nil reader yields no data.
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

// reconcileFlags overrides each fallback flag (a Flags field carrying a recon tag)
// with its reconciled value: argv-set flags (highest) > env (SNAKE_UPPER of the recon
// key) > config files. A flag not present in any source keeps the value the Parser
// already bound (its explicit argv value or declared default). Argv-only flags (no
// recon tag) are untouched. Each reconciled value is also written back into store, so
// the deferred [validate] pass sees an env/config-supplied flag as present (satisfying
// a required check) and range-checks it against any enum.
func (b *Binder) reconcileFlags(v reflect.Value, chain []ResolvedCommand, argv []string, store *parsedInputs, overrides map[string]string) error {
	if v.Kind() != reflect.Struct || !hasReconFlags(v) {
		return nil // no fallback flags → nothing to reconcile (env included)
	}
	offset := len(chain) - v.NumField()
	files, err := b.fileSources(b.chainConfigFiles(chain), overrides)
	if err != nil {
		return err
	}
	srcs := make([]recon.Source, 0, 2+len(files))
	srcs = append(srcs, recon.NewMapSource("flags", flagOverrides(v, chain, argv)), flagEnvSource(b.envPrefix))
	srcs = append(srcs, files...)
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return internalBind(channelFlag, "", "could not build the flag-fallback registry", err)
	}
	defer reg.Close()

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
			val, found, err := reg.Get(key)
			if err != nil {
				return reconBind(channelFlag, err)
			}
			if !found {
				continue
			}
			// Value.String stringifies any kind (int/float/bool config values too) —
			// the strict AsString returns "" for non-strings, silently dropping a
			// numeric/bool flag's env/config fallback.
			s := val.String()
			coerce(flags.Field(j), []string{s})
			if name := ft.Field(j).Tag.Get("rotini"); name != "" && offset >= 0 {
				recordFlag(store, offset+i, name, s)
			}
		}
	}
	return nil
}

// recordFlag writes a reconciled flag value into the parsed store at its chain frame,
// so deferred validation treats it as present (for required) and enum-checks it.
func recordFlag(store *parsedInputs, idx int, name, value string) {
	if store == nil || idx < 0 || idx >= len(store.scopes) {
		return
	}
	if store.scopes[idx].flags == nil {
		store.scopes[idx].flags = map[string][]string{}
	}
	store.scopes[idx].flags[name] = []string{value}
}

// hasReconFlags reports whether any command's Flags sub-struct declares a recon key
// (i.e. there is at least one env/config fallback flag to reconcile). When false, the
// binder skips building a registry entirely.
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

// flagOverrides maps the canonical key of every fallback flag explicitly set on
// argv to its (Parser-bound) value — the highest-precedence layer for reconciliation.
func flagOverrides(v reflect.Value, chain []ResolvedCommand, argv []string) map[string]any {
	m := map[string]any{}
	offset := len(chain) - v.NumField()
	if offset < 0 {
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
			fd, ok := findFlagDef(chain[offset+i].Flags, ft.Field(j).Tag.Get("rotini"))
			if ok && flagWasSet(argv, fd.Identifiers) {
				setNested(m, key, flags.Field(j).Interface())
			}
		}
	}
	return m
}

// setNested stores val at a dotted key path in m, creating nested maps as needed —
// recon resolves a "create.color" path by walking nested maps, so the flag source
// must be nested too (not a flat "create.color" key).
func setNested(m map[string]any, key string, val any) {
	segs := strings.Split(key, ".")
	for _, seg := range segs[:len(segs)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[seg] = next
		}
		m = next
	}
	m[segs[len(segs)-1]] = val
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

// flagWasSet reports whether any of a flag's identifiers appears as a flag token in
// argv (exact match; clustered short flags are not detected — a documented edge).
func flagWasSet(argv, identifiers []string) bool {
	for _, tok := range argv {
		if tok == "--" {
			break
		}
		if len(tok) < 2 || tok[0] != '-' {
			continue
		}
		name := tok
		if before, _, ok := strings.Cut(tok, "="); ok {
			name = before
		}
		if slices.Contains(identifiers, name) {
			return true
		}
	}
	return false
}

// configRegistry builds a recon registry over the configuration_files (for the
// pure Config channel), first (highest precedence) to last as declared.
// overrides carries any config_source-supplied paths (see pathOverrides).
func (b *Binder) configRegistry(files []ConfigFile, overrides map[string]string) (*recon.Registry, error) {
	srcs, err := b.fileSources(files, overrides)
	if err != nil {
		return nil, err
	}
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return nil, internalBind(channelConfig, "", "could not build the configuration registry", err)
	}
	return reg, nil
}

// cfgRegs is the config channel's registries for one bind: the merged
// precedence chain, plus lazily-built single-file registries for inputs the
// spec pins to one file (`file:` → the generated cfgfile tag).
type cfgRegs struct {
	binder    *Binder
	files     []ConfigFile // sources in scope for the invoked chain, nearest-wins order
	overrides map[string]string
	merged    *recon.Registry
	perFile   map[string]*recon.Registry
}

// configRegs builds the merged config registry and the lazy per-file cache over
// the sources in scope for chain — the cascade (see [Binder.chainConfigFiles]).
func (b *Binder) configRegs(chain []ResolvedCommand, overrides map[string]string) (*cfgRegs, error) {
	files := b.chainConfigFiles(chain)
	merged, err := b.configRegistry(files, overrides)
	if err != nil {
		return nil, err
	}
	return &cfgRegs{binder: b, files: files, overrides: overrides, merged: merged, perFile: map[string]*recon.Registry{}}, nil
}

// chainConfigFiles returns the config_files in scope for the resolved chain — the
// union along it (D-W3.1) — ordered NEAREST-WINS: the invoked (deepest) command's
// sources first (highest precedence in the merged registry), then each ancestor up
// to the root, preserving each command's own declared order. A source is in scope
// when its Scope is one of the chain's command paths; off-branch sources are
// excluded. An UNSCOPED source (Scope=="" — a manual/legacy global) is always in
// scope, appended last (lowest precedence).
func (b *Binder) chainConfigFiles(chain []ResolvedCommand) []ConfigFile {
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

// For returns the registry over ONLY the named configuration_files entry —
// what a pinned input's value (and its required) is judged against.
func (c *cfgRegs) For(name string) (*recon.Registry, error) {
	if reg, ok := c.perFile[name]; ok {
		return reg, nil
	}
	for _, f := range c.files {
		if f.Name != name {
			continue
		}
		src, err := c.binder.fileSource(f, c.overrides)
		if err != nil {
			return nil, err
		}
		reg, err := recon.New(recon.WithSource(src))
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
	c.merged.Close()
	for _, r := range c.perFile {
		r.Close()
	}
}

// bindPinnedConfig re-binds each cfgfile-pinned field of one Config struct
// against ONLY its pinned file's registry — including its recon required,
// which must be judged against that file, not the merged chain. A pinned key
// absent from its file zeroes the field, even when another file holds the key.
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
		if err := reg.Bind(tmp.Interface()); err != nil {
			return reconBind(channelConfig, err)
		}
		for i, j := range idxs {
			cs.Field(j).Set(tmp.Elem().Field(i))
		}
	}
	return nil
}

// fileSources builds one recon file source per configuration_files entry, in
// declared (precedence) order. Missing files are tolerated; ~ is expanded. An
// entry with a Discover strategy resolves its search directories now (at parse
// time) — the first directory containing the file wins. An entry whose path
// was supplied through config_source (overrides) reads that exact file and is
// NOT optional: the user explicitly asked for it, so a missing file errors.
// Custom BindMeta.Sources follow the declared files — explicit files beat
// ambient services (decided at ergonomics E1/E3-S4).
func (b *Binder) fileSources(files []ConfigFile, overrides map[string]string) ([]recon.Source, error) {
	srcs := make([]recon.Source, 0, len(files)+len(b.sources))
	for _, f := range files {
		src, err := b.fileSource(f, overrides)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, src)
	}
	return append(srcs, b.sources...), nil
}

// namedSource renames a recon source to its configuration_files logical name.
// FileSource names itself after the file's basename, and registry source names
// must be unique — but two declared entries may legitimately share a basename
// (a walk-up project file and a home file both called ".app.yaml"). The binder
// reads once at parse time, so the wrapper's loss of optional capabilities
// (live watch) costs nothing here.
type namedSource struct {
	recon.Source

	name string
}

func (s namedSource) Name() string { return s.name }

// fileSource builds the recon source for one configuration_files entry, named
// by the entry's logical name.
func (b *Binder) fileSource(f ConfigFile, overrides map[string]string) (recon.Source, error) {
	opts := []recon.FileOption{recon.WithPathExpansion(true)}
	if f.Format != "" {
		opts = append(opts, recon.WithFileFormat(f.Format))
	}
	path, overridden := overrides[f.Name]
	switch {
	case overridden:
		opts = append(opts, recon.WithOptional(false))
	case f.Discover != nil:
		dirs, err := discoverDirs(f.Discover)
		if err != nil {
			return nil, internalBind(channelConfig, f.Name, fmt.Sprintf("could not resolve the search path for configuration file %q", f.Name), err)
		}
		path = f.Discover.File
		opts = append(opts, recon.WithOptional(true), recon.WithSearchPaths(dirs...))
	default:
		path = f.Path
		opts = append(opts, recon.WithOptional(true))
	}
	src, err := recon.NewFileSource(path, opts...)
	if err != nil {
		// The file's content or path is the user's to fix (a config_source
		// override that points at a malformed/unreadable file surfaces here).
		return nil, usageBind(channelConfig, f.Name, fmt.Sprintf("could not open configuration file %q (%s)", f.Name, path), err)
	}
	if err := validateConfigFile(f, src); err != nil {
		return nil, err
	}
	return namedSource{Source: src, name: f.Name}, nil
}

// validateConfigFile checks the loaded document of one configuration_files
// entry against its declared schema (the spec entry's `schema:`) — the same
// load-time gate the stdin channel applies to its payload. The file recon
// actually resolved (after ~ expansion, discovery, or a config_source
// override) is the file validated. An absent optional file passes vacuously:
// document-shape validation gates what IS loaded; absence is the per-input
// `required`'s concern. The extra read happens once per source construction
// and only for entries that declare a schema.
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
		// A present-but-unreadable file the user controls (permissions, etc.).
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
	validator, err := recon.NewJSONSchemaValidator([]byte(f.Schema))
	if err != nil {
		return internalBind(channelConfig, f.Name, fmt.Sprintf("invalid schema for configuration file %q", f.Name), err)
	}
	if err := validator.Validate(m); err != nil {
		return usageBind(channelConfig, f.Name,
			fmt.Sprintf("configuration file %q (%s) is invalid: %s", f.Name, path, schemaDetail(err)), err)
	}
	return nil
}

// pathOverrides resolves each ConfigFile's config_source inputs (PathFrom) to
// the path they supply, by the documented precedence: the flag explicitly set
// on argv, then the env variable, then the flag's declared default. An entry
// none of them supplies keeps its own path/discover (no map entry).
func (b *Binder) pathOverrides(chain []ResolvedCommand, store *parsedInputs) map[string]string {
	out := map[string]string{}
	for _, f := range b.chainConfigFiles(chain) {
		pf := f.PathFrom
		if pf == nil {
			continue
		}
		explicit, defaulted := storeFlagValue(chain, store, pf.Flag)
		switch {
		case explicit != "":
			out[f.Name] = explicit
		case pf.Env != "" && os.Getenv(pf.Env) != "":
			out[f.Name] = os.Getenv(pf.Env)
		case defaulted != "":
			out[f.Name] = defaulted
		}
	}
	return out
}

// storeFlagValue finds a flag's parsed value by logical name across the chain,
// split by how it got there: explicitly set on argv vs. filled by its default.
// The last value wins for a repeated flag.
func storeFlagValue(chain []ResolvedCommand, store *parsedInputs, name string) (explicit, defaulted string) {
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

// discoverDirs resolves a Discover strategy to the ordered directory list its
// file is searched in. "walk-up" is the working directory up to the filesystem
// root (project-local config, git-style); "xdg" is $XDG_CONFIG_HOME/<app>,
// defaulting to ~/.config/<app>.
func discoverDirs(d *DiscoverDef) ([]string, error) {
	switch d.Strategy {
	case "walk-up":
		dir, err := os.Getwd()
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
		// fs.XDGConfigDir is XDG-literal on every platform ($XDG_CONFIG_HOME
		// else ~/.config/<app>) — matching this branch's prior behavior.
		dir, err := fs.XDGConfigDir(d.App)
		if err != nil {
			return nil, fmt.Errorf("xdg discovery: %w", err)
		}
		return []string{dir}, nil
	default:
		return nil, fmt.Errorf("unknown discover strategy %q", d.Strategy)
	}
}

// envSources builds the env channel's recon sources for one inputs value: the
// process environment, with per-input explicit `variable:` names pinned in BOTH
// directions via recon.WithEnvVars (the forward key→variable projection and the
// inverse variable→key parser its enumeration needs), and the optional env
// prefix scoping the convention-named rest. (Nested env families — envnest —
// are not registry data: recon resolves leaf keys only, so fillEnvNested sets
// those fields directly.)
func envSources(v reflect.Value, envPrefix string) []recon.Source {
	opts := []recon.EnvOption{recon.WithEnvVars(envExplicit(v))}
	if envPrefix != "" {
		opts = append(opts, recon.WithEnvPrefix(envPrefix+"_"))
	}
	return []recon.Source{recon.NewOSEnvSource(opts...)}
}

// flagEnvSource is the env source flags' fallbacks read: the plain SNAKE_UPPER
// projection of each recon key, scoped under env_prefix when one is declared
// (recon strips the prefix before parsing and prepends it when projecting).
func flagEnvSource(envPrefix string) recon.Source {
	if envPrefix == "" {
		return recon.NewOSEnvSource()
	}
	return recon.NewOSEnvSource(recon.WithEnvPrefix(envPrefix + "_"))
}

// fillEnvNested fills each nested env input of one Env struct (the generated
// `envnest:"<BASE>,<sep>[,required]"` tag — spec nesting:) directly from its
// variable family: with BASE=ACME_HTTP and sep=__, ACME_HTTP__RETRY__MAX=9
// binds {retry: {max: "9"}}. Segments are lowercased; values stay strings. It
// returns the recon keys it filled (the channel parsers record presence from
// it) and errors on a required family with no variables.
func fillEnvNested(env reflect.Value) (map[string]bool, error) {
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
		fam := envFamily(base, sep)
		if len(fam) == 0 {
			if opt == "required" {
				name := et.Field(j).Tag.Get("rotini")
				return filled, usageBind(channelEnv, name,
					fmt.Sprintf("environment input %q is required — set %s%s* variables", name, base, sep), nil)
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

// envFamily collects the BASE<sep>… environment variables into a nested map.
func envFamily(base, sep string) map[string]any {
	out := map[string]any{}
	if sep == "" {
		return out
	}
	prefix := base + sep
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			continue
		}
		segs := strings.Split(strings.ToLower(name[len(prefix):]), strings.ToLower(sep))
		setNested(out, strings.Join(segs, "."), val)
	}
	return out
}

// envExplicit collects the recon-key → explicit-env-var mapping from every Env
// field carrying an `env:"<VAR>"` tag (the spec's per-input `variable`), so the env
// source reads that exact variable instead of the SNAKE_UPPER default.
func envExplicit(v reflect.Value) map[string]string {
	m := map[string]string{}
	if v.Kind() != reflect.Struct {
		return m
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		env := ci.FieldByName("Env")
		if !env.IsValid() || env.Kind() != reflect.Struct {
			continue
		}
		et := env.Type()
		for j := range env.NumField() {
			vr := et.Field(j).Tag.Get("env")
			if key := reconKey(et.Field(j).Tag.Get("recon")); vr != "" && key != "" {
				m[key] = vr
			}
		}
	}
	return m
}

// fillChannels walks a <Cmd>Inputs struct (one field per command on the resolved
// path) and, for each command's <Prefix>CommandInputs, recon-binds its Env struct
// from envReg and its Config struct from cfgReg via their recon tags.
func fillChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs) error {
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
				if err := envReg.Bind(ci.Field(j).Addr().Interface()); err != nil {
					return reconBind(channelEnv, err)
				}
				if _, err := fillEnvNested(ci.Field(j)); err != nil {
					return err
				}
			case "Config":
				if err := cfg.merged.Bind(ci.Field(j).Addr().Interface()); err != nil {
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

// validateChannels enforces the declared numeric/string/array constraints on each
// command's Env and Config fields, reusing the same [checkConstraints] A1 applies to
// argv. It is presence-aware: a field is checked only when its source actually provided
// a value (reg.Get found), so absence is governed by `required` (recon), not by these.
func validateChannels(v reflect.Value, envReg *recon.Registry, cfg *cfgRegs) error {
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
				if err := validateChannelStruct(ci.Field(j), envReg, nil); err != nil {
					return err
				}
			case "Config":
				if err := validateChannelStruct(ci.Field(j), cfg.merged, cfg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateChannelStruct checks every constrained field of an Env/Config sub-struct
// against the value its registry resolved (when present). A cfgfile-pinned
// field is judged against its own file's registry (cfg non-nil for the config
// channel).
func validateChannelStruct(s reflect.Value, reg *recon.Registry, cfg *cfgRegs) error {
	if s.Kind() != reflect.Struct {
		return nil
	}
	st := s.Type()
	for j := range s.NumField() {
		f := st.Field(j)
		c, has := channelConstraints(f.Tag)
		if !has {
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
			channel := channelEnv
			if cfg != nil {
				channel = channelConfig
			}
			return reconBind(channel, err)
		}
		if !found {
			continue // only provided values are constraint-checked
		}
		typ := channelGoType(s.Field(j).Type())
		label := f.Tag.Get("rotini")
		if label == "" {
			label = key
		}
		if err := checkConstraints(label, typ, c, channelValues(val, typ), reconHasSecret(f.Tag.Get("recon"))); err != nil {
			return err
		}
	}
	return nil
}

// channelConstraints reads the validation struct-tags codegen emits on a channel field
// (min/max/xmin/xmax/multipleof/minlen/maxlen/minitems/maxitems/pattern) into a
// [Constraints]. Tag PRESENCE is what carries a numeric bound's declaredness —
// min:"0" is a real, enforced >= 0.
func channelConstraints(tag reflect.StructTag) (Constraints, bool) {
	var c Constraints
	has := false
	if v := tag.Get("min"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Minimum, has = new(f), true
		}
	}
	if v := tag.Get("max"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Maximum, has = new(f), true
		}
	}
	if v := tag.Get("xmin"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.ExclusiveMinimum, has = new(f), true
		}
	}
	if v := tag.Get("xmax"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.ExclusiveMaximum, has = new(f), true
		}
	}
	if v := tag.Get("multipleof"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.MultipleOf, has = new(f), true
		}
	}
	if v := tag.Get("minlen"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MinLength, has = n, true
		}
	}
	if v := tag.Get("maxlen"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxLength, has = n, true
		}
	}
	if v := tag.Get("minitems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MinItems, has = n, true
		}
	}
	if v := tag.Get("maxitems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxItems, has = n, true
		}
	}
	if v := tag.Get("pattern"); v != "" {
		c.Pattern, has = v, true
	}
	return c, has
}

// channelGoType maps a channel field's Go type to the type string checkConstraints
// expects (numeric bounds apply to int/float, length/pattern to strings, item counts
// to slices).
func channelGoType(t reflect.Type) string {
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

// channelValues renders a reconciled value as the string(s) checkConstraints consumes:
// the elements for an array type, else the single canonical string.
func channelValues(val recon.Value, typ string) []string {
	if isArrayType(typ) {
		if items, err := val.AsSlice(); err == nil {
			out := make([]string, len(items))
			for i, it := range items {
				out[i] = it.String()
			}
			return out
		}
	}
	return []string{val.String()}
}
