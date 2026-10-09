package rotini

import (
	"maps"
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
// parsers also carry unexported data that [InputReport.Validate] uses. A hand-built InputLayer
// (one the program builds from its own source, such as a prompt or a secrets service)
// participates in overlay, provenance and validation: the fields its Set names count as
// supplied, and their values are checked the way [Context.CheckInputs] checks them. A nil or
// empty Set means the layer supplied nothing: overlaying it leaves every field as it was.
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
	argDefaults map[int]string // the per-index argument values a layer supplies past argv: defaults or fallbacks (sparse)
	// anchor is the chain index of the inputs type's first field (see frameAnchor), so a merged
	// report can check hand-built values against the right commands; anchored says it is set.
	anchor   int
	anchored bool
	// stdinSchemas are the program's stdin payload schemas, so a merged report can check a
	// hand-built stdin document as Context.CheckInputs does.
	stdinSchemas map[string]string
	// view is the run's environment and directory, which a merged report checks hand-built
	// values against.
	view *osView
}

// ── the one-liner ────────────────────────────────────────────────────────────.

// Inputs acquires every declared channel (argv, environment, configuration files, the stdin
// payload, defaults), reconciles them in the standard precedence defaults < files < env < argv,
// validates the result, and returns it. When a short-circuit flag ([FlagDef.ShortCircuit]) is
// set on the command line, the declared requirements are waived, so the handler gets the
// values read so far and can act on the flag. A command line or environment value that can't be
// read is still an error; a configuration file that can't be read is skipped for that call.
//
//	inputs, err := rtx.Inputs[MycliDeployInputs]()
//
// Stdin is outside that order because it never competes: it fills only the leaf command's
// payload field, which no other channel writes. It is read once per run and held in memory, so
// a later call binds the same payload; a streamed stdin is an iterator over the run's one
// stream instead. It is not read at all, and the field stays nil, under a short-circuit flag or
// when the command's `unless_argument` file was given. A read of piped stdin ends when the run
// is canceled (by a trapped signal, for one), with an [*InputError] whose cause is the
// cancellation's.
//
// T must be the inputs type generated for the command whose hook is running ([Context.Command]),
// in any hook. Its last field describes that command and the preceding fields its ancestors, so
// the struct is anchored on the running command regardless of how deep the invocation went. A
// type that cannot be anchored there is an error, not a silent zero value.
//
// Inputs delegates to [InputReader.Read]; errors are [*ParseError] and [*InputError] values, or
// a [*WiringError] when a command declares config inputs and the program has no
// [InputSettings]. On error the returned T is partially filled and must not be used. A command
// line that doesn't parse is reported before any other channel is read. Use
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
// each field, the full per-field history, and Validate over the merged result. A field a
// hand-built [InputLayer] won is validated by the value it supplied, as [Context.CheckInputs]
// checks it.
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
			// Track which kind of layer won each field: a later rotini layer takes it back.
			if l.core == nil {
				if rep.handBuilt == nil {
					rep.handBuilt = map[FieldPath]bool{}
				}
				rep.handBuilt[path] = true
			} else {
				delete(rep.handBuilt, path)
			}
		}
		rep.absorb(l.core)
	}
	rep.finalize()
	rep.merged = reflect.ValueOf(out)
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

	// merged is the overlay's result, and handBuilt the fields whose final value came from a
	// hand-built layer; Validate checks those values the way CheckInputs does.
	merged    reflect.Value
	handBuilt map[FieldPath]bool
	anchor    int // chain index of the inputs type's first field, from a rotini layer
	anchored  bool
	// stdinSchemas check a hand-built stdin document; nil when no rotini layer supplied them.
	stdinSchemas map[string]string
	view         *osView // the run's environment and directory, from a rotini layer
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
//     against its enum or bounds. Item counts are the exception, as on the command line: a list
//     flag or variadic argument nobody supplied has zero items, so its minItems applies unless
//     it has a default. Environment and config lists are counted only when supplied.
//
// A merge that omits [Context.DefaultInputs] can therefore yield an enum-constrained flag as ""
// without error.
//
// A hand-built layer's fields count as supplied (a required input it supplies passes, and a
// flag it supplies counts as set for flag groups and dependencies), and the values it won are
// checked as [Context.CheckInputs] checks them, naming each input by its canonical spelling.
//
// A merge of hand-built layers alone has no command to check against, so Validate reports a
// [ParseKindInternal] error pointing at [Context.CheckInputs], which takes the command from
// the running context.
//
// A streamed stdin field is carried, not checked: its items are checked as they are read.
func (r InputReport) Validate() error {
	if r.chain == nil || r.store == nil {
		if len(r.handBuilt) > 0 {
			return &ParseError{Kind: ParseKindInternal, Msg: "rotini: InputReport.Validate has no command to check against: " +
				"every layer is hand-built; check hand-built inputs with Context.CheckInputs"}
		}
		return nil
	}
	if len(r.handBuilt) == 0 {
		return validateStore(r.chain, r.store)
	}
	anchor := r.anchor
	if !r.anchored {
		anchor = frameAnchor(r.merged, r.chain, -1)
	}
	// A short-circuit flag a hand-built layer set waives the rules, as it does in CheckInputs.
	handSet := Presence{}
	for p := range r.handBuilt {
		handSet[p] = InputSource{Layer: "custom"}
	}
	if typedShortCircuited(r.merged, r.chain, anchor, handSet) {
		return nil
	}
	store := r.store.withHandBuilt(r.merged, r.chain, anchor, r.handBuilt)
	if err := validateStore(r.chain, store); err != nil {
		return err
	}
	if shortCircuited(r.chain, store) {
		return nil
	}
	return checkTypedValues(r.merged, r.chain, anchor, func(p FieldPath) bool { return r.handBuilt[p] }, r.stdinSchemas, r.view)
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
		// A later layer's positions win, as its values do.
		merged := maps.Clone(r.argDefs)
		if merged == nil {
			merged = map[int]string{}
		}
		maps.Copy(merged, core.argDefaults)
		r.argDefs = merged
	}
	if core.anchored {
		r.anchor, r.anchored = core.anchor, true
	}
	if core.stdinSchemas != nil {
		r.stdinSchemas = core.stdinSchemas
	}
	if core.view != nil {
		r.view = core.view
		r.store.dir = core.view.base()
	}
	if core.store == nil || len(core.store.scopes) != len(r.store.scopes) {
		return
	}
	if r.store.detached == nil {
		r.store.detached = core.store.detached
	}
	r.store.dashedFirst = r.store.dashedFirst || core.store.dashedFirst
	for i := range core.store.scopes {
		for name, vals := range core.store.scopes[i].flags {
			if r.store.scopes[i].flags == nil {
				r.store.scopes[i].flags = map[string][]string{}
			}
			r.store.scopes[i].flags[name] = vals
			// The winning value's origin travels with it: a fallback layer's names its source,
			// and a later argv value clears it.
			recordOrigin(r.store, i, name, core.store.scopes[i].origin[name])
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
	store, err := parseArgvTokens(chain, rtx.Argv, rtx.argvAcq())
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
	return set, &layerCore{chain: chain, store: store, anchor: anchor, anchored: true, stdinSchemas: readerFor(rtx).stdinSchemas, view: rtx.osView()}, nil
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
				var ad ArgDef
				if i < len(frame.Arguments) {
					ad = frame.Arguments[i]
				}
				_ = coerceTime(f, []string{d}, argTimeSpec(ad, rtx.osView().clockRef()))
				secret := ad.Secret
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
				ts := tagTimeSpec(tag)
				ts.clock = rtx.osView().clockRef()
				_ = coerceTime(f, []string{d}, ts)
				set[fieldPath(topName, channel, fieldName)] = InputSource{
					Layer: "defaults",
					Raw:   redactValue(d, reconHasSecret(body)),
				}
			})
		}
	})
	return set, &layerCore{chain: chain, store: store, argDefaults: argDefaults, anchor: anchor, anchored: true, stdinSchemas: readerFor(rtx).stdinSchemas, view: rtx.osView()}, nil
}

