package rotini

import (
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// The per-channel input surface: each acquisition method returns a sparse [InputLayer] over the
// generated inputs type, and [MergeInputs] merges layers with slice order as precedence.
// [Context.Inputs] is the convenience path over the same machinery; these let a handler
// acquire, inspect, reorder, or replace any channel individually:
//
//	defaults, _ := rtx.DefaultInputs[MycliInputs]()
//	files, _    := rtx.FileInputs[MycliInputs]()
//	env, _      := rtx.EnvInputs[MycliInputs]()
//	argv, _     := rtx.ArgvInputs[MycliInputs]()
//	inputs, report := rotini.MergeInputsWithReport(defaults, files, env, argv)
//	if err := report.Validate(); err != nil { /* handler owns it */ }

// FieldPath identifies one leaf field of a generated inputs struct by its dot-joined Go field
// path, e.g. "RotiniGenerate.Flags.ConfFilePath".
type FieldPath string

// InputSource records which layer supplied a field's value and the raw text it supplied. Raw is
// pre-redacted for inputs the spec marks secret.
type InputSource struct {
	Layer string // the supplying layer's name: "defaults", "files", "env", "argv", "stdin", or custom
	Raw   string // the supplied text, a list's values joined with ", " whichever layer supplied it ("" when non-textual, e.g. a decoded stdin document); "[redacted]" for secrets
}

// Presence maps each field a layer supplied to its provenance. Overlay copies only these
// fields, so a layer's absent fields never overwrite a lower layer's values.
type Presence map[FieldPath]InputSource

// InputLayer is one input channel's view of the inputs type T: the values it supplied (all
// other fields are zero) and which fields those are. Layers from rotini's channel
// parsers also carry unexported data that [InputReport.Validate] uses; a hand-built InputLayer
// participates in overlay and provenance but contributes nothing to validation. A nil or empty
// Set means the layer supplied nothing: overlaying it leaves every field as it was.
type InputLayer[T any] struct {
	Name   string
	Values T
	Set    Presence

	core *layerCore // validation data: nil for hand-built layers
}

// layerCore is the channel parsers' validation payload, in the same store shape the Parser
// validates, so a merged [InputReport] reuses the validation and error text of [Parser.Parse].
type layerCore struct {
	chain       []Command
	store       *parsedInputs
	argDefaults map[int]string // the defaults layer's per-index argument defaults (sparse)
}

// ── the one-liner ────────────────────────────────────────────────────────────.

// Inputs acquires every declared channel (argv, environment, configuration files, the stdin
// payload, defaults), reconciles them in the standard precedence defaults < files < env < argv,
// validates the result, and returns it:
//
//	inputs, err := rtx.Inputs[MycliDeployInputs]()
//
// Stdin is outside that order because it never competes: it fills only the leaf command's
// payload field, which no other channel writes.
//
// T must be the inputs type generated for the command whose hook is running ([Context.Command]),
// in any hook. Its last field describes that command and the preceding fields its ancestors, so
// the struct is anchored on the running command regardless of how deep the invocation went. A
// type that cannot be anchored there is an error, not a silent zero value.
//
// Inputs delegates to [InputReader.Read]; errors are [*ParseError] and [*InputError] values. On
// error the returned T is partially filled and must not be used. Inputs stops at the first argv
// error, before the environment and configuration channels are read; use
// [Context.InputsWithReport] for a merged value and per-field provenance alongside the error.
func (rtx *Context) Inputs[T any]() (T, error) {
	var t T
	err := readerFor(rtx).Read(rtx, &t)
	return t, err
}

// InputsWithReport is [Context.Inputs] with provenance: the same reconciled, validated inputs
// plus an [InputReport] giving each field's Winner and History. It acquires each channel
// separately and overlays the layers in the standard precedence. An acquisition error returns
// the zero T; a validation error returns the merged inputs and report alongside it, so the
// caller can see which layer supplied the offending value.
func (rtx *Context) InputsWithReport[T any]() (T, InputReport, error) {
	var zero T
	defaults, err := rtx.DefaultInputs[T]()
	if err != nil {
		return zero, InputReport{}, err
	}
	files, err := rtx.FileInputs[T]()
	if err != nil {
		return zero, InputReport{}, err
	}
	env, err := rtx.EnvInputs[T]()
	if err != nil {
		return zero, InputReport{}, err
	}
	argv, err := rtx.ArgvInputs[T]()
	if err != nil {
		return zero, InputReport{}, err
	}
	stdin, err := rtx.StdinInputs[T]()
	if err != nil {
		return zero, InputReport{}, err
	}
	merged, report := MergeInputsWithReport(defaults, files, env, argv, stdin)
	// No fit check needed: every layer above already ran layerAnchor over the same type and chain.
	if err := report.Validate(); err != nil {
		return merged, report, err
	}
	return merged, report, nil
}

// ── channel acquisition ──────────────────────────────────────────────────────.

// ArgvInputs parses the command line only — flags and positionals across the resolved chain,
// exactly as supplied: no defaults, no fallback, and no required or enum validation. Validate
// the overlaid result with [InputReport.Validate], so a required flag satisfied by another layer
// passes. Parse failures are [*ParseError] values.
func (rtx *Context) ArgvInputs[T any]() (InputLayer[T], error) {
	var t T
	set, core, err := argvLayer(rtx, reflect.ValueOf(&t).Elem())
	return InputLayer[T]{Name: "argv", Values: t, Set: set, core: core}, err
}

// EnvInputs acquires the environment channel: every Env input, by its explicit variable or the
// SNAKE_UPPER projection of its key, plus the env fallback of any flag that declares a recon
// key. A missing required input or a constraint violation is this call's error.
func (rtx *Context) EnvInputs[T any]() (InputLayer[T], error) {
	var t T
	set, core, err := envLayer(readerFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return InputLayer[T]{Name: "env", Values: t, Set: set, core: core}, err
}

// FileInputs acquires the configuration-files channel: every Config input from the InputSettings
// sources, declared order being precedence, plus the config fallback of any flag that declares
// a recon key. Required and constraint failures are this call's errors.
func (rtx *Context) FileInputs[T any]() (InputLayer[T], error) {
	var t T
	set, core, err := filesLayer(readerFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return InputLayer[T]{Name: "files", Values: t, Set: set, core: core}, err
}

// StdinInputs acquires the stdin channel: the leaf command's typed payload, decoded per its
// declared format, schema-validated, and honoring required.
func (rtx *Context) StdinInputs[T any]() (InputLayer[T], error) {
	var t T
	set, core, err := stdinLayer(readerFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return InputLayer[T]{Name: "stdin", Values: t, Set: set, core: core}, err
}

// DefaultInputs synthesizes the spec's declared defaults as an explicit layer, conventionally
// the lowest. Flag and argument defaults come from the resolved chain; env and config defaults
// from their recon tags.
func (rtx *Context) DefaultInputs[T any]() (InputLayer[T], error) {
	var t T
	set, core, err := defaultsLayer(rtx, reflect.ValueOf(&t).Elem())
	return InputLayer[T]{Name: "defaults", Values: t, Set: set, core: core}, err
}

// ── overlay ──────────────────────────────────────────────────────────────────.

// MergeInputs merges layers into one inputs value. Slice order is precedence, low → high: a
// field set by a later layer wins, a field no layer set stays the zero value, and an unset
// layer field never clobbers a lower layer's. Overlaying an empty layer is the identity.
func MergeInputs[T any](layers ...InputLayer[T]) T {
	out, _ := MergeInputsWithReport(layers...)
	return out
}

// MergeInputsWithReport is [MergeInputs] plus the merged [InputReport]: which layer won
// each field, the full per-field history, and Validate over the merged result.
func MergeInputsWithReport[T any](layers ...InputLayer[T]) (T, InputReport) {
	var out T
	dst := reflect.ValueOf(&out).Elem()
	rep := InputReport{set: Presence{}, history: map[FieldPath][]InputSource{}}

	for _, l := range layers {
		src := reflect.ValueOf(l.Values)
		for _, path := range sortedPaths(l.Set) {
			copyFieldByPath(dst, src, path)
			prov := l.Set[path]
			rep.set[path] = prov
			rep.history[path] = append(rep.history[path], prov)
		}
		rep.absorb(l.core)
	}
	rep.finalize()
	return out, rep
}

// sortedPaths returns a Presence's keys in deterministic order.
func sortedPaths(p Presence) []FieldPath {
	out := make([]FieldPath, 0, len(p))
	for k := range p {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// copyFieldByPath copies the field at a dot-joined path from src to dst. Unknown segments are
// skipped, so a stale path in a hand-built layer copies nothing rather than panicking.
func copyFieldByPath(dst, src reflect.Value, path FieldPath) {
	for seg := range strings.SplitSeq(string(path), ".") {
		if dst.Kind() != reflect.Struct || src.Kind() != reflect.Struct {
			return
		}
		dst, src = dst.FieldByName(seg), src.FieldByName(seg)
		if !dst.IsValid() || !src.IsValid() {
			return
		}
	}
	if dst.CanSet() {
		dst.Set(src)
	}
}

// ── the report ───────────────────────────────────────────────────────────────.

// InputReport is the merged provenance of one overlay: which layer won each field, every layer
// that set it (low → high), and validation over the merged values. The zero InputReport reports
// no fields, and its Validate returns nil.
type InputReport struct {
	set     Presence
	history map[FieldPath][]InputSource
	chain   []Command
	store   *parsedInputs
	argDefs map[int]string
}

// Winner returns the provenance of the layer that supplied path's final value.
func (r InputReport) Winner(path FieldPath) (InputSource, bool) {
	p, ok := r.set[path]
	return p, ok
}

// History returns every layer that set path, low → high precedence; the last element is the
// winner.
func (r InputReport) History(path FieldPath) []InputSource {
	return r.history[path]
}

// Fields returns every field any layer set, sorted.
func (r InputReport) Fields() []FieldPath {
	return sortedPaths(r.set)
}

// Validate runs the same declarative checks [Parser.Parse] applies — required, enum,
// constraints, flag groups and dependencies — with "explicitly set" meaning set by the argv
// layer. Run it after the overlay so a required flag satisfied by any layer passes.
//
// It checks what the layers supplied, not the merged struct field by field:
//
//   - Presence rules fire on absence: a required input no layer supplied is an error.
//   - Value rules fire only on a supplied value: an unsupplied field's zero value is not checked
//     against its enum or bounds.
//
// A merge that omits [Context.DefaultInputs] can therefore yield an enum-constrained flag as ""
// without error. Hand-built layers contribute values but nothing to validate.
func (r InputReport) Validate() error {
	if r.chain == nil || r.store == nil {
		return nil
	}
	return validateStore(r.chain, r.store)
}

// absorb folds one layer's validation core into the report: later layers' flag values replace
// earlier ones scope by scope, the argv layer contributes positionals and the argv-set record,
// and the defaults layer contributes its sparse argument defaults.
func (r *InputReport) absorb(core *layerCore) {
	if core == nil {
		return
	}
	if r.chain == nil {
		r.chain = core.chain
		r.store = &parsedInputs{
			scopes:  make([]scopeInputs, len(core.chain)),
			argvSet: make([]map[string]bool, len(core.chain)),
		}
	}
	if core.argDefaults != nil {
		r.argDefs = core.argDefaults
	}
	if core.store == nil || len(core.store.scopes) != len(r.store.scopes) {
		return
	}
	for i := range core.store.scopes {
		for name, vals := range core.store.scopes[i].flags {
			if r.store.scopes[i].flags == nil {
				r.store.scopes[i].flags = map[string][]string{}
			}
			r.store.scopes[i].flags[name] = vals
		}
		if len(core.store.scopes[i].args) > 0 {
			r.store.scopes[i].args = core.store.scopes[i].args
		}
		for name := range core.store.argvSetAt(i) {
			if r.store.argvSet[i] == nil {
				r.store.argvSet[i] = map[string]bool{}
			}
			r.store.argvSet[i][name] = true
		}
	}
}

// finalize pads the merged positionals from the defaults layer, applying the Parser's gap
// rule: fill from the first unsupplied index, stopping at the first index without a default.
func (r *InputReport) finalize() {
	if r.store == nil || r.argDefs == nil || len(r.chain) == 0 {
		return
	}
	leaf := len(r.chain) - 1
	for i := len(r.store.scopes[leaf].args); ; i++ {
		d, ok := r.argDefs[i]
		if !ok {
			break
		}
		r.store.scopes[leaf].args = append(r.store.scopes[leaf].args, d)
	}
}

// argvSetAt returns the argv-set record at chain index idx, or nil when p is nil or idx is out
// of range.
func (p *parsedInputs) argvSetAt(idx int) map[string]bool {
	if p == nil || idx < 0 || idx >= len(p.argvSet) {
		return nil
	}
	return p.argvSet[idx]
}

// ── channel cores (shared, non-generic) ──────────────────────────────────────.

// layerAnchor is checkFrameFit plus frameAnchor, so no layer can accept a struct that does not
// describe the running command and return it zeroed with a nil error. Callers invoke it after
// any parse step, so a malformed command line reports itself first.
func layerAnchor(rtx *Context, v reflect.Value, chain []Command) (int, error) {
	self := rtx.frameIndex()
	if err := checkFrameFit(v, chain, self); err != nil {
		return 0, err
	}
	return frameAnchor(v, chain, self), nil
}

// argvLayer parses argv only (no defaults) into v and records presence.
func argvLayer(rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	store, err := parseArgvTokens(chain, rtx.Argv, rtx.flagStdin())
	if err != nil {
		return nil, nil, err
	}
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}

	set := Presence{}
	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		frame := chain[scope]
		si := store.scopes[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			vals, ok := si.flags[logical]
			if !ok {
				return
			}
			fd, _ := findFlagDef(frame.Flags, logical)
			set[fieldPath(topName, "Flags", fieldName)] = InputSource{
				Layer: "argv",
				Raw:   redactValue(strings.Join(vals, ", "), fd.Secret),
			}
		})
		argPresence(set, "argv", topName, ci, frame, si.args)
	})
	return set, &layerCore{chain: chain, store: store}, nil
}

// defaultsLayer synthesizes declared defaults into v and records presence.
func defaultsLayer(rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}

	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	applyDefaults(chain, store)
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}

	// Argument defaults are sparse (per index); they are recorded for the overlay's gap rule
	// and bound into their fields directly below.
	leaf := chain[len(chain)-1]
	argDefaults := map[int]string{}
	for i, ad := range leaf.Arguments {
		if ad.Default != "" {
			argDefaults[i] = ad.Default
		}
	}

	set := Presence{}
	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		frame := chain[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			if _, ok := store.scopes[scope].flags[logical]; !ok {
				return
			}
			fd, _ := findFlagDef(frame.Flags, logical)
			set[fieldPath(topName, "Flags", fieldName)] = InputSource{
				Layer: "defaults",
				Raw:   redactValue(fd.Default, fd.Secret),
			}
		})
		if scope == len(chain)-1 {
			idx := 0
			eachTaggedField(ci, "Arguments", func(fieldName, _ string, _ reflect.StructTag, f reflect.Value) {
				i := idx
				idx++
				d, ok := argDefaults[i]
				if !ok {
					return
				}
				_ = coerce(f, []string{d})
				var secret bool
				if i < len(frame.Arguments) {
					secret = frame.Arguments[i].Secret
				}
				set[fieldPath(topName, "Arguments", fieldName)] = InputSource{
					Layer: "defaults",
					Raw:   redactValue(d, secret),
				}
			})
		}
		// Env/Config defaults come from their recon `default=` tags.
		for _, channel := range []string{"Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, _ string, tag reflect.StructTag, f reflect.Value) {
				body := tag.Get("recon")
				d := reconDefault(body)
				if d == "" {
					return
				}
				_ = coerce(f, []string{d})
				set[fieldPath(topName, channel, fieldName)] = InputSource{
					Layer: "defaults",
					Raw:   redactValue(d, reconHasSecret(body)),
				}
			})
		}
	})
	return set, &layerCore{chain: chain, store: store, argDefaults: argDefaults}, nil
}

