package rotini

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// CompletionOptions are what a completer can ask of the shell for its answer, with
// [Context.SetCompletionOptions].
type CompletionOptions struct {
	// NoSpace: the shell adds no space after the inserted candidate, so the user goes on typing
	// the same word, such as a path segment. macOS's bash 3.2 always adds the space, and fish
	// adds none only after a candidate ending in one of @ = / : . ,
	NoSpace bool
	// KeepOrder: the whole answer is shown in the order given, sub-command names included. A
	// completer's own candidates keep their order without it. bash before 4.4 sorts anyway.
	KeepOrder bool
}

// SetCompletionOptions asks the shell for opts, from a [FlagValueCompleter] or
// [ArgValueCompleter], for this request's whole answer. Options add up across calls: one a call
// asks for stays on.
//
//	rtx.SetCompletionOptions(rotini.CompletionOptions{NoSpace: true})
//	return []string{"s3://", "gs://"}
//
// It does nothing outside a completion request.
func (rtx *Context) SetCompletionOptions(opts CompletionOptions) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if o := rtx.completionOptions; o != nil {
		o.NoSpace = o.NoSpace || opts.NoSpace
		o.KeepOrder = o.KeepOrder || opts.KeepOrder
	}
}

// PartialInputs reads what has been typed so far, for a completer: the command line up to the
// word being completed, over the environment, over the declared defaults, in that precedence.
// T is the inputs type of the running command, anchored as [Context.Inputs] anchors it; a
// [FlagValueCompleter] runs as the command that declares the flag.
//
//	in, set, _ := rtx.PartialInputs[DeployInputs]()
//	if _, ok := set["Deploy.Flags.Region"]; ok {
//		return servicesIn(in.Deploy.Flags.Region)
//	}
//
// It is lenient: nothing is validated or required, an unknown or half-typed flag is skipped,
// and a value that doesn't convert leaves its field unset. It never reads stdin or a file: a
// `-` or `@file` value is kept as typed, and configuration files are not read. The Presence
// names every field a layer supplied, defaults included.
//
// Outside a completion request it reads the whole of [Context.Argv]. The error is a wiring
// mistake: no resolved command, or a T that doesn't describe the running command.
func (rtx *Context) PartialInputs[T any]() (T, Presence, error) {
	var zero T
	chain, err := layerChain(rtx)
	if err != nil {
		return zero, nil, err
	}
	if _, err := layerAnchor(rtx, reflect.ValueOf(&zero).Elem(), chain); err != nil {
		return zero, nil, err
	}

	var layers []InputLayer[T]
	if defaults, err := rtx.DefaultInputs[T](); err == nil {
		layers = append(layers, defaults)
	}
	var env T
	if set, core, err := partialEnvLayer(readerFor(rtx), rtx, reflect.ValueOf(&env).Elem(), chain); err == nil {
		layers = append(layers, InputLayer[T]{Name: "env", Values: env, Set: set, core: core})
	}
	var argv T
	if set, core, err := partialArgvLayer(rtx, reflect.ValueOf(&argv).Elem(), chain); err == nil {
		layers = append(layers, InputLayer[T]{Name: "argv", Values: argv, Set: set, core: core})
	}
	merged, report := MergeInputsWithReport(layers...)
	return merged, maps.Clone(report.set), nil
}

// partialArgv returns the words PartialInputs reads: the command line without the word being
// completed, with bash's "=" splits ("--flag = value", "key = value") joined back.
func (rtx *Context) partialArgv() []string {
	rtx.mu.RLock()
	words := rtx.Argv
	completing := rtx.completionOptions != nil
	rtx.mu.RUnlock()
	if completing && len(words) > 0 {
		words = words[:len(words)-1]
		var joined []string
		for i := 0; i < len(words); i++ {
			if words[i] == "=" && len(joined) > 0 {
				joined[len(joined)-1] += "="
				if i+1 < len(words) {
					joined[len(joined)-1] += words[i+1]
					i++
				}
				continue
			}
			joined = append(joined, words[i])
		}
		words = joined
	}
	return words
}

