package rotini

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-rotini/recon"
)

// Binder is the default multi-source input binder: it fills a command's typed
// inputs from argv (flags + positional arguments, via the embedded [Parser]) and
// from the non-argv channels — environment variables and configuration files —
// reconciled and decoded by recon. It is a service, bound like the parser:
//
//	// main.go
//	rth.Program.
//	    Bind("binder", rotini.NewBinder(rtg.BindMeta)).
//	    Execute()
//
//	// a handler
//	binder := rotini.MustGet[*rotini.Binder](rtx, "binder")
//	var in rtg.WidgetCreateInputs
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
}

// NewBinder returns the default binder, configured from the generated descriptor
// (the rtg package's BindMeta var) — its configuration_files sources and per-command
// stdin payload schemas.
func NewBinder(meta BindMeta) *Binder {
	return &Binder{parser: NewParser(), configFiles: meta.ConfigFiles, stdinSchemas: meta.StdinSchemas}
}

// Bind fills out — a non-nil pointer to the typed inputs struct rtg emits — from
// every wired channel: argv flags + positional arguments (via the parser), env- and
// config-file fallbacks for flags that declare them, the pure Env/Config channels,
// and a leaf command's typed stdin payload. Required/enum/constraint validation of
// the argv channel runs once over the fully-reconciled values — so a required flag is
// satisfiable from env or config, not only from argv, and an env/config-supplied value
// is enum-checked. It returns the first error (a usage error from parsing/validation,
// or a recon bind/validation error for env/config/stdin).
func (b *Binder) Bind(rtx *Context, out any) error {
	if b == nil {
		return &usageError{msg: "rotini: nil binder"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &usageError{msg: "rotini: Bind out argument must be a non-nil pointer to an inputs struct"}
	}

	// 1. argv → Flags + Arguments, WITHOUT validation: required/enum (and, later,
	//    declared constraints) are checked once in step 3, over the fully-reconciled
	//    store, so a required flag can be satisfied by env/config — not only by argv.
	store, chain, err := b.parser.parseBind(rtx, out)
	if err != nil {
		return err
	}
	v := rv.Elem()

	// 2. flag fallback: for flags that declare a recon key, reconcile
	//    argv-set > env (SNAKE_UPPER of the key) > config; otherwise keep the
	//    Parser's value (an explicit argv value or the flag's default). Each
	//    reconciled value is recorded back into the store so step 3 validates it too.
	if err := b.reconcileFlags(v, chain, rtx.Args(), store); err != nil {
		return err
	}

	// 3. validate the reconciled flags + arguments (required + enum) — the single
	//    validation locus for the argv channel, run after fallback so it sees every
	//    source. Argv errors surface before any channel error.
	if err := validate(chain, store); err != nil {
		return err
	}
	if err := validateFlagGroups(chain, rtx.Args()); err != nil {
		return err
	}
	if err := validateFlagDependencies(chain, rtx.Args()); err != nil {
		return err
	}

	// 4. env + config → the Env/Config sub-structs, from independent registries
	//    (so an env var never leaks into a config field, or vice versa); recon
	//    enforces each channel's own required/validator. The env source honors an
	//    input's explicit `variable` (else recon's SNAKE_UPPER).
	envReg, err := recon.New(recon.WithSource(
		recon.NewOSEnvSource(recon.WithEnvTransform(envTransform(envExplicit(v))))))
	if err != nil {
		return fmt.Errorf("rotini: env registry: %w", err)
	}
	defer envReg.Close()

	cfgReg, err := b.configRegistry()
	if err != nil {
		return err
	}
	defer cfgReg.Close()

	if err := fillChannels(v, envReg, cfgReg); err != nil {
		return err
	}

	// 4b. validate the env/config channel values against their declared constraints
	//     (the same checks A1 applies to argv), over the values actually provided.
	if err := validateChannels(v, envReg, cfgReg); err != nil {
		return err
	}

	// 5. stdin → the leaf command's typed payload (decoded by its declared format).
	return b.fillStdin(v)
}

// fillStdin decodes piped stdin into the leaf command's Stdin payload field, when it
// declares one, using the format on its `stdin:"<format>"` tag. Stdin is a single
// stream, so only the leaf (the running command) consumes it; when nothing is piped
// the Stdin field is left nil. The bytes are read from os.Stdin — see readStdin.
// The decoded payload is validated against the command's stdin JSON Schema (from
// BindMeta) before binding, so a malformed document is rejected with a clear error.
func (b *Binder) fillStdin(v reflect.Value) error {
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

	data, err := readStdin()
	if err != nil {
		return fmt.Errorf("rotini: read stdin: %w", err)
	}
	if len(data) == 0 {
		return nil // nothing piped → leave Stdin nil
	}
	codec, ok := recon.DefaultCodecs().ByName(format)
	if !ok {
		return fmt.Errorf("rotini: unsupported stdin format %q", format)
	}
	m, err := codec.Decode(data)
	if err != nil {
		return fmt.Errorf("rotini: decode stdin (%s): %w", format, err)
	}

	// Validate the decoded payload against the command's stdin schema (when one was
	// generated for this <Prefix>Stdin type), before binding.
	if js := b.stdinSchemas[sf.Type().Elem().Name()]; js != "" {
		validator, err := recon.NewJSONSchemaValidator([]byte(js))
		if err != nil {
			return fmt.Errorf("rotini: stdin schema: %w", err)
		}
		if err := validator.Validate(m); err != nil {
			return fmt.Errorf("rotini: invalid stdin payload: %w", err)
		}
	}

	reg, err := recon.New(recon.WithSource(recon.NewMapSource("stdin", m)))
	if err != nil {
		return fmt.Errorf("rotini: stdin registry: %w", err)
	}
	defer reg.Close()

	ptr := reflect.New(sf.Type().Elem()) // *<Prefix>Stdin
	if err := reg.Bind(ptr.Interface()); err != nil {
		return fmt.Errorf("rotini: bind stdin: %w", err)
	}
	sf.Set(ptr)
	return nil
}

// readStdin returns the bytes piped or redirected to stdin, or nil when stdin is an
// interactive terminal (so binding a stdin channel never blocks waiting for input).
func readStdin() ([]byte, error) {
	return readPipedStdin()
}

// readPipedStdin returns the bytes piped or redirected to stdin, or nil when stdin
// is an interactive terminal (so it never blocks waiting for input).
func readPipedStdin() ([]byte, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil, nil // a terminal, not a pipe/redirect
	}
	return io.ReadAll(os.Stdin)
}