// envLayer acquires the env channel (Env structs + flag env-fallbacks) into v.
func envLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	envReg, err := recon.New(recon.WithSources(envSources(v, b.envPrefix)...))
	if err != nil {
		return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer envReg.Close()

	// Flag env fallbacks read the env projection of the recon key, as in reconcileFlags.
	flagReg, err := recon.New(recon.WithSource(flagEnvSource(v, b.envPrefix)))
	if err != nil {
		return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer flagReg.Close()

	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	return channelLayer(v, chain, anchor, "env", "Env", envReg, flagReg, nil)
}

// filesLayer acquires the config-files channel into v. config_source paths are honored here
// too: argv and env are re-read best-effort to learn where the files channel should look, and
// a malformed argv contributes nothing — [Context.ArgvInputs] owns reporting it.
func filesLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[string]string{}
	if store, err := parseInto(chain, rtx.Argv, rtx.flagStdin()); err == nil {
		overrides = b.pathOverrides(chain, store)
	}
	cfg, err := b.configRegs(chain, overrides, v)
	if err != nil {
		return nil, nil, err
	}
	defer cfg.Close()
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	return channelLayer(v, chain, anchor, "files", "Config", cfg.merged, cfg.merged, cfg)
}

// channelLayer is the shared env/files core: recon-bind each command's channel struct,
// constraint-check the provided values, fill flag fallbacks, and record presence for
// everything the channel supplied.
func channelLayer(v reflect.Value, chain []Command, anchor int, layerName, structName string, reg, flagReg *recon.Registry, cfg *cfgRegs) (Presence, *layerCore, error) {
	set := Presence{}
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	var bindErr error

	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		if bindErr != nil {
			return
		}
		if err := fillChannelStruct(set, ci, topName, structName, layerName, reg, cfg); err != nil {
			bindErr = err
			return
		}
		if err := recordFlagFallbacks(set, store, ci, chain, scope, topName, layerName, flagReg); err != nil {
			bindErr = err
		}
	})
	if bindErr != nil {
		return nil, nil, bindErr
	}
	return set, &layerCore{chain: chain, store: store}, nil
}

