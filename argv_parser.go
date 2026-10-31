package rotini

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ArgvParser reads the command line in a syntax of the author's choosing. It is the argv layer
// of [Context.Inputs] and its siblings, set with [Program.WithParser]: everything else stays
// rotini's — the environment, configuration files, stdin, defaults, precedence, coercion,
// validation, short-circuit flags and provenance.
//
// ParseArgv reports what the command line says as text, in [ArgvValues]: which flags it set
// to which values, and the invoked command's positional arguments. Rotini then reads those
// values exactly as it reads its own parser's: a flag's `from` acquisition, separator, enum,
// bounds and type all apply, a flag it names that the chain does not declare is an unknown-flag
// [*ParseError], and a flag it sets wins over the same input's environment and configuration
// fallbacks.
//
// Command resolution is not the parser's: the [Resolver] has already chosen the commands by the
// time ParseArgv runs. [Context.Argv] is the whole command line, command names included, and
// [Context.CommandChain] the commands it resolved to, each with the word that named it in
// Matched. A syntax whose words the default resolver would misread as command names needs a
// [Program.WithResolver] too.
//
// ParseArgv may run more than once per read and from concurrent reads, so it must not change rtx
// and should report the same values for the same command line. An error it returns is reported
// as it is; one that carries no [Category] is reported as a usage error, prefixed with the
// command path, and errors.As still reaches it. Return a [*ParseError] to give the failure a
// [ParseKind].
//
// Shell completion keeps rotini's own syntax: `__complete` and [Context.PartialInputs] read the
// command line with rotini's parser, so a custom syntax completes only through the completers
// the program supplies. [*Parser] is rotini's own ArgvParser.
type ArgvParser interface {
	ParseArgv(rtx *Context) (ArgvValues, error)
}

// ArgvValues is what an [ArgvParser] read from the command line, as text.
type ArgvValues struct {
	// Flags are the flags the command line set, in the order given. A flag set more than once
	// appears once per occurrence: a repeatable flag keeps every value, any other the last.
	Flags []ArgvFlag
	// Args are the invoked command's positional arguments, in order, before any separator
	// splits them.
	Args []string
}

// ArgvFlag is one occurrence of a flag on the command line.
type ArgvFlag struct {
	// Name names the flag: its spec name ("replicas"), or any identifier it declares, dashes
	// included ("--replicas", "-r", or a negated form such as "--no-color", which sets false).
	// It is looked up from the invoked command up to the root.
	Name string
	// Value is the value as given. A bool flag given "" is true. A count flag given "" counts
	// one occurrence, and one given a non-negative integer counts that many. A negated form
	// takes no value. Any other flag takes Value as it is, "" included.
	Value string
	// Typed is how the user wrote the flag, which an error about it names ("replicas" for
	// `replicas=3`); "" names it by its first long identifier.
	Typed string
}

// ParseArgv reads rtx's command line with rotini's own grammar and reports the flags and
// arguments it gives, without acquiring any value: a `@file` or "-" value is reported as
// typed, and an argument glob unexpanded. A wrapping [ArgvParser] can rewrite these and return
// them. A command line that does not parse is a [*ParseError].
func (p *Parser) ParseArgv(rtx *Context) (ArgvValues, error) {
	chain, err := layerChain(rtx)
	if err != nil {
		return ArgvValues{}, err
	}
	var out ArgvValues
	if _, err := parseArgvTokens(quietChain(chain), rtx.Argv, argvAcq{capture: &out}); err != nil {
		return ArgvValues{}, err
	}
	return out, nil
}

// activeParser returns the run's custom [ArgvParser], or nil when the run reads argv with
// rotini's own.
func (rtx *Context) activeParser() ArgvParser {
	rtx.mu.RLock()
	p := rtx.parser
	rtx.mu.RUnlock()
	if _, own := p.(*Parser); own {
		return nil
	}
	return p
}

// settledParser is parser as a setting holds it: a nil *Parser is no parser, so the default.
func settledParser(parser ArgvParser) ArgvParser {
	if pp, ok := parser.(*Parser); ok && pp == nil {
		return nil
	}
	return parser
}

// argvTokens reads what the command line supplies, without defaults, through the run's parser:
// rotini's grammar, or the program's [ArgvParser].
func (rtx *Context) argvTokens(chain []Command) (*parsedInputs, error) {
	if p := rtx.activeParser(); p != nil {
		return customArgvStore(rtx, p, chain, rtx.argvAcq())
	}
	return parseArgvTokens(chain, rtx.Argv, rtx.argvAcq())
}

// argvStore is argvTokens with the declared defaults applied, as [parseInto] gives them.
func (rtx *Context) argvStore(chain []Command) (*parsedInputs, error) {
	store, err := rtx.argvTokens(chain)
	if err != nil {
		return nil, err
	}
	applyDefaults(chain, store)
	return store, nil
}

// quietArgv is [quietParse] through the run's parser: what the command line says, read without
// acquiring a value or expanding a glob. nil when it does not parse.
func (rtx *Context) quietArgv(chain []Command) *parsedInputs {
	p := rtx.activeParser()
	if p == nil {
		return quietParse(chain, rtx.Argv)
	}
	store, err := customArgvStore(rtx, p, quietChain(chain), argvAcq{})
	if err != nil {
		return nil
	}
	return store
}