// envLayer acquires the env channel (Env structs + flag env-fallbacks) into v. A registry is
// built only for the inputs v describes.
func envLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	view := rtx.osView()
	overrides := map[string]string{}
	if store, err := parseInto(chain, rtx.Argv, rtx.argvAcq()); err == nil {
		overrides = b.pathOverrides(chain, store, view)
	}
	if view, err = b.inputView(chain, v, overrides, argvWaived(rtx, chain), view); err != nil {
		return nil, nil, err
	}
	var envReg, flagReg *recon.Registry
	if hasEnvChannel(v) {
		if envReg, err = recon.New(recon.WithSources(envSources(v, b.envPrefix, view)...)); err != nil {
			return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer envReg.Close()
	}

	// Flag env fallbacks read the env projection of the recon key, as in reconcileFlags.
	if v.Kind() == reflect.Struct && hasReconFlags(v) {
		if flagReg, err = recon.New(recon.WithSource(flagEnvSource(v, b.envPrefix, view))); err != nil {
			return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer flagReg.Close()
	}

	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	labels := channelLabels{env: envNames(v, "Env", b.envPrefix), view: view}
	return channelLayer(v, chain, anchor, "env", "Env", envReg, flagReg, nil, argvWaived(rtx, chain), labels)
}

// argvWaived reports whether the command line short-circuits the run ([shortCircuited]), for a
// per-channel reader that does not otherwise parse argv. A command line that cannot be parsed
// waives nothing: [Context.ArgvInputs] owns reporting it.
func argvWaived(rtx *Context, chain []Command) bool {
	store, err := parseInto(chain, rtx.Argv, rtx.argvAcq())
	return err == nil && shortCircuited(chain, store)
}

// filesLayer acquires the config-files channel into v. config_source paths are honored here
// too: argv and env are re-read best-effort to learn where the files channel should look, and
// a malformed argv contributes nothing — [Context.ArgvInputs] owns reporting it. No file is
// read when v describes no config inputs and no flag with a fallback.
func filesLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	view := rtx.osView()
	overrides := map[string]string{}
	waived := false
	store, perr := parseInto(chain, rtx.Argv, rtx.argvAcq())
	if perr == nil {
		overrides = b.pathOverrides(chain, store, view)
		waived = shortCircuited(chain, store)
	}
	if view, err = b.inputView(chain, v, overrides, waived, view); err != nil {
		return nil, nil, err
	}
	if perr == nil && view.hasInputEnv() {
		overrides = b.pathOverrides(chain, store, view)
	}
	var reg *recon.Registry
	var cfg *cfgRegs
	if hasConfigChannel(v) || (v.Kind() == reflect.Struct && hasReconFlags(v)) {
		if cfg, err = b.configRegs(chain, overrides, v, waived, view); err != nil {
			return nil, nil, err
		}
		defer cfg.Close()
		reg = cfg.merged
	}
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	labels := cfg.labels()
	labels.view = view
	return channelLayer(v, chain, anchor, "files", "Config", reg, reg, cfg, waived, labels)
}

