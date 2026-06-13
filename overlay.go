package rotini

// This file is the à-la-carte input surface: per-channel acquisition
// (ParseArgv / ParseEnv / ParseFiles / ParseStdin / Defaults), each returning a
// sparsely-populated Layer over the same generated inputs type, and
// OverlayInputs, which merges layers where slice order IS precedence
// (low → high). The one-call Binder.Bind remains the convenience path over the
// same machinery; these functions exist so a handler can acquire, inspect,
// reorder, or replace any channel individually. Each derives its
// configuration from the Context's bound [BindMeta] (the generated NewProgram
// binds it under [KeyBindMeta]; a standalone Context binds its own, or none
// for a CLI without config files):
//
//	defaults, _ := rotini.Defaults[cmdgen.MycliInputs](rtx)
//	files, _    := rotini.ParseFiles[cmdgen.MycliInputs](rtx)
//	env, _      := rotini.ParseEnv[cmdgen.MycliInputs](rtx)
//	argv, _     := rotini.ParseArgv[cmdgen.MycliInputs](rtx)
//	inputs, report := rotini.OverlayInputsP(defaults, files, env, argv)
//	if err := report.Validate(); err != nil { /* handler owns it */ }

import (
	"reflect"
	"sort"
	"strings"

	"github.com/go-rotini/recon"
)

// FieldPath identifies one leaf field of a generated inputs struct by its
// dot-joined Go field path, e.g. "RotiniGenerate.Flags.ConfFilePath" — the
// names the user reads in their own generated code. Both the channel parsers
// (which record presence) and the overlay (which copies set fields) derive it
// from the same type, so the two sides can never disagree.
type FieldPath string

// Provenance records which layer supplied a field's value and the raw text it
// supplied. Raw is pre-redacted for inputs the spec marks secret — a Report can
// never leak what an error message must not.
type Provenance struct {
	Layer string // the supplying layer's name: "defaults", "files", "env", "argv", "stdin", or custom
	Raw   string // the supplied text ("" when non-textual, e.g. a decoded stdin document); "[redacted]" for secrets
}

// Presence maps each field a layer actually supplied to its provenance —
// the set-detection that makes overlay precedence real: a layer's zero-valued,
// absent fields are skipped, never copied.
type Presence map[FieldPath]Provenance

// Layer is one input channel's view of the inputs type T: the values it
// supplied (sparsely populated — everything else is T's zero value) and exactly
// which fields those are. Layers produced by rotini's channel parsers also
// carry unexported validation data ([Report.Validate] uses it); a hand-built
// Layer{Name, Values, Set} participates in overlay and provenance but
// contributes nothing to validation.
type Layer[T any] struct {
	Name   string
	Values T
	Set    Presence

	core *layerCore // validation data: nil for hand-built layers
}

// layerCore is the channel parsers' validation payload: the resolved chain and
// the raw string values per chain scope, in the same store shape the Parser
// validates, so a merged Report re-uses the exact validation (and error text)
// of Parser.Parse / Binder.Bind.
type layerCore struct {
	chain       []ResolvedCommand
	store       *parsedInputs
	argDefaults map[int]string // the defaults layer's per-index argument defaults (sparse)
}

// ── the one-liner ────────────────────────────────────────────────────────────

// Collect is the 95% handler's entire input story: every declared channel —
// argv, environment, configuration files (declared AND custom BindMeta
// sources), the stdin payload, defaults — acquired, reconciled in the standard
// precedence (defaults < files < env < argv), and validated, in one call:
//
//	inputs, err := rotini.Collect[cmdgen.MycliDeployInputs](rtx)
//
// Configuration comes from the Context's bound [BindMeta] ([KeyBindMeta] —
// the generated NewProgram binds it; a CLI with nothing to declare needs
// nothing). It is [Binder.Bind] under the hood; errors are the same
// data-shaped [*ParseError]s. When the answer to "where did this value come
// from" matters, use [CollectP].
func Collect[T any](rtx *Context) (T, error) {
	var t T
	err := binderFor(rtx).Bind(rtx, &t)
	return t, err
}