// fillChannelStruct binds one command's channel struct from the registry, validates it, and
// records where each field's value came from.
func fillChannelStruct(set Presence, ci reflect.Value, topName, structName, layerName string, reg *recon.Registry, cfg *cfgRegs) error {
	cs := ci.FieldByName(structName)
	if !cs.IsValid() || cs.Kind() != reflect.Struct {
		return nil
	}
	if err := reg.Bind(cs.Addr().Interface()); err != nil {
		return reconBind(channelOf(cfg), err)
	}
	if err := validateChannelStruct(cs, reg, cfg); err != nil {
		return err
	}
	if cfg != nil {
		if err := bindPinnedConfig(cs, cfg); err != nil {
			return err
		}
	}

	// Nested env families (envnest) are filled directly — recon resolves leaf keys
	// only — and recorded as non-textual presence.
	nested := map[string]bool{}
	if structName == "Env" {
		var err error
		if nested, err = fillEnvNested(cs); err != nil {
			return err
		}
	}

	eachTaggedField(ci, structName, func(fieldName, _ string, tag reflect.StructTag, _ reflect.Value) {
		recordChannelField(set, tag, topName, structName, layerName, fieldName, reg, cfg, nested)
	})
	return nil
}

// recordChannelField records which layer supplied one channel field and its raw value,
// redacted when the input is secret. A pinned field is read from its own file's registry alone.
func recordChannelField(set Presence, tag reflect.StructTag, topName, structName, layerName, fieldName string, reg *recon.Registry, cfg *cfgRegs, nested map[string]bool) {
	body := tag.Get("recon")
	key := reconKey(body)
	if key == "" {
		return
	}

	fieldReg := reg
	if pin := tag.Get("cfgfile"); pin != "" && cfg != nil {
		if pinned, err := cfg.For(pin); err == nil {
			fieldReg = pinned
		}
	}

	// A nested family was filled directly, so it has no single textual value.
	if nested[key] {
		set[fieldPath(topName, structName, fieldName)] = InputSource{
			Layer: layerName,
			Raw:   redactValue("", reconHasSecret(body)),
		}
		return
	}
	if val, found, err := fieldReg.Get(key); err == nil && found {
		set[fieldPath(topName, structName, fieldName)] = InputSource{
			Layer: layerName,
			Raw:   redactValue(val.String(), reconHasSecret(body)),
		}
	}
}

