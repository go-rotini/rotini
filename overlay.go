package rotini

import (
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// The à-la-carte input surface: per-channel acquisition, each returning a sparsely-populated
// [Layer] over the generated inputs type, and [OverlayInputs], which merges layers where slice
// order is precedence. [Collect] is the convenience path over the same machinery; these exist
// so a handler can acquire, inspect, reorder, or replace any channel individually:
//
//	defaults, _ := rotini.Defaults[MycliInputs](rtx)
//	files, _    := rotini.ParseFiles[MycliInputs](rtx)
//	env, _      := rotini.ParseEnv[MycliInputs](rtx)
//	argv, _     := rotini.ParseArgv[MycliInputs](rtx)
//	inputs, report := rotini.OverlayInputsP(defaults, files, env, argv)
//	if err := report.Validate(); err != nil { /* handler owns it */ }

// FieldPath identifies one leaf field of a generated inputs struct by its dot-joined Go field
// path, e.g. "RotiniGenerate.Flags.ConfFilePath". The channel parsers and the overlay derive
// it from the same type, so the two can never disagree.
type FieldPath string

// Provenance records which layer supplied a field's value and the raw text it supplied. Raw is
// pre-redacted for inputs the spec marks secret.
type Provenance struct {
	Layer string // the supplying layer's name: "defaults", "files", "env", "argv", "stdin", or custom
	Raw   string // the supplied text ("" when non-textual, e.g. a decoded stdin document); "[redacted]" for secrets
}

// Presence maps each field a layer actually supplied to its provenance. It is what makes
// overlay precedence real: a layer's absent fields are skipped, never copied.
type Presence map[FieldPath]Provenance

// Layer is one input channel's view of the inputs type T: the values it supplied — everything
// else is T's zero value — and exactly which fields those are. Layers from rotini's channel
// parsers also carry unexported data that [Report.Validate] uses; a hand-built Layer
// participates in overlay and provenance but contributes nothing to validation.
type Layer[T any] struct {
	Name   string
	Values T
	Set    Presence

	core *layerCore // validation data: nil for hand-built layers
}

// layerCore is the channel parsers' validation payload, in the same store shape the Parser
// validates, so a merged Report reuses the exact validation and error text of [Parser.Parse].
type layerCore struct {
	chain       []ResolvedCommand
	store       *parsedInputs
	argDefaults map[int]string // the defaults layer's per-index argument defaults (sparse)
}

// ── the one-liner ────────────────────────────────────────────────────────────.

// Collect is the typical handler's entire input story: every declared channel — argv,
// environment, configuration files, the stdin payload, defaults — acquired, reconciled in the
// standard precedence (defaults < files < env < argv), and validated, in one call:
//
//	inputs, err := rotini.Collect[MycliDeployInputs](rtx)
//
// **A handler collects the type generated for its own command, in any hook.** That is the whole
// rule. An inputs struct's last field describes the collecting command and the fields before it
// describe its ancestors, so Collect anchors the struct on [Context.Frame] — the command whose
// hook is running. A leaf's Run, a cascading hook three frames up, a composed child mounted
// under someone else's umbrella: same call, correct in each.
//
// It did not used to be one rule. The anchor was inferred from the struct's field count against
// the chain, which made the correct call depend on how deep THIS invocation happened to go: a
// cascading hook on a middle frame read a descendant's flags, and a composed child's cascading
// hook could not read its own flags at all. Both returned zeros with a nil error. A separate
// CollectRoot existed for part of the gap and is gone; [Binder.BindRoot] remains as the
// low-level escape for a caller that genuinely wants the first n frames.
//
// It is [Binder.Bind] under the hood, so errors are the same data-shaped [*ParseError]s and
// [*BindError]s. Use [CollectP] when "where did this value come from" matters.
func Collect[T any](rtx *Context) (T, error) {
	var t T
	err := binderFor(rtx).Bind(rtx, &t)
	return t, err
}

// CollectP is [Collect] with provenance: the same reconciled, validated inputs plus the
// [Report] answering Winner and History per field. It overlays the per-channel layers in the
// standard precedence, producing the same values Collect does at the cost of acquiring each
// channel separately. A validation failure returns the merged inputs and the report alongside
// the error, so a funnel can still say which layer supplied the offending value.
func CollectP[T any](rtx *Context) (T, Report, error) {
	var zero T
	defaults, err := Defaults[T](rtx)
	if err != nil {
		return zero, Report{}, err
	}
	files, err := ParseFiles[T](rtx)
	if err != nil {
		return zero, Report{}, err
	}
	env, err := ParseEnv[T](rtx)
	if err != nil {
		return zero, Report{}, err
	}
	argv, err := ParseArgv[T](rtx)
	if err != nil {
		return zero, Report{}, err
	}
	stdin, err := ParseStdin[T](rtx)
	if err != nil {
		return zero, Report{}, err
	}
	merged, report := OverlayInputsP(defaults, files, env, argv, stdin)
	if err := report.Validate(); err != nil {
		return merged, report, err
	}
	// Same frame-fit check Collect applies, after validation so a bad command line reports
	// itself first. See checkFrameFit.
	if err := checkFrameFit(reflect.ValueOf(&merged).Elem(), rtx.Chain(), rtx.frameIndex()); err != nil {
		return merged, report, err
	}
	return merged, report, nil
}

// ── channel acquisition ──────────────────────────────────────────────────────.

// ParseArgv parses the command line only — flags and positionals across the resolved chain,
// exactly as supplied: no defaults, no fallback, and no required or enum validation. Validate
// the overlaid result with [Report.Validate], so a required flag satisfied by another layer
// passes. Parse failures are [ParseError]s.
func ParseArgv[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := argvLayer(rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "argv", Values: t, Set: set, core: core}, err
}

// ParseEnv acquires the environment channel: every Env input, by its explicit variable or the
// SNAKE_UPPER projection of its key, plus the env fallback of any flag that declares a recon
// key. A missing required input or a constraint violation is this call's error.
func ParseEnv[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := envLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "env", Values: t, Set: set, core: core}, err
}