// customArgvStore runs p and records what it read into a store, as rotini's parser records the
// words it reads.
func customArgvStore(rtx *Context, p ArgvParser, chain []Command, acq argvAcq) (*parsedInputs, error) {
	vals, err := p.ParseArgv(rtx)
	if err != nil {
		return nil, argvParserError(err, chain)
	}
	store, err := recordArgvValues(chain, vals, acq)
	if err != nil {
		return nil, atCommand(err, chain)
	}
	return store, nil
}

// recordArgvValues records an [ArgvParser]'s values for chain into a store.
func recordArgvValues(chain []Command, vals ArgvValues, acq argvAcq) (*parsedInputs, error) {
	store := newParsedInputs(len(chain))
	store.dir, store.clock = acq.dir, acq.clock
	store.poolHint = len(vals.Flags) + len(vals.Args)
	leaf := len(chain) - 1
	for _, f := range vals.Flags {
		if err := store.recordValueFlag(chain, f, acq); err != nil {
			return nil, err
		}
	}
	for _, a := range vals.Args {
		if err := store.recordArg(chain[leaf], leaf, a, acq); err != nil {
			return nil, err
		}
	}
	store.scopes[leaf].argvArgs = len(store.scopes[leaf].args)
	store.expandGlobs(chain[leaf].Arguments, leaf, acq.goos)
	return store, nil
}

// recordValueFlag records one [ArgvFlag], read by the rules [ArgvFlag] states.
func (p *parsedInputs) recordValueFlag(chain []Command, f ArgvFlag, acq argvAcq) error {
	fd, idx, negated, ok := argvFlagRef(chain, f.Name)
	typed := f.Typed
	if !ok {
		if typed == "" {
			typed = f.Name
		}
		return &ParseError{
			Kind:       ParseKindUnknownFlag,
			Msg:        fmt.Sprintf("unknown flag %q", typed),
			Flag:       typed,
			Token:      typed,
			Candidates: chainFlagIdentifiers(chain),
		}
	}
	if typed == "" {
		typed = flagLabel(*fd)
		if negated {
			typed = f.Name
		}
	}
	value := f.Value
	switch {
	case negated:
		if value != "" {
			return &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s is the negated form and takes no value; use %s to set one", typed, flagLabel(*fd)),
				Flag: typed,
			}
		}
		value = "false"
	case fd.Type == "count":
		n := 1
		if value != "" {
			var err error
			if n, err = strconv.Atoi(value); err != nil || n < 0 {
				return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s counts occurrences; %q is not a count", typed, value), Flag: typed, Token: value}
			}
		}
		if n == 0 {
			if fd.Local && idx != len(chain)-1 {
				return localFlagError(chain, idx, typed)
			}
			return p.recordZeroCount(idx, fd, typed)
		}
		for range n {
			if err := p.recordScopedFlag(chain, idx, fd, "1", typed, acq); err != nil {
				return err
			}
		}
		return nil
	case fd.Type == "bool" && value == "":
		value = "true"
	}
	return p.recordScopedFlag(chain, idx, fd, value, typed, acq)
}

// recordZeroCount records a count flag set to zero occurrences: set on argv, tallying none.
func (p *parsedInputs) recordZeroCount(idx int, fd *FlagDef, typed string) error {
	si := &p.scopes[idx]
	if si.typed == nil {
		si.typed = map[string]string{}
	}
	si.typed[fd.Name] = typed
	if si.flags == nil {
		si.flags = map[string][]string{}
	}
	if si.flags[fd.Name] == nil {
		si.flags[fd.Name] = []string{}
	}
	if p.argvSet[idx] == nil {
		p.argvSet[idx] = map[string]bool{}
	}
	p.argvSet[idx][fd.Name] = true
	return nil
}

// argvFlagRef finds the flag an [ArgvFlag] names: by an identifier when name starts with a
// dash, else by its spec name, from the invoked command up to the root.
func argvFlagRef(chain []Command, name string) (fd *FlagDef, idx int, negated, ok bool) {
	if strings.HasPrefix(name, "-") {
		return findFlagRef(chain, name)
	}
	for i := range slices.Backward(chain) {
		for j := range chain[i].Flags {
			if f := &chain[i].Flags[j]; f.Name == name {
				return f, i, false, true
			}
		}
	}
	return nil, -1, false, false
}

// argvParserError is how a run reports an [ArgvParser]'s error: as it is when it carries a
// [Category], else as a usage error naming the command path.
func argvParserError(err error, chain []Command) error {
	if CategoryOf(err) != CategoryNone {
		return atCommand(err, chain)
	}
	return &argvUsageError{err: err, path: pathOf(chain)}
}

// argvUsageError is an [ArgvParser]'s uncategorized error, reported as bad input against the
// command path.
type argvUsageError struct {
	err  error
	path string
}

func (e *argvUsageError) Error() string   { return e.path + ": " + e.err.Error() }
func (e *argvUsageError) Unwrap() []error { return []error{e.err, ErrUsage} }