// recordFlagFallbacks fills the flags that declare a recon key and records their provenance
// and raw text, so a later argv layer can still override them.
func recordFlagFallbacks(set Presence, store *parsedInputs, ci reflect.Value, chain []Command, scope int, topName, layerName string, flagReg *recon.Registry) error {
	var bindErr error
	eachTaggedField(ci, "Flags", func(fieldName, logical string, tag reflect.StructTag, f reflect.Value) {
		if bindErr != nil {
			return
		}
		key := reconKey(tag.Get("recon"))
		if key == "" {
			return
		}
		fd, _ := findFlagDef(chain[scope].Flags, logical)
		vals, err := bindFlagFallback(flagReg, f, tag, key, fd, chain, scope)
		if err != nil || vals == nil {
			bindErr = err
			return
		}
		set[fieldPath(topName, "Flags", fieldName)] = InputSource{
			Layer: layerName,
			Raw:   redactValue(strings.Join(vals, ", "), fd.Secret),
		}
		if store.scopes[scope].flags == nil {
			store.scopes[scope].flags = map[string][]string{}
		}
		store.scopes[scope].flags[logical] = vals
	})
	return bindErr
}

// stdinLayer acquires the stdin channel into v.
func stdinLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	if err := b.fillStdin(rtx, v); err != nil {
		return nil, nil, err
	}
	set := Presence{}
	if v.Kind() == reflect.Struct && v.NumField() > 0 {
		leaf := v.Field(v.NumField() - 1)
		if leaf.Kind() == reflect.Struct {
			if sf := leaf.FieldByName("Stdin"); sf.IsValid() && sf.Kind() == reflect.Pointer && !sf.IsNil() {
				set[fieldPath(v.Type().Field(v.NumField()-1).Name, "Stdin")] = InputSource{Layer: "stdin"}
			}
		}
	}
	return set, &layerCore{chain: chain}, nil
}