// ParseFiles acquires the configuration-files channel: every Config input from the BindMeta
// sources, declared order being precedence, plus the config fallback of any flag that declares
// a recon key. Required and constraint failures are this call's errors.
func ParseFiles[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := filesLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "files", Values: t, Set: set, core: core}, err
}

// ParseStdin acquires the stdin channel: the leaf command's typed payload, decoded per its
// declared format, schema-validated, and honoring required.
func ParseStdin[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := stdinLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "stdin", Values: t, Set: set, core: core}, err
}

// Defaults synthesizes the spec's declared defaults as an explicit layer — conventionally
// layer 0, which makes "no input at all" visible and testable. Flag and argument defaults come
// from the resolved chain; env and config defaults from their recon tags.
func Defaults[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := defaultsLayer(rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "defaults", Values: t, Set: set, core: core}, err
}

// ── overlay ──────────────────────────────────────────────────────────────────.

// OverlayInputs merges layers into one inputs value. Slice order is precedence, low → high: a
// field set by a later layer wins, a field no layer set stays the zero value, and an unset
// layer field never clobbers a lower layer's. Overlaying an empty layer is the identity.
func OverlayInputs[T any](layers ...Layer[T]) T {
	out, _ := OverlayInputsP(layers...)
	return out
}