// CollectP is [Collect] with provenance: the same reconciled, validated
// inputs plus the [Report] that answers Winner/History per field. It rides
// the per-channel layer machinery ([Defaults], [ParseFiles], [ParseEnv],
// [ParseArgv], [ParseStdin]) overlaid in the standard precedence — the same
// values Collect produces (pinned by test), at the cost of acquiring each
// channel separately. Validation failures return the merged inputs AND the
// report alongside the error, so a funnel can still say which layer supplied
// the offending value.
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
	return merged, report, report.Validate()
}

// ── channel acquisition ──────────────────────────────────────────────────────

// ParseArgv parses the command line only — flags and positionals across the
// resolved chain, exactly as supplied: no defaults, no env/config fallback, and
// no required/enum validation (validate the overlaid result via
// [Report.Validate], so a required flag satisfied by another layer passes).
// Parse failures (unknown flag, missing value) are [ParseError]s.
func ParseArgv[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := argvLayer(rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "argv", Values: t, Set: set, core: core}, err
}

// ParseEnv acquires the environment channel: every Env input (explicit
// `variable:` or recon's SNAKE_UPPER projection of its key), plus the env
// fallback of any flag that declares a recon key. Channel-owned semantics stay
// in-channel, exactly as in [Binder.Bind]: a required env input missing or a
// constraint violation is this call's error.
func ParseEnv[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := envLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "env", Values: t, Set: set, core: core}, err
}

// ParseFiles acquires the configuration-files channel: every Config input from
// the BindMeta sources (declared order = precedence), plus the config fallback
// of any flag that declares a recon key. Channel-owned semantics (required,
// constraints) are this call's errors, exactly as in [Binder.Bind].
func ParseFiles[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := filesLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "files", Values: t, Set: set, core: core}, err
}

// ParseStdin acquires the stdin channel: the leaf command's typed payload,
// decoded per its declared format, schema-validated, and honoring required —
// exactly the [Binder.Bind] stdin step.
func ParseStdin[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := stdinLayer(binderFor(rtx), rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "stdin", Values: t, Set: set, core: core}, err
}

// Defaults synthesizes the spec's declared defaults as an explicit layer —
// conventionally layer 0 of the overlay, making "no input at all" visible and
// testable. Flag and argument defaults come from the resolved chain's
// Definition; env/config input defaults from their recon `default=` tags.
func Defaults[T any](rtx *Context) (Layer[T], error) {
	var t T
	set, core, err := defaultsLayer(rtx, reflect.ValueOf(&t).Elem())
	return Layer[T]{Name: "defaults", Values: t, Set: set, core: core}, err
}

// ── overlay ──────────────────────────────────────────────────────────────────