// ── shared walking helpers ───────────────────────────────────────────────────.

// layerChain validates the context and returns its resolved chain, enforcing the same
// preconditions as Parser.parseBind.
func layerChain(rtx *Context) ([]Command, error) {
	if rtx == nil {
		return nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: parse on nil context"}
	}
	chain := rtx.CommandChain()
	if len(chain) == 0 {
		return nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: no command resolved for this context"}
	}
	return chain, nil
}

// walkCommandStructs visits each per-command CommandInputs field with its Go field name and
// chain index, the struct's first field sitting at chain index offset (the anchor frameAnchor
// computes).
func walkCommandStructs(v reflect.Value, chain []Command, offset int, visit func(topName string, scope int, ci reflect.Value)) {
	if v.Kind() != reflect.Struct {
		return
	}
	if offset < 0 || offset+v.NumField() > len(chain) {
		return
	}
	for i := range v.NumField() {
		ci := v.Field(i)
		if ci.Kind() != reflect.Struct {
			continue
		}
		visit(v.Type().Field(i).Name, offset+i, ci)
	}
}

// eachTaggedField visits each rotini-tagged field of one channel sub-struct
// (Flags/Arguments/Env/Config) of a CommandInputs value, with its full struct tag.
func eachTaggedField(ci reflect.Value, structName string, visit func(fieldName, logical string, tag reflect.StructTag, f reflect.Value)) {
	s := ci.FieldByName(structName)
	if !s.IsValid() || s.Kind() != reflect.Struct {
		return
	}
	st := s.Type()
	for j := range s.NumField() {
		sf := st.Field(j)
		logical := sf.Tag.Get("rotini")
		if logical == "" {
			continue
		}
		visit(sf.Name, logical, sf.Tag, s.Field(j))
	}
}

