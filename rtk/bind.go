package rtk

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/rotini"
)

// Binder is the default multi-source input binder: it fills a command's typed
// inputs from argv (flags + positional arguments, via the embedded [Parser]) and
// from the non-argv channels — environment variables and configuration files —
// reconciled and decoded by recon. It is a service, bound like the parser:
//
//	// main.go
//	rth.Program.
//	    Bind("io", rtk.NewIO()).
//	    Bind("binder", rtk.NewBinder(rtg.BindMeta)).
//	    Execute()
//
//	// a handler
//	binder := rotini.MustGet[*rtk.Binder](rtx, "binder")
//	var in rtg.WidgetCreateInputs
//	if err := binder.Bind(rtx, &in); err != nil { /* handler owns it */ }
//
// Env values fill the generated <Prefix>Env struct (recon's env source maps a
// field's recon key to its SNAKE_UPPER form); config-file values fill <Prefix>Config
// from the document's configuration_files (carried in [rotini.BindMeta]). The two
// channels use independent registries, so an env var never leaks into a config field
// or vice versa. Flag fallback reconciliation and stdin are added in later phases.
type Binder struct {
	parser      *Parser
	configFiles []rotini.ConfigFile
}

// NewBinder returns the default binder, configured from the generated descriptor
// (the rtg package's BindMeta var) — primarily its configuration_files sources.
func NewBinder(meta rotini.BindMeta) *Binder {
	return &Binder{parser: NewParser(), configFiles: meta.ConfigFiles}
}

// Bind fills out — a non-nil pointer to the typed inputs struct rtg emits — from
// every wired channel: argv flags + positional arguments first (via the parser,
// including its required/enum validation), then the env and config channels via
// recon. It returns the first error (a usage error from argv parsing, or a recon
// bind/validation error for env/config).
func (b *Binder) Bind(rtx *rotini.Context, out any) error {
	if b == nil {
		return &usageError{msg: "rotini: nil binder"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &usageError{msg: "rotini: Bind out argument must be a non-nil pointer to an inputs struct"}
	}

	// 1. argv → Flags + Arguments (and required/enum validation).
	if err := b.parser.Parse(rtx, out); err != nil {
		return err
	}
	v := rv.Elem()

	// 2. flag fallback: for flags that declare a config key, reconcile
	//    argv-set > env (SNAKE_UPPER of the key) > config; otherwise keep the
	//    Parser's value (an explicit argv value or the flag's default).
	if err := b.reconcileFlags(v, rtx.Chain(), rtx.Args()); err != nil {
		return err
	}

	// 3. env + config → the Env/Config sub-structs, from independent registries
	//    (so an env var never leaks into a config field, or vice versa). The env
	//    source honors an input's explicit `variable` (else recon's SNAKE_UPPER).
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

	// 4. stdin → the leaf command's typed payload (decoded by its declared format).
	return fillStdin(v)
}

// fillStdin decodes piped stdin into the leaf command's Stdin payload field, when it
// declares one, using the format on its `stdin:"<format>"` tag. Stdin is a single
// stream, so only the leaf (the running command) consumes it; when nothing is piped
// the Stdin field is left nil.
func fillStdin(v reflect.Value) error {
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

	data, err := readPipedStdin()
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
// with its reconciled value: argv-set flags (highest) > env > config files. A flag
// not present in any source keeps the value the Parser already bound (its explicit
// argv value or declared default). Argv-only flags (no recon tag) are untouched.
//
// Note: the Parser's required-check runs at argv-parse, so a *required* flag is not
// yet satisfiable by env/config fallback (it must be on argv) — a later refinement.
func (b *Binder) reconcileFlags(v reflect.Value, chain []rotini.ResolvedCommand, argv []string) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	overrides := flagOverrides(v, chain, argv)
	if len(overrides) == 0 && len(b.configFiles) == 0 {
		return nil // nothing can change a flag's value
	}
	srcs := []recon.Source{recon.NewMapSource("flags", overrides), recon.NewOSEnvSource()}
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
			if found {
				s, _ := val.AsString()
				coerce(flags.Field(j), []string{s})
			}
		}
	}
	return nil
}

// flagOverrides maps the canonical key of every fallback flag explicitly set on
// argv to its (Parser-bound) value — the highest-precedence layer for reconciliation.
func flagOverrides(v reflect.Value, chain []rotini.ResolvedCommand, argv []string) map[string]any {
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

// findFlagDef finds a flag definition by its logical name.
func findFlagDef(defs []rotini.FlagDef, name string) (rotini.FlagDef, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return rotini.FlagDef{}, false
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