// OverlayInputs merges layers into one inputs value. Slice order is precedence,
// low → high: a field set by a later layer wins; a field no layer set stays the
// zero value; an unset layer field never clobbers a lower layer's value
// (skip-not-zero). Overlaying an empty layer is the identity.
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// copyFieldByPath copies the field at a dot-joined Go field path from src to
// dst (same type). Unknown segments are skipped — a stale path in a hand-built
// layer copies nothing rather than panicking.
func copyFieldByPath(dst, src reflect.Value, path FieldPath) {
	for _, seg := range strings.Split(string(path), ".") {
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

// ── the report ───────────────────────────────────────────────────────────────

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

// Validate runs the same declarative checks [Parser.Parse] applies — required,
// enum, constraints, flag groups, and flag dependencies — over the merged
// values, with group/dependency "explicitly set" meaning set by the argv layer.
// Run it after the overlay so a required flag satisfied by any layer passes.
// It validates the layers' channel data; hand-built layers contribute values
// but nothing to validate. A Report from no channel layers validates clean.
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

// absorb folds one layer's validation core into the report: later layers'
// flag values replace earlier ones scope-by-scope (matching the overlay), the
// argv layer contributes positionals and the argv-set record, and the defaults
// layer contributes its sparse argument defaults.
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

// finalize pads the merged positionals from the defaults layer's sparse
// argument defaults — exactly the gap rule the Parser applies: fill from the
// first unsupplied index, stopping at the first index without a default.
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

// ── channel cores (shared, non-generic) ──────────────────────────────────────

// argvLayer parses argv only (no defaults) into v and records presence.
func argvLayer(rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	store, err := parseArgvTokens(chain, rtx.Args, rtx.Stdin)
	if err != nil {
		return nil, nil, err
	}
	if err := bindInputs(v, store, chain); err != nil {
		return nil, nil, err
	}

	set := Presence{}
	walkCommandStructs(v, chain, func(topName string, scope int, ci reflect.Value) {
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
	if err := bindInputs(v, store, chain); err != nil {
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
	walkCommandStructs(v, chain, func(topName string, scope int, ci reflect.Value) {
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
	flagReg, err := recon.New(recon.WithSource(flagEnvSource(b.envPrefix)))
	if err != nil {
		return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
	}
	defer flagReg.Close()

	return channelLayer(v, chain, "env", "Env", envReg, flagReg, nil)
}

// filesLayer acquires the config-files channel (Config structs + flag
// config-fallbacks) into v. config_source paths are honored here too: the
// argv+env phase is re-read best-effort to learn where the files channel
// should look (a malformed argv contributes nothing — ParseArgv owns
// reporting it).
func filesLayer(b *Binder, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	overrides := map[string]string{}
	if store, err := parseInto(chain, rtx.Args, rtx.Stdin); err == nil {
		overrides = b.pathOverrides(chain, store)
	}
	cfg, err := b.configRegs(overrides)
	if err != nil {
		return nil, nil, err
	}
	defer cfg.Close()
	return channelLayer(v, chain, "files", "Config", cfg.merged, cfg.merged, cfg)
}

// channelLayer is the shared env/files core: recon-bind each command's channel
// struct (required + defaults are recon's contract), constraint-check provided
// values, fill flag fallbacks for recon-keyed flags, and record presence for
// everything the channel actually supplied.
func channelLayer(v reflect.Value, chain []ResolvedCommand, layerName, structName string, reg, flagReg *recon.Registry, cfg *cfgRegs) (Presence, *layerCore, error) {
	set := Presence{}
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	var bindErr error

	walkCommandStructs(v, chain, func(topName string, scope int, ci reflect.Value) {
		if bindErr != nil {
			return
		}
		// The channel struct itself.
		cs := ci.FieldByName(structName)
		if cs.IsValid() && cs.Kind() == reflect.Struct {
			if err := reg.Bind(cs.Addr().Interface()); err != nil {
				bindErr = reconBind(channelForStruct(structName), err)
				return
			}
			if err := validateChannelStruct(cs, reg, cfg); err != nil {
				bindErr = err
				return
			}
			if cfg != nil {
				if err := bindPinnedConfig(cs, cfg); err != nil {
					bindErr = err
					return
				}
			}
			// Nested env families (envnest) are filled directly — recon
			// resolves leaf keys only — and recorded as non-textual presence.
			nested := map[string]bool{}
			if structName == "Env" {
				var err error
				if nested, err = fillEnvNested(cs); err != nil {
					bindErr = err
					return
				}
			}
			eachTaggedField(ci, structName, func(fieldName, logical string, _ reflect.Value) {
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
			})
		}
		// Flag fallbacks: recon-keyed flags read this channel too.
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
	})
	if bindErr != nil {
		return nil, nil, bindErr
	}
	return set, &layerCore{chain: chain, store: store}, nil
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

// ── shared walking helpers ───────────────────────────────────────────────────

// layerChain validates the context and returns its resolved chain — the same
// preconditions Parser.parseBind enforces.
func layerChain(rtx *Context) ([]ResolvedCommand, error) {
	if rtx == nil {
		return nil, &ParseError{Msg: "rotini: parse on nil context"}
	}
	chain := rtx.Chain()
	if len(chain) == 0 {
		return nil, &ParseError{Msg: "rotini: no command resolved for this context"}
	}
	return chain, nil
}

// walkCommandStructs visits each per-command CommandInputs field of a generated
// inputs struct with its Go field name and chain scope index, aligned at the
// leaf exactly like bindInputs.
func walkCommandStructs(v reflect.Value, chain []ResolvedCommand, visit func(topName string, scope int, ci reflect.Value)) {
	if v.Kind() != reflect.Struct {
		return
	}
	offset := len(chain) - v.NumField()
	if offset < 0 {
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

// argPresence records presence for the positional-argument fields that
// received values, mirroring bindArgs' index mapping (a trailing []string
// absorbs the rest).
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