// argPresence records presence for the positional fields that received values, mirroring
// bindArgs' index mapping.
func argPresence(set Presence, layerName, topName string, ci reflect.Value, frame Command, args []string) {
	if len(args) == 0 {
		return
	}
	s := ci.FieldByName("Arguments")
	if !s.IsValid() || s.Kind() != reflect.Struct {
		return
	}
	st := s.Type()
	idx := 0
	for j := range s.NumField() {
		fieldName := st.Field(j).Name
		f := s.Field(j)
		variadic := f.Kind() == reflect.Slice // a slice argument is variadic, whatever its element type
		var vals []string
		switch {
		case variadic:
			vals = args[min(idx, len(args)):]
			idx = len(args)
		case idx < len(args):
			vals = args[idx : idx+1]
			idx++
		default:
			continue
		}
		if len(vals) == 0 {
			continue
		}
		var secret bool
		if j < len(frame.Arguments) {
			secret = frame.Arguments[j].Secret
		}
		set[fieldPath(topName, "Arguments", fieldName)] = InputSource{
			Layer: layerName,
			Raw:   redactValue(strings.Join(vals, ", "), secret),
		}
	}
}

// fieldPath joins Go field names into a FieldPath.
func fieldPath(parts ...string) FieldPath {
	return FieldPath(strings.Join(parts, "."))
}

// reconDefault extracts the `default=` option from a recon struct-tag body.
func reconDefault(body string) string {
	if body == "" {
		return ""
	}
	for _, opt := range strings.Split(body, ",")[1:] {
		if v, ok := strings.CutPrefix(strings.TrimSpace(opt), "default="); ok {
			return v
		}
	}
	return ""
}