// channelLayer is the shared env/files core: recon-bind each command's channel struct,
// constraint-check the provided values, fill flag fallbacks, and record presence for
// everything the channel supplied. A short-circuited run (waived) skips the requirement checks
// and a configuration value of the wrong type. A nil registry reads nothing. labels names the
// channel's inputs and files in errors.
func channelLayer(v reflect.Value, chain []Command, anchor int, layerName, structName string, reg, flagReg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) (Presence, *layerCore, error) {
	view := labels.view
	set := Presence{}
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	var bindErr error
	var placed map[int]string // the leaf's argument fallbacks, by position

	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		if bindErr != nil {
			return
		}
		if reg != nil {
			if err := fillChannelStruct(set, ci, topName, structName, layerName, reg, cfg, waived, labels); err != nil {
				bindErr = err
				return
			}
		}
		if flagReg != nil {
			rd := fallbackRead{view: view, waiveFiles: waived, files: labels.files}
			if err := recordFlagFallbacks(set, store, ci, chain, scope, topName, layerName, flagReg, rd); err != nil {
				bindErr = err
				return
			}
			if scope == len(chain)-1 {
				placed, bindErr = recordArgFallbacks(set, ci, chain[scope], topName, layerName, flagReg, rd)
			}
		}
	})
	if bindErr != nil {
		return nil, nil, bindErr
	}
	return set, &layerCore{chain: chain, store: store, argDefaults: placed, anchor: anchor, anchored: true, view: view}, nil
}