// partialArgvLayer binds the command line typed so far into v, leniently (see PartialInputs).
func partialArgvLayer(rtx *Context, v reflect.Value, chain []Command) (Presence, *layerCore, error) {
	store, err := quietTokens(chain, lenientArgv(chain, rtx.partialArgv()))
	if err != nil {
		return nil, nil, err
	}
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	dropUnbindable(v.Type(), store, chain, anchor)
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}
	set := Presence{}
	walkCommandStructs(v, chain, anchor, func(topName string, scope int, ci reflect.Value) {
		frame := chain[scope]
		si := store.scopes[scope]
		eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, _ reflect.Value) {
			if vals, ok := si.flags[logical]; ok {
				fd, _ := findFlagDef(frame.Flags, logical)
				set[fieldPath(topName, "Flags", fieldName)] = InputSource{Layer: "argv", Raw: redactValue(strings.Join(vals, ", "), fd.Secret), Origin: argvFlagOrigin(si, logical)}
			}
		})
		argPresence(set, "argv", topName, ci, frame, si.args)
	})
	return set, &layerCore{chain: chain, store: store, anchor: anchor, anchored: true, view: rtx.osView()}, nil
}

// lenientArgv drops the words the parser would reject — unknown flags, a flag missing its value,
// a malformed cluster — reading them with the parser's own rules, so what is left parses.
func lenientArgv(chain []Command, argv []string) []string {
	leaf := len(chain) - 1
	depth, positionals := 1, 0
	terminated := false
	pt := passthroughArg(chain[leaf].Arguments)
	noop := func(int, FlagDef, string, string) error { return nil }
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		switch {
		case terminated || (chain[leaf].Passthrough && depth == len(chain)):
			out = append(out, tok)
		case tok == "--":
			terminated = true
			out = append(out, tok)
		case isFlag(chain[:depth], tok):
			extra, err := consumeFlagToken(chain[:depth], tok, argv, i, noop)
			if err == nil {
				out = append(out, argv[i:i+extra+1]...)
			}
			i += extra
		case positionals == 0 && depth < len(chain) && namesFrame(chain[depth-1], tok, chain[depth].Name):
			depth++
			out = append(out, tok)
		default:
			out = append(out, tok)
			terminated = depth == len(chain) && (positionals == pt || chain[leaf].OptionsFirst)
			positionals++
		}
	}
	return out
}

// namesFrame reports whether tok names cur's child called name.
func namesFrame(cur Command, tok, name string) bool {
	c, ok := findChild(cur, tok)
	return ok && c.Name == name
}

// dropUnbindable removes from store each flag value, and the trailing positionals, that would
// fail to convert into an inputs value of type t, so the rest still binds.
func dropUnbindable(t reflect.Type, store *parsedInputs, chain []Command, anchor int) {
	try := func(scope int, si scopeInputs) bool {
		probe := &parsedInputs{scopes: make([]scopeInputs, len(store.scopes))}
		probe.scopes[scope] = si
		return bindInputs(reflect.New(t).Elem(), probe, chain, anchor) == nil
	}
	for s := range store.scopes {
		si := &store.scopes[s]
		names := make([]string, 0, len(si.flags))
		for name := range si.flags {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if !try(s, scopeInputs{flags: map[string][]string{name: si.flags[name]}, typed: si.typed}) {
				delete(si.flags, name)
				delete(store.argvSetAt(s), name)
			}
		}
		for len(si.args) > 0 && !try(s, scopeInputs{args: si.args}) {
			si.args = si.args[:len(si.args)-1]
		}
	}
}

// partialEnvLayer is the environment channel without its requirement and constraint checks, as
// a short-circuited run reads it.
func partialEnvLayer(b *InputReader, rtx *Context, v reflect.Value, chain []Command) (Presence, *layerCore, error) {
	view := rtx.osView()
	var envReg, flagReg *recon.Registry
	var err error
	if hasEnvChannel(v) {
		if envReg, err = recon.New(recon.WithoutWatch(), recon.WithSources(envSources(v, b.envPrefix, view)...)); err != nil {
			return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer envReg.Close()
	}
	if v.Kind() == reflect.Struct && hasReconFlags(v) {
		if flagReg, err = recon.New(recon.WithoutWatch(), recon.WithSource(flagEnvSource(v, b.envPrefix, view))); err != nil {
			return nil, nil, internalBind(channelEnv, "", "could not build the environment registry", err)
		}
		defer flagReg.Close()
	}
	anchor, err := layerAnchor(rtx, v, chain)
	if err != nil {
		return nil, nil, err
	}
	labels := channelLabels{env: envNames(v, "Env", b.envPrefix), view: view}
	return channelLayer(v, chain, anchor, "env", "Env", envReg, flagReg, nil, true, labels)
}