// reconcileFlags overrides each fallback flag (a Flags field carrying a recon tag)
// with its reconciled value: argv-set flags (highest) > env (SNAKE_UPPER of the recon
// key) > config files. A flag not present in any source keeps the value the Parser
// already bound (its explicit argv value or declared default). Argv-only flags (no
// recon tag) are untouched. Each reconciled value is also written back into store, so
// the deferred [validate] pass sees an env/config-supplied flag as present (satisfying
// a required check) and range-checks it against any enum.
func (b *Binder) reconcileFlags(v reflect.Value, chain []ResolvedCommand, argv []string, store *parsedInputs) error {
	if v.Kind() != reflect.Struct || !hasReconFlags(v) {
		return nil // no fallback flags → nothing to reconcile (env included)
	}
	offset := len(chain) - v.NumField()
	srcs := []recon.Source{recon.NewMapSource("flags", flagOverrides(v, chain, argv)), recon.NewOSEnvSource()}
	files, err := b.fileSources()
	if err != nil {
		return err
	}
	srcs = append(srcs, files...)
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return fmt.Errorf("rotini: flag registry: %w", err)
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
				return fmt.Errorf("rotini: reconcile flag %q: %w", key, err)
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
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
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
	if i := strings.IndexByte(tag, ','); i >= 0 {
		return tag[:i]
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
		if eq := strings.IndexByte(tok, '='); eq >= 0 {
			name = tok[:eq]
		}
		for _, id := range identifiers {
			if id == name {
				return true
			}
		}
	}
	return false
}