// fillChannelStruct binds one command's channel struct from the registry, validates it, and
// records where each field's value came from.
func fillChannelStruct(set Presence, ci reflect.Value, topName, structName, layerName string, reg *recon.Registry, cfg *cfgRegs, waived bool, labels channelLabels) error {
	view := labels.view
	cs := ci.FieldByName(structName)
	if !cs.IsValid() || cs.Kind() != reflect.Struct {
		return nil
	}
	bind := bindReconWaived
	if cfg != nil {
		bind = bindConfigWaived
	} else if err := checkEnvMaps(cs, reg, labels); err != nil {
		return err
	}
	if err := bind(reg, cs.Addr().Interface(), waived); err != nil {
		return labels.bind(channelOf(cfg), err)
	}
	if !waived {
		if err := validateChannelStruct(cs, reg, cfg, view); err != nil {
			return err
		}
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
		if nested, err = fillEnvNested(cs, view); err != nil {
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
func recordFlagFallbacks(set Presence, store *parsedInputs, ci reflect.Value, chain []Command, scope int, topName, layerName string, flagReg *recon.Registry, rd fallbackRead) error {
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
		vals, origin, err := bindFlagFallback(flagReg, f, tag, key, fd, chain, scope, rd)
		if err != nil || vals == nil {
			bindErr = err
			return
		}
		recordOrigin(store, scope, logical, origin)
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

// stdinLayer acquires the stdin channel into v. A streamed field counts as set when it holds
// an iterator; nothing is read until the handler ranges over it.
func stdinLayer(b *InputReader, rtx *Context, v reflect.Value) (Presence, *layerCore, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return nil, nil, err
	}
	args := func(name string) ([]string, error) {
		store, err := parseArgvTokens(chain, rtx.Argv, rtx.argvAcq())
		if err != nil {
			return nil, err
		}
		return leafArgValues(chain, store, name), nil
	}
	if err := b.fillStdin(rtx, v, argvWaived(rtx, chain), args); err != nil {
		return nil, nil, err
	}
	set := Presence{}
	if v.Kind() == reflect.Struct && v.NumField() > 0 {
		leaf := v.Field(v.NumField() - 1)
		if leaf.Kind() == reflect.Struct {
			if sf := leaf.FieldByName("Stdin"); stdinSet(sf) {
				set[fieldPath(v.Type().Field(v.NumField()-1).Name, "Stdin")] = InputSource{Layer: "stdin"}
			}
		}
	}
	return set, &layerCore{chain: chain, view: rtx.osView()}, nil
}

// stdinSet reports whether a Stdin field holds a payload: a non-nil pointer, or a non-nil
// iterator for a streamed stdin.
func stdinSet(sf reflect.Value) bool {
	return sf.IsValid() && (sf.Kind() == reflect.Pointer || sf.Kind() == reflect.Func) && !sf.IsNil()
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
	spans := argSpans(fieldArgDefs(s, frame.Arguments), len(args))
	for j := range s.NumField() {
		vals := args[spans[j][0]:spans[j][1]]
		if len(vals) == 0 {
			continue
		}
		var secret bool
		if j < len(frame.Arguments) {
			secret = frame.Arguments[j].Secret
		}
		set[fieldPath(topName, "Arguments", st.Field(j).Name)] = InputSource{
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