// OverlayInputsP is [OverlayInputs] plus the merged [Report]: which layer won
// each field, the full per-field history, and Validate over the merged result.
func OverlayInputsP[T any](layers ...Layer[T]) (T, Report) {
	var out T
	dst := reflect.ValueOf(&out).Elem()
	rep := Report{set: Presence{}, history: map[FieldPath][]Provenance{}}

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

// Report is the merged provenance of one overlay: which layer won each field,
// every layer that set it (low → high), and validation over the merged values.
type Report struct {
	set     Presence
	history map[FieldPath][]Provenance
	chain   []ResolvedCommand
	store   *parsedInputs
	argDefs map[int]string
}

// Winner returns the provenance of the layer that supplied path's final value.
func (r Report) Winner(path FieldPath) (Provenance, bool) {
	p, ok := r.set[path]
	return p, ok
}

// History returns every layer that set path, low → high precedence — the last
// element is the winner.
func (r Report) History(path FieldPath) []Provenance {
	return r.history[path]
}

// Fields returns every field any layer set, sorted, for stable doctor-style
// output.
func (r Report) Fields() []FieldPath {
	return sortedPaths(r.set)
}

// Validate runs the same declarative checks [Parser.Parse] applies — required, enum,
// constraints, flag groups and dependencies — over the merged values, with "explicitly set"
// meaning set by the argv layer. Run it after the overlay so a required flag satisfied by any
// layer passes. Hand-built layers contribute values but nothing to validate.
func (r Report) Validate() error {
	if r.chain == nil || r.store == nil {
		return nil
	}
	if err := validate(r.chain, r.store); err != nil {
		return err
	}
	if err := validateFlagGroups(r.chain, r.store); err != nil {
		return err
	}
	return validateFlagDependencies(r.chain, r.store)
}

// absorb folds one layer's validation core into the report: later layers' flag values replace
// earlier ones scope by scope, the argv layer contributes positionals and the argv-set record,
// and the defaults layer contributes its sparse argument defaults.
func (r *Report) absorb(core *layerCore) {
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
func (r *Report) finalize() {
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

// argvSetAt returns frame idx's argv-set record, nil-safe.
func (p *parsedInputs) argvSetAt(idx int) map[string]bool {
	if p == nil || idx < 0 || idx >= len(p.argvSet) {
		return nil
	}
	return p.argvSet[idx]
}

// ── channel cores (shared, non-generic) ──────────────────────────────────────.

// argvLayer parses argv only (no defaults) into v and records presence.
func argvLayer(rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	store, err := parseArgvTokens(chain, rtx.Argv, rtx.Stdin)
	if err != nil {
		return nil, nil, err
	}
	anchor := frameAnchor(v, chain, rtx.frameIndex(), false)
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}

	set := Presence{}
	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		frame := chain[scope]
		si := store.scopes[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.Value) {
			vals, ok := si.flags[logical]
			if !ok {
				return
			}
			fd, _ := findFlagDef(frame.Flags, logical)
			set[fieldPath(topName, "Flags", fieldName)] = Provenance{
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

	// Flag defaults bind through the same store machinery as parsed values.
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	applyDefaults(chain, store)
	anchor := frameAnchor(v, chain, rtx.frameIndex(), false)
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}

	// Argument defaults are sparse (per index); record them for the overlay's
	// gap rule and set the corresponding fields directly.
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
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.Value) {
			if _, ok := store.scopes[scope].flags[logical]; !ok {
				return
			}
			fd, _ := findFlagDef(frame.Flags, logical)
			set[fieldPath(topName, "Flags", fieldName)] = Provenance{
				Layer: "defaults",
				Raw:   redactValue(fd.Default, fd.Secret),
			}
		})
		// Positional defaults: bind per-index into the Arguments struct.
		if scope == len(chain)-1 {
			idx := 0
			eachTaggedField(ci, "Arguments", func(fieldName, logical string, f reflect.Value) {
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
				set[fieldPath(topName, "Arguments", fieldName)] = Provenance{
					Layer: "defaults",
					Raw:   redactValue(d, secret),
				}
			})
		}
		// Env/Config defaults come from their recon `default=` tags.
		for _, channel := range []string{"Env", "Config"} {
			eachTaggedField(ci, channel, func(fieldName, logical string, f reflect.Value) {
				body := taggedFieldTag(ci, channel, fieldName).Get("recon")
				d := reconDefault(body)
				if d == "" {
					return
				}
				_ = coerce(f, []string{d})
				set[fieldPath(topName, channel, fieldName)] = Provenance{
					Layer: "defaults",
					Raw:   redactValue(d, reconHasSecret(body)),
				}
			})
		}
	})
	return set, &layerCore{chain: chain, store: store, argDefaults: argDefaults}, nil
}