// configRegistry builds a recon registry over the configuration_files (for the
// pure Config channel), first (highest precedence) to last as declared.
func (b *Binder) configRegistry() (*recon.Registry, error) {
	srcs, err := b.fileSources()
	if err != nil {
		return nil, err
	}
	reg, err := recon.New(recon.WithSources(srcs...))
	if err != nil {
		return nil, fmt.Errorf("rotini: config registry: %w", err)
	}
	return reg, nil
}

// fileSources builds one recon file source per configuration_files entry, in
// declared (precedence) order. Missing files are tolerated; ~ is expanded.
func (b *Binder) fileSources() ([]recon.Source, error) {
	srcs := make([]recon.Source, 0, len(b.configFiles))
	for _, f := range b.configFiles {
		opts := []recon.FileOption{recon.WithOptional(true), recon.WithPathExpansion(true)}
		if f.Format != "" {
			opts = append(opts, recon.WithFileFormat(f.Format))
		}
		src, err := recon.NewFileSource(f.Path, opts...)
		if err != nil {
			return nil, fmt.Errorf("rotini: config source %q: %w", f.Name, err)
		}
		srcs = append(srcs, src)
	}
	return srcs, nil
}

// envExplicit collects the recon-key → explicit-env-var mapping from every Env
// field carrying an `env:"<VAR>"` tag (the spec's per-input `variable`), so the env
// source reads that exact variable instead of the SNAKE_UPPER default.
func envExplicit(v reflect.Value) map[string]string {
	m := map[string]string{}
	if v.Kind() != reflect.Struct {
		return m
	}
	for i := range v.NumField() {
		ci := v.Field(i)
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

// envTransform maps a recon key to its environment variable: an explicit `variable`
// when the input declared one, else recon's snake-upper projection.
func envTransform(explicit map[string]string) recon.KeyTransform {
	return func(p recon.Path) string {
		if v, ok := explicit[p.String()]; ok {
			return v
		}
		return recon.SnakeUpperTransform(p)
	}
}

// fillChannels walks a <Cmd>Inputs struct (one field per command on the resolved
// path) and, for each command's <Prefix>CommandInputs, recon-binds its Env struct
// from envReg and its Config struct from cfgReg via their recon tags.
func fillChannels(v reflect.Value, envReg, cfgReg *recon.Registry) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	for i := range v.NumField() {
		ci := v.Field(i)
		if ci.Kind() != reflect.Struct {
			continue
		}
		t := ci.Type()
		for j := range ci.NumField() {
			switch t.Field(j).Name {
			case "Env":
				if err := envReg.Bind(ci.Field(j).Addr().Interface()); err != nil {
					return fmt.Errorf("rotini: bind env: %w", err)
				}
			case "Config":
				if err := cfgReg.Bind(ci.Field(j).Addr().Interface()); err != nil {
					return fmt.Errorf("rotini: bind config: %w", err)
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
func validateChannels(v reflect.Value, envReg, cfgReg *recon.Registry) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	for i := range v.NumField() {
		ci := v.Field(i)
		if ci.Kind() != reflect.Struct {
			continue
		}
		t := ci.Type()
		for j := range ci.NumField() {
			switch t.Field(j).Name {
			case "Env":
				if err := validateChannelStruct(ci.Field(j), envReg); err != nil {
					return err
				}
			case "Config":
				if err := validateChannelStruct(ci.Field(j), cfgReg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateChannelStruct checks every constrained field of an Env/Config sub-struct
// against the value its registry resolved (when present).
func validateChannelStruct(s reflect.Value, reg *recon.Registry) error {
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
		val, found, err := reg.Get(key)
		if err != nil {
			return fmt.Errorf("rotini: read %q: %w", key, err)
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
// (min/max/minlen/maxlen/minitems/maxitems/pattern) into a [Constraints].
func channelConstraints(tag reflect.StructTag) (Constraints, bool) {
	var c Constraints
	has := false
	if v := tag.Get("min"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Minimum, has = f, true
		}
	}
	if v := tag.Get("max"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Maximum, has = f, true
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