// envLayer acquires the env channel (Env structs + flag env-fallbacks) into v.
func envLayer(b *Binder, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	envReg, err := recon.New(recon.WithSources(envSources(v, b.envPrefix)...))
	if err != nil {
		return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer envReg.Close()

	// Flag env-fallbacks read the plain env projection of the recon key, the
	// same source order reconcileFlags gives them.
	flagReg, err := recon.New(recon.WithSource(flagEnvSource(v, b.envPrefix)))
	if err != nil {
		return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer flagReg.Close()

	return channelLayer(v, chain, frameAnchor(v, chain, rtx.frameIndex(), false), "env", "Env", envReg, flagReg, nil)
}

// filesLayer acquires the config-files channel into v. config_source paths are honored here
// too: argv and env are re-read best-effort to learn where the files channel should look, and
// a malformed argv contributes nothing — [ParseArgv] owns reporting it.
func filesLayer(b *Binder, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[string]string{}
	if store, err := parseInto(chain, rtx.Argv, rtx.Stdin); err == nil {
		overrides = b.pathOverrides(chain, store)
	}
	cfg, err := b.configRegs(chain, overrides)
	if err != nil {
		return nil, nil, err
	}
	defer cfg.Close()
	return channelLayer(v, chain, frameAnchor(v, chain, rtx.frameIndex(), false), "files", "Config", cfg.merged, cfg.merged, cfg)
}

// channelLayer is the shared env/files core: recon-bind each command's channel struct,
// constraint-check the provided values, fill flag fallbacks, and record presence for
// everything the channel supplied.
func channelLayer(v reflect.Value, chain []ResolvedCommand, anchor int, layerName, structName string, reg, flagReg *recon.Registry, cfg *cfgRegs) (Presence, *layerCore, error) {
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
		recordFlagFallbacks(set, store, ci, chain, scope, topName, layerName, flagReg)
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
		return reconBind(channelForStruct(structName), err)
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

	eachTaggedField(ci, structName, func(fieldName, _ string, _ reflect.Value) {
		recordChannelField(set, ci, topName, structName, layerName, fieldName, reg, cfg, nested)
	})
	return nil
}

// recordChannelField records which layer supplied one channel field and its raw value,
// redacted when the input is secret. A pinned field is read from its own file's registry alone.
func recordChannelField(set Presence, ci reflect.Value, topName, structName, layerName, fieldName string, reg *recon.Registry, cfg *cfgRegs, nested map[string]bool) {
	tag := taggedFieldTag(ci, structName, fieldName)
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
		set[fieldPath(topName, structName, fieldName)] = Provenance{
			Layer: layerName,
			Raw:   redactValue("", reconHasSecret(body)),
		}
		return
	}
	if val, found, err := fieldReg.Get(key); err == nil && found {
		set[fieldPath(topName, structName, fieldName)] = Provenance{
			Layer: layerName,
			Raw:   redactValue(val.String(), reconHasSecret(body)),
		}
	}
}

// recordFlagFallbacks fills the flags that declare a recon key and records their provenance
// and raw text, so a later argv layer can still override them.
func recordFlagFallbacks(set Presence, store *parsedInputs, ci reflect.Value, chain []ResolvedCommand, scope int, topName, layerName string, flagReg *recon.Registry) {
	eachTaggedField(ci, "Flags", func(fieldName, logical string, f reflect.Value) {
		key := reconKey(taggedFieldTag(ci, "Flags", fieldName).Get("recon"))
		if key == "" {
			return
		}
		val, found, err := flagReg.Get(key)
		if err != nil || !found {
			return
		}
		s := val.String()
		_ = coerce(f, []string{s})
		fd, _ := findFlagDef(chain[scope].Flags, logical)
		set[fieldPath(topName, "Flags", fieldName)] = Provenance{
			Layer: layerName,
			Raw:   redactValue(s, fd.Secret),
		}
		if store.scopes[scope].flags == nil {
			store.scopes[scope].flags = map[string][]string{}
		}
		store.scopes[scope].flags[logical] = []string{s}
	})
}

// stdinLayer acquires the stdin channel into v.
func stdinLayer(b *Binder, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
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
				set[fieldPath(v.Type().Field(v.NumField()-1).Name, "Stdin")] = Provenance{Layer: "stdin"}
			}
		}
	}
	return set, &layerCore{chain: chain}, nil
}

// ── shared walking helpers ───────────────────────────────────────────────────.

// layerChain validates the context and returns its resolved chain — the same
// preconditions Parser.parseBind enforces.
func layerChain(rtx *Context) ([]ResolvedCommand, error) {
	if rtx == nil {
		return nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: parse on nil context"}
	}
	chain := rtx.Chain()
	if len(chain) == 0 {
		return nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: no command resolved for this context"}
	}
	return chain, nil
}

// walkCommandStructs visits each per-command CommandInputs field with its Go field name and
// chain scope index, aligned at the leaf exactly like bindInputs.
func walkCommandStructs(v reflect.Value, chain []ResolvedCommand, offset int, visit func(topName string, scope int, ci reflect.Value)) {
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
// (Flags/Arguments/Env/Config) of a CommandInputs value.
func eachTaggedField(ci reflect.Value, structName string, visit func(fieldName, logical string, f reflect.Value)) {
	s := ci.FieldByName(structName)
	if !s.IsValid() || s.Kind() != reflect.Struct {
		return
	}
	st := s.Type()
	for j := range s.NumField() {
		logical := st.Field(j).Tag.Get("rotini")
		if logical == "" {
			continue
		}
		visit(st.Field(j).Name, logical, s.Field(j))
	}
}

// taggedFieldTag returns the full struct tag of one channel sub-struct field.
func taggedFieldTag(ci reflect.Value, structName, fieldName string) reflect.StructTag {
	s := ci.FieldByName(structName)
	if !s.IsValid() || s.Kind() != reflect.Struct {
		return ""
	}
	f, ok := s.Type().FieldByName(fieldName)
	if !ok {
		return ""
	}
	return f.Tag
}

// argPresence records presence for the positional fields that received values, mirroring
// bindArgs' index mapping.
func argPresence(set Presence, layerName, topName string, ci reflect.Value, frame ResolvedCommand, args []string) {
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
		set[fieldPath(topName, "Arguments", fieldName)] = Provenance{
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
