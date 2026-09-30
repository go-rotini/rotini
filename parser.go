package rotini

import (
	"encoding"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// parsedInputs is one invocation's parsed argv, indexed by position in the resolved chain
// (root = 0 … leaf). Keying by position rather than command name means two commands on a
// single path can never collide, and a statically-composed child still reads its own
// generated types unchanged.
type parsedInputs struct {
	scopes  []scopeInputs     // one entry per resolved chain frame, root → leaf
	argvSet []map[string]bool // per frame: logical flag names explicitly set on argv (not defaults, not env/config fallback)

	// span limits validation to the frames an inputs type describes, [lo, hi). A parent
	// collecting its own inputs in CascadingPreRun describes only its own frames; judging the
	// leaf's required inputs there failed `app sub --help` before sub's handler could answer
	// the --help. Zero value: no limit.
	span *[2]int

	// detached is the likely mistake behind an unexpected positional: a word that directly
	// followed a flag whose value is optional, and would have been a valid value for it —
	// `--dry-run server`, where the value had to be attached (`--dry-run=server`). [0] is the
	// flag as typed, [1] the word. nil when there is none.
	detached *[2]string
}

// detachedHint is the hint an unexpected-positional error carries when one of the extra
// words is the value of an optional-value flag written detached, else "".
func (p *parsedInputs) detachedHint(extra []string) string {
	if p == nil || p.detached == nil || !slices.Contains(extra, p.detached[1]) {
		return ""
	}
	return fmt.Sprintf(" — %s takes its value attached: %s=%s", p.detached[0], p.detached[0], p.detached[1])
}

// covers reports whether chain frame i is one this validation pass judges.
func (p *parsedInputs) covers(i int) bool {
	return p == nil || p.span == nil || (i >= p.span[0] && i < p.span[1])
}

// setOnArgv reports whether the flag was explicitly provided on argv in chain frame idx — the
// presence convention flag groups and dependencies enforce. A default or fallback is not
// presence.
func (p *parsedInputs) setOnArgv(idx int, name string) bool {
	if p == nil || idx < 0 || idx >= len(p.argvSet) || p.argvSet[idx] == nil {
		return false
	}
	return p.argvSet[idx][name]
}

// scopeInputs holds one chain frame's parsed values: flag values by logical name
// (a repeatable flag keeps every value) and the positional arguments.
type scopeInputs struct {
	flags map[string][]string
	args  []string
	// typed is the identifier the user typed for each flag set on argv ("--due", "-d",
	// "--no-color", "--db.host"), so an error about its value names what they wrote.
	typed map[string]string
	// used is every identifier each flag was set through, in order — what Deprecations reports
	// from, so a token counts against the flag the parser actually bound it to.
	used map[string][]string
}

// label is how an error names flag fd: as the user typed it, else by its preferred identifier.
func (si scopeInputs) label(fd FlagDef) string {
	if t := si.typed[fd.Name]; t != "" {
		return t
	}
	return flagLabel(fd)
}

// ParseKind classifies a [ParseError] so a funnel can branch on the failure without matching
// the human message. Most kinds are the end-user's to fix; [ParseKindInternal] is a misuse of
// the parser API itself — surfaced as a [*ParseError] for uniformity, but the author's bug.
type ParseKind int

// The parse failure kinds. Branch on these rather than on a message: the message is
// presentation, the kind is data.
const (
	// ParseKindUnspecified is the zero value: a [ParseError] whose construction
	// site did not classify it (a hand-built error, or a path predating EH5).
	ParseKindUnspecified         ParseKind = iota
	ParseKindUnknownFlag                   // an argv token looked like a flag no command on the chain declares
	ParseKindUnknownCommand                // a stray positional on a branch-only command (a mistyped sub-command)
	ParseKindNeedsValue                    // a value-taking flag was given no value
	ParseKindInvalidValue                  // a value could not be coerced/resolved (bad type, unreadable @file, malformed map, value on a no-value flag)
	ParseKindEnumViolation                 // a value was not one of a declared enum's members
	ParseKindConstraintViolation           // a declared bound or flag-group/dependency rule was violated
	ParseKindMissingRequired               // a required flag or argument was absent
	ParseKindNoArguments                   // a positional was given to a command that accepts none
	ParseKindTooManyArguments              // more positionals than the command's declared (non-variadic) arity
	ParseKindInternal                      // a parser API misuse: nil parser/context, or a bad out argument
)

// String renders the kind as a short, stable label (for logs and tests).
func (k ParseKind) String() string {
	switch k {
	case ParseKindUnknownFlag:
		return "unknown-flag"
	case ParseKindUnknownCommand:
		return "unknown-command"
	case ParseKindNeedsValue:
		return "needs-value"
	case ParseKindInvalidValue:
		return "invalid-value"
	case ParseKindEnumViolation:
		return "enum-violation"
	case ParseKindConstraintViolation:
		return "constraint-violation"
	case ParseKindMissingRequired:
		return "missing-required"
	case ParseKindNoArguments:
		return "no-arguments"
	case ParseKindTooManyArguments:
		return "too-many-arguments"
	case ParseKindInternal:
		return "internal"
	default:
		return "unspecified"
	}
}

// ParseError is a parse-time failure caused by bad input. It is data, not presentation: the
// message offers no suggestions and no usage dump, and the structured fields let a handler
// compose its own response — switch on Kind, pair Token with Candidates and a bound
// [Suggestor] for "did you mean", or render help for Command. It unwraps to [ErrUsage], so
// [CategoryOf] reports [CategoryUsage].
//
// That is a label, not an exit code. rotini forces no category→code mapping and the default
// funnel exits 1 for any failure; a program that wants the common "2 means the command line was
// wrong" convention maps it in its own funnel. See [Category].
//
//	var pe *rotini.ParseError
//	if errors.As(err, &pe) {
//	    switch pe.Kind {
//	    case rotini.ParseKindUnknownFlag, rotini.ParseKindUnknownCommand:
//	        // pe.Token + pe.Candidates feed a Suggestor's "did you mean"
//	    case rotini.ParseKindMissingRequired:
//	        // prompt, or point at the help for pe.Command
//	    }
//	}
type ParseError struct {
	Kind       ParseKind // what went wrong, for branching without matching Msg (zero = ParseKindUnspecified)
	Msg        string    // the human-readable failure, opinion-free
	Command    string    // the command in whose scope parsing failed ("" when not command-scoped)
	Flag       string    // the flag involved, by the identifier or label used ("" when not flag-related)
	Token      string    // the offending argv token or value ("" when none)
	Candidates []string  // the vocabulary Token failed against — sibling commands, declared flags, enum members (nil when none applies)
}

// Error renders the parse failure as a single, user-facing line.
func (e *ParseError) Error() string { return e.Msg }

// Unwrap exposes the category sentinel, so [CategoryOf] and errors.Is reach it.
func (e *ParseError) Unwrap() error { return ErrUsage }

// Parser is rotini's argv parser: given a resolved chain, it parses and validates the command
// line against what those commands declare — GNU/POSIX grammar, typed coercion, enum and
// constraint checks — failing with a [*ParseError].
//
// It is a service, so parsing is opt-in (a CLI that wants raw argv binds nothing and reads
// [Context.Argv]) and the registry is a dependency-injection seam:
//
//	parser := rtx.Parser()
//	var in MycliInputs
//	err := parser.Parse(rtx, &in)
type Parser struct{}

// NewParser returns rotini's default [Parser], ready to bind under the "parser"
// registry key.
func NewParser() *Parser {
	return &Parser{}
}

// Parse binds the running command's arguments into out — a non-nil pointer to the generated
// inputs struct — from the resolved chain and raw argv on rtx, in the json.Unmarshal style:
//
//	var in MycliInputs
//	if err := parser.Parse(rtx, &in); err != nil { /* handler owns it */ }
//
// It applies declared defaults, validates required and enum, then fills out by reflection from
// the `rotini:"…"` struct tags. Built-ins and any [encoding.TextUnmarshaler] are coerced, and
// a trailing []string absorbs the remaining positionals.
//
// It returns a [*ParseError] when out is not a non-nil pointer, a flag is unknown or missing
// its value, a required input is absent, or a value falls outside a declared enum.
//
// # It does not check that out describes the running command
//
// Parse is the mechanism; [Collect] is the contract. Collect and the per-channel layer functions
// reject a struct that cannot describe the caller's own command — one covering more commands
// than the caller is deep — because a handler asking for its own inputs can only have meant one
// thing. Parse binds what fits and leaves the rest zeroed, which is what lets a caller drive it
// with a struct spanning a whole tree and reuse it across several argv shapes.
//
// That is a deliberate split, not an oversight, and it is the only place in the input surface
// where a mismatched struct passes quietly. A handler collecting its own inputs should reach for
// Collect and get the check.
func (p *Parser) Parse(rtx *Context, out any) error {
	store, chain, err := p.parseBind(rtx, out)
	if err != nil {
		return err
	}
	if err := validate(chain, store); err != nil {
		return err
	}
	if err := validateFlagGroups(chain, store); err != nil {
		return err
	}
	return validateFlagDependencies(chain, store)
}

// Deprecation is a deprecated CLI token found in this invocation's argv: the identifier used,
// the kind of input, and that input's logical name. It is pure identification — rotini attaches
// no message and does nothing with it. It implements error so it can be returned or printed
// directly.
type Deprecation struct {
	Kind       string // "flag", "argument" or "command"
	Name       string // the input's logical name (the flag/argument/command name)
	Identifier string // the token actually used on argv (e.g. "--conf", "build"); an argument's <name>
	Message    string // the spec's `deprecated:` message, when the input is deprecated as a whole
}

// Error renders the deprecation notice as a single line, with the author's message when there
// is one: `flag "--conf" is deprecated: use --config`.
func (d Deprecation) Error() string {
	if d.Message != "" {
		return fmt.Sprintf("%s %q is deprecated: %s", d.Kind, d.Identifier, d.Message)
	}
	return fmt.Sprintf("deprecated %s identifier %q was used", d.Kind, d.Identifier)
}

// Deprecations returns each deprecated token this invocation actually used — a command
// invoked via a deprecated alias, or a flag set via a deprecated identifier. It is a data feed
// only: rotini prints nothing, and the handler decides what to do with each:
//
//	for _, d := range rotini.Deprecations(rtx) {
//		rtx.RecordWarning(fmt.Errorf("%w — use %q instead", d, d.Name))
//	}
//
// It is a function rather than a method on [Parser] because it needs no parser: everything it
// reports is already on the [Context] — the resolved chain and the argv that produced it. As a
// method it forced a handler to pull a *Parser out of the registry to obtain a receiver it
// never used, which in turn made a parser binding look mandatory in every entrypoint. Supply a Parser
// when you want to override the default or call [Parser.Parse] yourself; deprecation reporting
// needs neither.
func Deprecations(rtx *Context) []Deprecation {
	if rtx == nil {
		return nil
	}
	argv := rtx.Argv
	var out []Deprecation
	chain := rtx.Chain()
	store := quietParse(chain, argv)
	for i, frame := range chain {
		// A command deprecated as a whole reports however it was invoked; one with only
		// deprecated aliases reports when one of those resolved it (frame.Matched).
		if frame.Matched != "" && deprecatedToken(frame.Matched, frame.DeprecatedIdentifiers, frame.Deprecated) {
			out = append(out, Deprecation{Kind: "command", Name: frame.Name, Identifier: frame.Matched, Message: frame.Deprecated})
		}
		if store == nil {
			continue // argv does not parse; the parse error is what the user sees
		}
		for _, fd := range frame.Flags {
			out = append(out, flagDeprecations(fd, store.scopes[i].used[fd.Name])...)
		}
	}
	// A deprecated argument reports when a value was supplied for it.
	if n := len(chain); n > 0 {
		out = append(out, argumentDeprecations(chain, argv)...)
	}
	return out
}

// deprecatedToken reports whether using token deprecates: when some spellings are listed as
// deprecated, only those are — the others are the ones to move to, and the message (if any) is
// about the listed ones; with none listed, a message deprecates every spelling.
func deprecatedToken(token string, deprecatedIDs []string, message string) bool {
	if len(deprecatedIDs) > 0 {
		return slices.Contains(deprecatedIDs, token)
	}
	return message != ""
}

// flagDeprecations reports a flag set on argv through a deprecated identifier — or through any
// identifier when the flag as a whole is deprecated — once per identifier used, carrying the
// spec's message.
func flagDeprecations(fd FlagDef, used []string) []Deprecation {
	var out []Deprecation
	for _, id := range used {
		if deprecatedToken(id, fd.DeprecatedIdentifiers, fd.Deprecated) {
			out = append(out, Deprecation{Kind: "flag", Name: fd.Name, Identifier: id, Message: fd.Deprecated})
		}
	}
	return out
}

// quietParse tokenizes argv against chain the way the parser does, on a copy of the chain with
// value acquisition off, so asking what argv says never reads a file or stdin. nil when argv
// does not parse.
func quietParse(chain []ResolvedCommand, argv []string) *parsedInputs {
	quiet := make([]ResolvedCommand, len(chain))
	for i, frame := range chain {
		flags := make([]FlagDef, len(frame.Flags))
		for j, fd := range frame.Flags {
			fd.From = nil
			flags[j] = fd
		}
		frame.Flags = flags
		quiet[i] = frame
	}
	store, err := parseArgvTokens(quiet, argv, nil)
	if err != nil {
		return nil
	}
	return store
}

// positionals are the leaf's positional tokens in argv.
func positionals(chain []ResolvedCommand, argv []string) []string {
	if store := quietParse(chain, argv); store != nil {
		return store.scopes[len(chain)-1].args
	}
	return nil
}

// argumentDeprecations reports each deprecated argument of the leaf that argv supplied a value
// for, by position.
func argumentDeprecations(chain []ResolvedCommand, argv []string) []Deprecation {
	leaf := chain[len(chain)-1]
	if !slices.ContainsFunc(leaf.Arguments, func(a ArgDef) bool { return a.Deprecated != "" }) {
		return nil
	}
	supplied := len(positionals(chain, argv))
	var out []Deprecation
	for i, ad := range leaf.Arguments {
		if ad.Deprecated != "" && i < supplied {
			out = append(out, Deprecation{Kind: "argument", Name: ad.Name, Identifier: "<" + ad.Name + ">", Message: ad.Deprecated})
		}
	}
	return out
}

// parseBind parses argv into a store and binds it into out without validating — that is
// [validate]'s job. It is the shared front half of [Parser.Parse] and of the [Binder], which
// reconciles env and config fallbacks into the store before validating, so a required input is
// satisfiable from any source. It returns the store and chain for that deferred pass.
func (p *Parser) parseBind(rtx *Context, out any) (*parsedInputs, []ResolvedCommand, error) {
	if p == nil {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: nil parser"}
	}
	if rtx == nil {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: parse on nil context"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: Parse out argument must be a non-nil pointer to an inputs struct"}
	}
	chain := rtx.Chain()
	if len(chain) == 0 {
		return nil, nil, &ParseError{Kind: ParseKindInternal, Msg: "rotini: no command resolved for this context"}
	}
	store, err := parseInto(chain, rtx.Argv, rtx.flagStdin())
	if err != nil {
		return nil, nil, err
	}
	if err := bindInputs(rv.Elem(), store, chain, frameAnchor(rv.Elem(), chain, rtx.frameIndex())); err != nil {
		return nil, nil, err
	}
	return store, chain, nil
}

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized flag, or one
// missing its value, is a [ParseError]. Chain command tokens are consumed, and everything
// after the leaf command and after "--" is a positional of the leaf. Declared defaults are
// applied; required and enum checks are [validate]'s job.
func parseInto(chain []ResolvedCommand, argv []string, stdin io.Reader) (*parsedInputs, error) {
	store, err := parseArgvTokens(chain, argv, stdin)
	if err != nil {
		return nil, err
	}
	applyDefaults(chain, store)
	return store, nil
}

// flagTokenValue resolves the value one argv flag token carries and returns the index to
// continue from, which advances only when the value came from the following token rather than
// an inline "=value".
//
// Five shapes: a count flag takes no value, and an inline one is an error since the tally is
// computed; a bool defaults to "true" but honors an inline value; anything with an inline
// value uses it; a flag with an implicit value takes that and leaves the next token alone;
// anything else consumes the next token, and running out is an error.
func flagTokenValue(fdef FlagDef, name, inline string, hasInline bool, argv []string, i int) (value string, next int, err error) {
	switch {
	case fdef.Type == "count":
		if hasInline {
			return "", i, &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("flag %q counts occurrences and takes no value", name), Flag: name}
		}
		return "1", i, nil // each occurrence appends one marker; the binder tallies them
	case fdef.Type == "bool":
		if hasInline {
			return inline, i, nil
		}
		return "true", i, nil
	case hasInline:
		return inline, i, nil
	case fdef.ImplicitValue != "":
		return fdef.ImplicitValue, i, nil // optional value, none attached: never consume the next word
	default:
		i++
		if i >= len(argv) {
			return "", i, &ParseError{Kind: ParseKindNeedsValue, Msg: fmt.Sprintf("flag %q needs a value", name), Flag: name}
		}
		return argv[i], i, nil
	}
}

// consumeFlagToken parses one flag token from argv[i], records it, and reports how many extra
// argv entries it consumed — a separate value word, or the tail of a short cluster.
//
// A token matching no declared identifier is retried as a POSIX short cluster (-vh → -v -h,
// -n5 → -n 5). Long flags never cluster, so an unmatched one is an unknown-flag error carrying
// the chain's vocabulary for a [Suggestor].
func consumeFlagToken(chain []ResolvedCommand, tok string, argv []string, i int, addFlag func(int, FlagDef, string, string) error) (int, error) {
	name, inline, hasInline := splitFlag(tok)

	fdef, idx, negated, ok := findFlagMatch(chain, name)
	if !ok {
		// --db.host=h sets one field of the object flag --db: it is the occurrence host=h.
		if ofd, oidx, field, isField := objectFieldFlag(chain, name); isField {
			value, next, err := flagTokenValue(FlagDef{Type: "string"}, name, inline, hasInline, argv, i)
			if err != nil {
				return 0, err
			}
			// Errors name the object flag (--db), not the field spelling: the field is named
			// in the message itself ("unknown key \"nope\"").
			return next - i, addFlag(oidx, ofd, quotePair(field, value), strings.TrimSuffix(name, "."+field))
		}
		if isShortCluster(name) {
			return parseCluster(chain, name[1:], inline, hasInline, argv, i, addFlag)
		}
		return 0, &ParseError{
			Kind:       ParseKindUnknownFlag,
			Msg:        fmt.Sprintf("unknown flag %q", name),
			Flag:       name,
			Token:      name,
			Candidates: chainFlagIdentifiers(chain),
		}
	}

	// A negated form IS the value: "--no-color" means false, and "--no-color=x" would be
	// asking two questions at once, so it is rejected rather than guessed at.
	if negated {
		if hasInline {
			return 0, &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("flag %q is the negated form and takes no value — use %q to set one", name, "--"+fdef.Name),
				Flag: name,
			}
		}
		return 0, addFlag(idx, fdef, "false", name)
	}

	value, next, err := flagTokenValue(fdef, name, inline, hasInline, argv, i)
	if err != nil {
		return 0, err
	}
	return next - i, addFlag(idx, fdef, value, name)
}

// recordArgvFlag records one argv occurrence of flag fd at chain frame idx: the identifier the
// user typed (for error labels), the value resolved through the flag's acquisition modes and
// split on its separator, and the fact that argv set it.
func (p *parsedInputs) recordArgvFlag(idx int, fd FlagDef, value, typed string, stdin io.Reader) error {
	si := &p.scopes[idx]
	if si.typed == nil {
		si.typed = map[string]string{}
	}
	si.typed[fd.Name] = typed
	if si.used == nil {
		si.used = map[string][]string{}
	}
	if !slices.Contains(si.used[fd.Name], typed) {
		si.used[fd.Name] = append(si.used[fd.Name], typed)
	}
	value, err := resolveFlagValue(fd, typed, value, stdin)
	if err != nil {
		return err
	}
	values, err := splitValue(value, fd.Separator)
	if err != nil {
		return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", typed, err), Flag: typed}
	}
	if si.flags == nil {
		si.flags = map[string][]string{}
	}
	si.flags[fd.Name] = append(si.flags[fd.Name], values...)
	if p.argvSet[idx] == nil {
		p.argvSet[idx] = map[string]bool{}
	}
	p.argvSet[idx][fd.Name] = true
	return nil
}

// recordArg records one positional of the leaf command, split on the separator of the variadic
// argument it lands in, if that declares one.
func (p *parsedInputs) recordArg(leaf ResolvedCommand, idx int, value string) error {
	values := []string{value}
	if def, ok := variadicAt(leaf.Arguments, len(p.scopes[idx].args)); ok && def.Separator != "" {
		var err error
		if values, err = splitValue(value, def.Separator); err != nil {
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("<%s>: %v", def.Name, err)}
		}
	}
	p.scopes[idx].args = append(p.scopes[idx].args, values...)
	return nil
}

// parseArgvTokens is parseInto minus defaults: exactly what argv supplied. It records each
// explicitly-set flag in the store's argvSet, the single source of truth for "set on the
// command line". stdin backs the from:stdin sentinel and is read only when a "-" value on an
// opted-in flag actually appears.
func parseArgvTokens(chain []ResolvedCommand, argv []string, stdin io.Reader) (*parsedInputs, error) {
	store := &parsedInputs{
		scopes:  make([]scopeInputs, len(chain)),
		argvSet: make([]map[string]bool, len(chain)),
	}
	leaf := len(chain) - 1 // chain index of the leaf command
	depth := 1             // index of the next chain frame we might descend into
	startedArgs := false   // a positional has been seen: command descent is over
	terminated := false    // "--" has been seen: flag parsing is over too

	addFlag := func(idx int, fd FlagDef, value, typed string) error {
		return store.recordArgvFlag(idx, fd, value, typed, stdin)
	}
	addArg := func(value string) error {
		return store.recordArg(chain[leaf], leaf, value)
	}

	// Passthrough: once the chain's passthrough leaf has been entered, every
	// remaining token — flag-shaped, "--", anything — is a raw positional.
	passthrough := func() bool {
		return chain[len(chain)-1].Passthrough && depth == len(chain)
	}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		if passthrough() {
			if err := addArg(tok); err != nil {
				return nil, err
			}
			continue
		}

		if !terminated && tok == "--" { // explicit end of flags; the rest are
			terminated = true  // positional, even flag-looking tokens (and any
			startedArgs = true // further "--" is a literal positional)
			continue
		}

		if !terminated && isFlag(tok) {
			// Only the commands reached so far are eligible: a flag typed before a sub-command's
			// name belongs to one of its ancestors, never to it. (Searching the whole chain let
			// `app --store x set` bind set's own --store.)
			extra, err := consumeFlagToken(chain[:depth], tok, argv, i, addFlag)
			if err != nil {
				return nil, err
			}
			i += extra
			noteDetached(store, chain[:depth], tok, argv, i)
			continue
		}

		// A non-flag token: consume it as the next command in the chain if it is
		// one, otherwise it (and everything after) is a positional of the leaf.
		if !startedArgs && depth < len(chain) {
			if c, ok := findChild(chain[depth-1], tok); ok && c.Name == chain[depth].Name {
				depth++
				continue
			}
		}
		startedArgs = true
		if err := addArg(tok); err != nil {
			return nil, err
		}
	}

	return store, nil
}

// noteDetached records, the first time it happens, a bare optional-value flag followed by a word
// that would have been a valid value for it: the word will be a positional, and if that turns out
// to be one too many, the error says what the user probably meant. See parsedInputs.detached.
func noteDetached(store *parsedInputs, chain []ResolvedCommand, tok string, argv []string, i int) {
	if store.detached != nil || strings.Contains(tok, "=") || i+1 >= len(argv) {
		return
	}
	fd, _, ok := findFlagIndex(chain, tok)
	next := argv[i+1]
	if !ok || fd.ImplicitValue == "" || isFlag(next) || next == "--" {
		return
	}
	if len(fd.Enum) > 0 && !enumHas(fd.Enum, next, fd.IgnoreCase) {
		return
	}
	store.detached = &[2]string{tok, next}
}

// checkFlagValues checks one flag's argv values against its enum, its key=value shape when it
// is a map, and its constraints. label is the flag as the user typed it.
func checkFlagValues(fd FlagDef, label string, vals []string) error {
	for _, v := range vals {
		if len(fd.Enum) > 0 && !enumHas(fd.Enum, v, fd.IgnoreCase) {
			return &ParseError{
				Kind:       ParseKindEnumViolation,
				Msg:        fmt.Sprintf("invalid value %q for %s (one of: %s)", redactValue(v, fd.Secret), label, strings.Join(fd.Enum, ", ")),
				Flag:       label,
				Token:      redactValue(v, fd.Secret),
				Candidates: fd.Enum,
			}
		}
		if isMapType(fd.Type) && !strings.Contains(v, "=") {
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s expects key=value pairs (got %q)", label, redactValue(v, fd.Secret)), Flag: label}
		}
	}
	return checkConstraints(label, fd.Type, fd.Constraints, vals, fd.Secret)
}

// strayCommand reports a positional on a command that branches but takes no arguments: a
// mistyped sub-command. The error carries the sibling vocabulary for a [Suggestor].
func strayCommand(leaf ResolvedCommand, si scopeInputs, store *parsedInputs) error {
	if len(leaf.Commands) == 0 || len(leaf.Arguments) > 0 || len(si.args) == 0 {
		return nil
	}
	tok := si.args[0]
	return &ParseError{
		Kind:       ParseKindUnknownCommand,
		Msg:        fmt.Sprintf("unknown command %q for %q", tok, leaf.Name) + store.detachedHint(si.args[:1]),
		Command:    leaf.Name,
		Token:      tok,
		Candidates: childCommandNames(leaf),
	}
}

// extraPositionals reports positionals the leaf has no argument for. With no variadic argument
// to absorb them, extra positionals are a usage error rather than a silent drop.
func extraPositionals(leaf ResolvedCommand, si scopeInputs, store *parsedInputs) error {
	n := len(leaf.Arguments)
	if hasVariadicArg(leaf.Arguments) || len(si.args) <= n {
		return nil
	}
	hint := store.detachedHint(si.args[n:]) // only a word that is one too many
	if n == 0 {
		return &ParseError{Kind: ParseKindNoArguments, Msg: fmt.Sprintf("%q takes no arguments (got %d)", leaf.Name, len(si.args)) + hint, Command: leaf.Name}
	}
	return &ParseError{Kind: ParseKindTooManyArguments, Msg: fmt.Sprintf("%q accepts at most %d %s (got %d)", leaf.Name, n, plural("argument", n), len(si.args)) + hint, Command: leaf.Name}
}

// validate enforces the declarative constraints on the resolved chain against a parsed store:
// a stray positional on a branch-only command is a mistyped sub-command, required inputs must
// be present or defaulted, and any value must fall inside a declared enum.
func validate(chain []ResolvedCommand, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]
	leafCovered := store.covers(len(chain) - 1)

	if leafCovered {
		if err := strayCommand(leaf, si, store); err != nil {
			return err
		}
	}

	if err := requiredErrors(chain, store); err != nil {
		return err
	}

	for i, f := range chain {
		if !store.covers(i) {
			continue
		}
		fsi := store.scopes[i]
		for _, fd := range f.Flags {
			if err := checkFlagValues(fd, fsi.label(fd), fsi.flags[fd.Name]); err != nil {
				return err
			}
		}
	}

	if !leafCovered {
		return nil
	}
	if err := extraPositionals(leaf, si, store); err != nil {
		return err
	}

	for i, ad := range leaf.Arguments {
		var vals []string
		switch {
		case ad.Variadic:
			if i < len(si.args) {
				vals = si.args[i:]
			} // an absent variadic still gets a MinItems check below
		case i < len(si.args):
			vals = si.args[i : i+1]
		default:
			continue // a non-variadic argument that was not provided — requiredErrors covers absence
		}
		for _, v := range vals {
			if len(ad.Enum) > 0 && !enumHas(ad.Enum, v, ad.IgnoreCase) {
				return &ParseError{
					Kind:       ParseKindEnumViolation,
					Msg:        fmt.Sprintf("invalid value %q for <%s> (one of: %s)", redactValue(v, ad.Secret), ad.Name, strings.Join(ad.Enum, ", ")),
					Token:      redactValue(v, ad.Secret),
					Candidates: ad.Enum,
				}
			}
		}
		if err := checkConstraints("<"+ad.Name+">", ad.Type, ad.Constraints, vals, ad.Secret); err != nil {
			return err
		}
	}
	return nil
}

// hasVariadicArg reports whether any of a command's arguments is variadic — when one
// is, it slurps every trailing positional, so no "too many arguments" can occur.
func hasVariadicArg(args []ArgDef) bool {
	for _, a := range args {
		if a.Variadic {
			return true
		}
	}
	return false
}

// checkConstraints enforces an input's declared bounds against the values supplied for it —
// one for a scalar, possibly many for a repeatable flag or variadic argument. Numeric bounds
// apply to the int/uint/float family, length and pattern to strings, item counts to
// arrays/maps; for a repeatable input the per-value checks apply to each element. A constraint
// on a type none of those fit cannot come from a valid spec, so a hand-built Definition that
// does it anyway is skipped rather than guessed at. A secret input's value and length are
// redacted in the error.
func checkConstraints(label, typ string, c Constraints, values []string, secret bool) error {
	if isArrayType(typ) || isMapType(typ) {
		if err := checkItemCount(label, c, len(values)); err != nil {
			return err
		}
	}
	elem := constraintElemType(typ)
	for _, v := range values {
		var err error
		switch {
		case isNumericType(elem):
			err = checkNumericBounds(label, c, v, secret, parseNumber, formatNum)
		case measuredTypes[elem] != nil:
			m := measuredTypes[elem]
			err = checkNumericBounds(label, c, v, secret, m.parse, m.format)
		case isPathType(elem):
			// A path is still a string, so its declared length and pattern bounds
			// apply — `pattern: '\.ya?ml$'` on a config path is a reasonable thing to
			// want, and skipping them here would silently ignore a declared
			// constraint, which is the one outcome validation exists to prevent.
			if err = checkStringBounds(label, c, v, secret); err == nil {
				err = checkPathExists(label, elem, v)
			}
		case elem == "string":
			err = checkStringBounds(label, c, v, secret)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// isPathType reports whether typ is one of the two filesystem types, whose contract is
// "this path exists, and is the right kind of thing". The generated field is a plain string;
// the type name is what tells the parser to check it.
func isPathType(typ string) bool { return typ == "existingfile" || typ == "existingdir" }

// checkPathExists enforces an existingfile/existingdir type at PARSE time, where the message
// can name the flag the user typed, rather than three layers into a handler as an *os.PathError
// naming only a path.
//
// It is deliberately only an existence-and-kind check. Expanding "~", cleaning, resolving
// symlinks and deciding whether a missing file should be created are POLICY, and policy
// belongs to the handler; whether a path is there is a fact.
func checkPathExists(label, typ, value string) error {
	info, err := os.Stat(value)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		noun := "file"
		if typ == "existingdir" {
			noun = "directory"
		}
		return &ParseError{
			Kind: ParseKindInvalidValue,
			Msg:  fmt.Sprintf("%s: no such %s: %q", label, noun, value),
			Flag: label,
		}
	case err != nil:
		// Non-leaky: a permission or I/O failure names the path and the problem in
		// rotini's own words, never the raw OS error text.
		return &ParseError{
			Kind: ParseKindInvalidValue,
			Msg:  fmt.Sprintf("%s: cannot read %q", label, value),
			Flag: label,
		}
	case typ == "existingfile" && info.IsDir():
		return &ParseError{
			Kind: ParseKindInvalidValue,
			Msg:  fmt.Sprintf("%s: %q is a directory, not a file", label, value),
			Flag: label,
		}
	case typ == "existingdir" && !info.IsDir():
		return &ParseError{
			Kind: ParseKindInvalidValue,
			Msg:  fmt.Sprintf("%s: %q is not a directory", label, value),
			Flag: label,
		}
	}
	return nil
}

// constraintViolation builds the one error shape every bound check returns.
func constraintViolation(format string, args ...any) error {
	return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf(format, args...)}
}

// checkItemCount enforces the collection-level bounds — how many values an array or
// map input may carry, as opposed to what each value must be.
func checkItemCount(label string, c Constraints, n int) error {
	switch {
	case c.MinItems > 0 && n < c.MinItems:
		return constraintViolation("%s needs at least %d %s (got %d)", label, c.MinItems, plural("value", c.MinItems), n)
	case c.MaxItems > 0 && n > c.MaxItems:
		return constraintViolation("%s accepts at most %d %s (got %d)", label, c.MaxItems, plural("value", c.MaxItems), n)
	}
	return nil
}

// checkNumericBounds enforces the numeric bounds on one value. An unparseable value is
// skipped: coerce already reports it, and reporting it twice would be noise.
func checkNumericBounds(label string, c Constraints, v string, secret bool, parse func(string) (float64, bool), formatNum func(float64) string) error {
	n, numeric := parse(v)
	if !numeric {
		return nil
	}
	got := redactValue(v, secret)
	switch {
	case c.Minimum != nil && n < *c.Minimum:
		return constraintViolation("%s must be >= %s (got %s)", label, formatNum(*c.Minimum), got)
	case c.Maximum != nil && n > *c.Maximum:
		return constraintViolation("%s must be <= %s (got %s)", label, formatNum(*c.Maximum), got)
	case c.ExclusiveMinimum != nil && n <= *c.ExclusiveMinimum:
		return constraintViolation("%s must be > %s (got %s)", label, formatNum(*c.ExclusiveMinimum), got)
	case c.ExclusiveMaximum != nil && n >= *c.ExclusiveMaximum:
		return constraintViolation("%s must be < %s (got %s)", label, formatNum(*c.ExclusiveMaximum), got)
	case c.MultipleOf != nil && !isMultipleOf(n, *c.MultipleOf):
		return constraintViolation("%s must be a multiple of %s (got %s)", label, formatNum(*c.MultipleOf), got)
	}
	return nil
}

// parseNumber reports whether v is a number, and its value.
func parseNumber(v string) (float64, bool) {
	n, err := strconv.ParseFloat(v, 64)
	return n, err == nil
}

// checkStringBounds enforces the length and pattern bounds on one value. A secret's length is
// redacted — the length alone leaks something about a credential.
func checkStringBounds(label string, c Constraints, v string, secret bool) error {
	ln := utf8.RuneCountInString(v)
	gotLen := strconv.Itoa(ln)
	if secret {
		gotLen = "[redacted]"
	}
	switch {
	case c.MinLength > 0 && ln < c.MinLength:
		return constraintViolation("%s must be at least %d %s long (got %s)", label, c.MinLength, plural("character", c.MinLength), gotLen)
	case c.MaxLength > 0 && ln > c.MaxLength:
		return constraintViolation("%s must be at most %d %s long (got %s)", label, c.MaxLength, plural("character", c.MaxLength), gotLen)
	}
	if c.Pattern != "" {
		if ok, err := regexp.MatchString(c.Pattern, v); err == nil && !ok {
			if c.PatternMessage != "" {
				return constraintViolation("%s %s (got %q)", label, c.PatternMessage, redactValue(v, secret))
			}
			return constraintViolation("%s must match %s (got %q)", label, c.Pattern, redactValue(v, secret))
		}
	}
	return nil
}

// isMultipleOf reports whether n is an integer multiple of m, with a small relative tolerance
// for float representation so 1.2 / 0.1 counts.
func isMultipleOf(n, m float64) bool {
	if m <= 0 {
		return false
	}
	q := n / m
	return math.Abs(q-math.Round(q)) < 1e-9
}

// redactValue returns "[redacted]" for a secret input, else the value unchanged — so a
// secret flag/argument's value never appears in usage or validation errors.
func redactValue(v string, secret bool) string {
	if secret {
		return "[redacted]"
	}
	return v
}

// validateFlagGroups enforces each command's cross-flag presence rules. Set means explicitly
// provided on argv — a default or fallback does not count — read from the store's argvSet,
// which unlike a raw argv re-scan also sees flags set inside short clusters.
func validateFlagGroups(chain []ResolvedCommand, store *parsedInputs) error {
	for i, f := range chain {
		if !store.covers(i) {
			continue
		}
		for _, g := range f.FlagGroups {
			all := make([]string, 0, len(g.Flags))
			var set []string
			for _, name := range g.Flags {
				label := "--" + name
				if fd, ok := findFlagDef(f.Flags, name); ok {
					label = flagLabel(fd)
				}
				all = append(all, label)
				if store.setOnArgv(i, name) {
					set = append(set, label)
				}
			}
			if err := checkFlagGroup(g.Kind, set, all); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateFlagDependencies enforces each command's conditional cross-flag requirements: when a
// dependency's When flag is set on argv, every flag it Requires must be too. Set follows the
// same explicit-argv convention as flag groups.
func validateFlagDependencies(chain []ResolvedCommand, store *parsedInputs) error {
	for i, f := range chain {
		if !store.covers(i) {
			continue
		}
		for _, dep := range f.FlagDependencies {
			whenFD, ok := findFlagDef(f.Flags, dep.When)
			if !ok {
				continue // unknown trigger flag (a spec lint rejects this) — nothing to enforce
			}
			whenLabel := flagLabel(whenFD)
			if !store.setOnArgv(i, dep.When) {
				continue // the trigger is absent — the requirement does not apply
			}
			var missing []string
			for _, name := range dep.Requires {
				label := "--" + name
				if fd, ok := findFlagDef(f.Flags, name); ok {
					if store.setOnArgv(i, name) {
						continue
					}
					label = flagLabel(fd)
				}
				missing = append(missing, label)
			}
			if len(missing) > 0 {
				noun, verb := "flag", "is"
				if len(missing) > 1 {
					noun, verb = "flags", "are"
				}
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s %s %s required when %s is set", noun, joinAnd(missing), verb, whenLabel)}
			}
		}
	}
	return nil
}

// checkFlagGroup applies one group's rule given the labels set and the full membership.
func checkFlagGroup(kind FlagGroupKind, set, all []string) error {
	switch kind {
	case FlagGroupMutuallyExclusive:
		if len(set) > 1 {
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: "flags " + joinAnd(set) + " are mutually exclusive"}
		}
	case FlagGroupRequiredTogether:
		if n := len(set); n > 0 && n < len(all) {
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: "flags " + strings.Join(all, ", ") + " must be used together"}
		}
	case FlagGroupOneOf:
		switch {
		case len(set) == 0:
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: "exactly one of " + strings.Join(all, ", ") + " is required"}
		case len(set) > 1:
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: "flags " + joinAnd(set) + " are mutually exclusive"}
		}
	case FlagGroupAtLeastOne:
		if len(set) == 0 {
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: "at least one of " + strings.Join(all, ", ") + " is required"}
		}
	}
	return nil
}

// joinAnd formats a list as "a", "a and b", or "a, b, and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

// numericFamily is every resolved type string whose values are range-checkable numbers — the
// int/uint/float vocabulary plus the JSON Schema aliases, since a hand-built Definition may
// use either spelling.
var numericFamily = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"integer": true, "number": true,
}

func isNumericType(typ string) bool { return numericFamily[typ] }

// measured is a type whose values are quantities in a unit of their own — a duration in
// nanoseconds, a size in bytes — so numeric bounds apply once a value is read in that unit, and
// print back in the type's spelling ("must be <= 30d", "<= 1Gi").
type measured struct {
	parse  func(string) (float64, bool)
	format func(float64) string
}

// measuredTypes are the non-numeric types numeric bounds apply to, by definition type name.
var measuredTypes = map[string]*measured{
	"time.Duration": {
		parse: func(s string) (float64, bool) {
			d, err := parseDuration(strings.TrimSpace(s))
			return float64(d), err == nil
		},
		format: func(f float64) string { return formatDuration(time.Duration(f)) },
	},
	"rotini.ByteSize": {
		parse: func(s string) (float64, bool) {
			n, err := parseByteSize(s)
			return float64(n), err == nil
		},
		format: func(f float64) string { return ByteSize(int64(f)).String() },
	},
}

// constraintElemType is the type a constraint's per-value checks apply to: the element type
// for a repeatable input, the type itself otherwise. Item-count bounds stay on the collection.
func constraintElemType(typ string) string {
	return strings.TrimPrefix(typ, "[]")
}

func isArrayType(typ string) bool { return strings.HasPrefix(typ, "[]") }

// isMapType reports whether typ is a map type ("map[...]…"), whose flag takes repeated
// key=value pairs.
func isMapType(typ string) bool { return strings.HasPrefix(typ, "map[") }

// formatNum renders a numeric bound without a trailing ".000…".
func formatNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// applyDefaults fills in declared flag and trailing-argument defaults for inputs
// the user did not provide, so handlers and required-checks see them.
// flagDefaults is the occurrences a flag's declared default seeds when the user supplied
// none: every element of Defaults for a repeatable input, or the single Default. Both empty
// means the flag has no default and is left unset, which is how "absent" stays distinguishable
// from "explicitly empty".
//
// Default wins when both are set, so a Definition that somehow carries both is not ambiguous.
// The spec cannot produce that — a default is a scalar or a list, never both — but a
// hand-built Definition can.
func flagDefaults(fd FlagDef) []string {
	if fd.Default != "" {
		return []string{fd.Default}
	}
	if len(fd.Defaults) == 0 {
		return nil
	}
	return slices.Clone(fd.Defaults)
}

func applyDefaults(chain []ResolvedCommand, store *parsedInputs) {
	for i, f := range chain {
		for _, fd := range f.Flags {
			seed := flagDefaults(fd)
			if len(seed) == 0 {
				continue
			}
			if store.scopes[i].flags == nil {
				store.scopes[i].flags = map[string][]string{}
			}
			if _, ok := store.scopes[i].flags[fd.Name]; !ok {
				store.scopes[i].flags[fd.Name] = seed
			}
		}
	}

	leaf := len(chain) - 1
	args := chain[leaf].Arguments
	for i := len(store.scopes[leaf].args); i < len(args); i++ {
		if args[i].Default == "" {
			break // can't fill a gap before a defaultless argument
		}
		store.scopes[leaf].args = append(store.scopes[leaf].args, args[i].Default)
	}
}

// requiredErrors reports any required flags or arguments (across the resolved
// chain / on the leaf) that were neither provided nor defaulted.
func requiredErrors(chain []ResolvedCommand, store *parsedInputs) error {
	var missing []string
	for i, f := range chain {
		if !store.covers(i) {
			continue
		}
		si := store.scopes[i]
		for _, fd := range f.Flags {
			if fd.Required {
				if _, ok := si.flags[fd.Name]; !ok {
					missing = append(missing, flagLabel(fd))
				}
			}
		}
	}
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]
	for i, ad := range leaf.Arguments {
		if store.covers(len(chain)-1) && ad.Required && i >= len(si.args) {
			missing = append(missing, "<"+ad.Name+">")
		}
	}
	if len(missing) > 0 {
		return &ParseError{Kind: ParseKindMissingRequired, Msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

// flagLabel names a flag when nothing the user typed can: its first long identifier ("--due"
// reads better than "-d"), else its first identifier, else --<name>.
func flagLabel(f FlagDef) string {
	for _, id := range f.Identifiers {
		if strings.HasPrefix(id, "--") {
			return id
		}
	}
	if len(f.Identifiers) > 0 {
		return f.Identifiers[0]
	}
	return "--" + f.Name
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// resolveFlagValue applies a flag's declared acquisition modes to one argv-supplied value.
// With "file", a value starting with '@' becomes the named file's contents; with "stdin", a
// value of exactly "-" becomes the piped stdin, which must not be empty. Resolved text has one
// trailing line ending removed (see trimAcquiredPayload — the same rule the stdin channel
// uses) and then flows through the same coercion and validation as a literal value. Without the matching mode, '@' and '-' are ordinary characters, and defaults and
// fallbacks never resolve — sentinels are argv grammar.
func resolveFlagValue(fd FlagDef, label, value string, stdin io.Reader) (string, error) {
	switch {
	case strings.HasPrefix(value, "@") && slices.Contains(fd.From, "file"):
		data, err := os.ReadFile(value[1:])
		if err != nil {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: cannot read %q: %v", label, value, err),
				Flag: label, Token: value,
			}
		}
		return trimAcquiredPayload(string(data)), nil
	case value == "-" && slices.Contains(fd.From, "stdin"):
		data, err := readStdin(stdin)
		if err != nil {
			return "", &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: read stdin: %v", label, err), Flag: label}
		}
		if len(data) == 0 {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: stdin is empty — %q asks for a piped value", label, "-"),
				Flag: label,
			}
		}
		return trimAcquiredPayload(string(data)), nil
	}
	return value, nil
}

// isShortCluster reports whether name is a candidate POSIX short-flag cluster: a single-dash
// token of more than one character. It is tried only after an exact-identifier lookup misses.
func isShortCluster(name string) bool {
	return len(name) > 2 && name[0] == '-' && name[1] != '-'
}

// parseCluster expands a POSIX short-flag cluster against the chain. Each character is one
// short flag: booleans are set in turn, and the first value-taking flag consumes the rest of
// the cluster, else the inline "=value", else the next argv token. It returns how many extra
// argv tokens it consumed.
func parseCluster(chain []ResolvedCommand, body, inline string, hasInline bool, argv []string, i int, addFlag func(idx int, fd FlagDef, value, typed string) error) (int, error) {
	for k := range len(body) {
		short := "-" + body[k:k+1]
		fdef, idx, ok := findFlagIndex(chain, short)
		if !ok {
			return 0, &ParseError{Kind: ParseKindUnknownFlag, Msg: fmt.Sprintf("unknown flag %q", short), Flag: short, Token: short}
		}
		if fdef.Type == "bool" || fdef.Type == "count" {
			v := "true"
			if fdef.Type == "count" {
				v = "1"
			}
			if err := addFlag(idx, fdef, v, short); err != nil {
				return 0, err
			}
			continue
		}
		// A value-taking flag ends the cluster: its value is whatever follows.
		switch rest := body[k+1:]; {
		case rest != "":
			return 0, addFlag(idx, fdef, rest, short)
		case hasInline:
			return 0, addFlag(idx, fdef, inline, short)
		case fdef.ImplicitValue != "":
			return 0, addFlag(idx, fdef, fdef.ImplicitValue, short)
		default:
			if i+1 >= len(argv) {
				return 0, &ParseError{Kind: ParseKindNeedsValue, Msg: fmt.Sprintf("flag %q needs a value", short), Flag: short}
			}
			return 1, addFlag(idx, fdef, argv[i+1], short)
		}
	}
	// Every flag in the cluster was boolean; a trailing "=value" has nothing to bind.
	if hasInline {
		return 0, &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("flag %q does not take a value", "-"+body), Flag: "-" + body}
	}
	return 0, nil
}

// findFlagIndex searches the chain leaf→root for a flag whose identifiers include name,
// returning its definition and the owning command's chain index.
func findFlagIndex(chain []ResolvedCommand, name string) (FlagDef, int, bool) {
	f, i, _, ok := findFlagMatch(chain, name)
	return f, i, ok
}

// findFlagMatch is findFlagIndex plus whether name matched a NEGATED form ("--no-color" for a
// negatable "--color"). A declared identifier always wins over a negated one, so an author who
// genuinely declares "--no-cache" keeps it.
func findFlagMatch(chain []ResolvedCommand, name string) (def FlagDef, idx int, negated, ok bool) {
	for i, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if slices.Contains(f.Identifiers, name) {
				return f, i, false, true
			}
		}
	}
	for i, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if f.Negatable && slices.Contains(negatedIdentifiers(f), name) {
				return f, i, true, true
			}
		}
	}
	return FlagDef{}, -1, false, false
}

// negatedIdentifiers returns the "--no-<x>" form of each LONG identifier of a negatable flag.
// Short identifiers get none: "-no-c" is not a thing, and "-C" would be an invention rotini
// has no business making on the author's behalf.
func negatedIdentifiers(f FlagDef) []string {
	if !f.Negatable {
		return nil
	}
	var out []string
	for _, id := range f.Identifiers {
		if name, ok := strings.CutPrefix(id, "--"); ok {
			out = append(out, "--no-"+name)
		}
	}
	return out
}

// childCommandNames is the dispatchable vocabulary of a command's visible children, for a
// mistyped-command [ParseError]'s Candidates.
func childCommandNames(cur ResolvedCommand) []string {
	var names []string
	for _, c := range cur.Commands {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	for _, r := range cur.Remotes {
		names = append(names, r.Name)
		names = append(names, r.Aliases...)
	}
	return names
}

// chainFlagIdentifiers is the non-hidden flag vocabulary of the whole chain, for an
// unknown-flag [ParseError]'s Candidates.
func chainFlagIdentifiers(chain []ResolvedCommand) []string {
	var ids []string
	for _, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if !f.Hidden {
				ids = append(ids, f.Identifiers...)
				ids = append(ids, negatedIdentifiers(f)...)
			}
		}
	}
	return ids
}

// bindInputs fills a <Cmd>Inputs struct, one field per command on the resolved path in
// root→leaf order. Fields bind to the tail of the chain aligned at the leaf, so each command's
// inputs come from the right frame however deep it was reached; extra parent frames from a
// statically-composed subtree simply go unbound.
func bindInputs(v reflect.Value, p *parsedInputs, chain []ResolvedCommand, offset int) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	if offset < 0 || offset+v.NumField() > len(p.scopes) {
		// Not an error HERE. bindInputs runs before argv is validated, and a chain that came
		// up short is usually a symptom — an unknown command, a stray positional — whose own
		// diagnostic is far better than anything this could say. checkFrameFit reports the
		// genuine misfit after those have had their turn.
		return nil
	}
	if err := checkChainAlignment(v, chain, offset); err != nil {
		return err
	}
	for i := range v.NumField() {
		if err := bindCommandInputs(v.Field(i), p.scopes[offset+i], chain[offset+i]); err != nil {
			return err
		}
	}
	return nil
}

// frameAnchor is the chain index that field 0 of an inputs struct maps to.
//
// An inputs struct's LAST field describes the command whose handler is collecting it, and the
// fields before it describe that command's ancestors, in order. So the anchor counts back from
// the CALLER'S OWN FRAME:
//
//	offset = self - n + 1
//
// self is [Context.Frame]'s index — the command whose hook is running, which the lifecycle
// records for every step (see [AtFrame]). For a leaf hook self is the last frame and this
// reduces to len(chain) - n, which is what the anchor used to be for every hook.
//
// It used to be that, for every hook, plus a boolean that forced index 0. Neither input
// identified the caller, so the anchor was inferred from the struct's SHAPE — and a shorter
// struct always aligns against something. A cascading hook on a middle frame got a descendant's
// flags, and a composed child's cascading hook could not read its own flags at all: its type
// spans only its own lineage, so leaf-anchoring landed below it and root-anchoring landed above
// it. Both returned zeros and a nil error.
//
// There is no longer a way to pin the anchor at index 0. Root-anchoring existed for CollectRoot,
// which frames replaced; BindRoot outlived it by one release as "the low-level escape", with no
// caller and no test, and it was the only remaining route to the silent wrong-frame answer this
// function exists to prevent. A caller who genuinely wants the first n frames has
// [Context.Chain].
func frameAnchor(v reflect.Value, chain []ResolvedCommand, self int) int {
	if v.Kind() != reflect.Struct {
		return 0
	}
	if self < 0 || self >= len(chain) {
		self = len(chain) - 1
	}
	return self - v.NumField() + 1
}

// checkFrameFit rejects an inputs struct that cannot sit on the chain at the caller's own frame
// — it describes more ancestors than the running command has.
//
// This is an EXACT check rather than a heuristic, and it is only possible because the anchor is
// now derived from the running frame instead of the struct's shape. It catches the mistake that
// used to be entirely invisible: collecting a DESCENDANT's inputs type, which needs more
// ancestors than the caller has. Under leaf-anchoring a struct of any size found somewhere to
// sit, and an over-long one was skipped in silence and came back zeroed.
//
// It is called after argv has been parsed and validated, so a short chain caused by a bad
// command line reports that instead of this.
func checkFrameFit(v reflect.Value, chain []ResolvedCommand, self int) error {
	if v.Kind() != reflect.Struct || len(chain) == 0 {
		return nil
	}
	if self < 0 || self >= len(chain) {
		self = len(chain) - 1
	}
	if n := v.NumField(); n > self+1 {
		return &ParseError{
			Kind: ParseKindInternal,
			Msg: fmt.Sprintf(
				"rotini: %s describes %d commands but %q is only %d deep: an inputs type covers a "+
					"command and its ancestors, so a handler collects the type generated for ITS OWN "+
					"command — a descendant's type cannot be collected from a shallower hook",
				displayTypeName(v.Type()), n, pathOf(chain[:self+1]), self+1),
		}
	}
	return nil
}

// checkChainAlignment rejects an inputs struct that does not describe the running command.
//
// Fields map to chain frames by position, aligned at the LEAF — field i of an n-field struct
// takes chain[len(chain)-n+i]. That is what lets a composed child's handler collect its own
// short type (GrandInputs has one field; the chain is root→child→grand) without knowing which
// parent tree it was mounted into.
//
// The cost is that a SHORTER type always aligns against something. Collecting an ancestor's
// type from a deeper run — Collect[MigInputs] while `mig db status` runs — mapped Mig onto the
// status frame and filled it from status's flags. The compiler is happy, the binder is happy,
// and every field comes back zero (or, if the two commands happen to share a flag name, comes
// back holding the WRONG command's value, which is worse). There is no signal at all.
//
// The test is DISJOINTNESS, not containment: a field that declares flags, landing on a frame
// that declares flags, sharing not one name between them, is not describing that command.
// Containment would be the stronger claim and is wrong — a hand-built struct may carry fields
// for flags a particular Definition omits, and those simply stay zero. Overlap of even one
// name means the struct is talking about this command, which is all that is being asked.
//
// Only flags are checked. They are named and unordered, so a mismatch is unambiguous;
// positional arguments carry no names to compare.
func checkChainAlignment(v reflect.Value, chain []ResolvedCommand, offset int) error {
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
		if !flags.IsValid() || flags.NumField() == 0 {
			continue
		}
		frame := chain[offset+i]
		if frame.Name == "" && len(frame.Flags) == 0 {
			continue // an anonymous frame (a hand-built chain) declares nothing to check against
		}
		ft := flags.Type()
		for j := range flags.NumField() {
			name := ft.Field(j).Tag.Get("rotini")
			if name == "" {
				continue
			}
			// Every flag the type names must be one the command it maps onto declares. The
			// check was once "the two share SOME flag" — and every command shares `help`, so an
			// ancestor's type collected from a descendant bound partially: fallbacks filled its
			// fields by key while its argv values were silently lost.
			if _, ok := findFlagDef(frame.Flags, name); ok {
				continue
			}
			return &ParseError{
				Kind: ParseKindInternal,
				Msg: fmt.Sprintf(
					"rotini: %s does not describe the running command %q: its %s field maps to %q, which declares no flag %q. "+
						"An inputs type binds to the END of the command chain, so a handler collects the type generated for ITS OWN "+
						"command; an ancestor's type cannot be collected from a deeper command",
					displayTypeName(v.Type()), pathOf(chain), v.Type().Field(i).Name, frame.Name, name),
			}
		}
	}
	return nil
}

// displayTypeName names a type for an error message, falling back to its string form for an
// anonymous struct.
func displayTypeName(t reflect.Type) string {
	if n := t.Name(); n != "" {
		return n
	}
	return t.String()
}

// pathOf renders a resolved chain as the command path the user typed.
func pathOf(chain []ResolvedCommand) string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.Name
	}
	return strings.Join(names, " ")
}

// bindCommandInputs fills a <Cmd>CommandInputs struct's Flags and Arguments.
func bindCommandInputs(v reflect.Value, si scopeInputs, frame ResolvedCommand) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := range v.NumField() {
		switch t.Field(i).Name {
		case "Flags":
			if err := bindFlags(v.Field(i), si, frame.Flags); err != nil {
				return err
			}
		case "Arguments":
			if err := bindArgs(v.Field(i), si.args, frame.Arguments); err != nil {
				return err
			}
		}
	}
	return nil
}

// bindFlags fills a <Cmd>Flags struct by matching each field's `rotini:"<name>"` tag against
// the parsed values, surfacing a coercion failure as a usage error naming the flag.
func bindFlags(v reflect.Value, si scopeInputs, defs []FlagDef) error {
	flags := si.flags
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := range v.NumField() {
		name := t.Field(i).Tag.Get("rotini")
		if name == "" {
			continue
		}
		raw, ok := flags[name]
		if !ok {
			continue
		}
		if def, ok := findFlagDef(defs, name); ok {
			switch {
			case def.DottedKeys:
				if err := coerceMapDotted(v.Field(i), raw); err != nil {
					return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %s", flagErrLabel(si, defs, name), coerceMessage(err, def.Secret)), Flag: flagErrLabel(si, defs, name)}
				}
				continue
			case isObjectFlag(def):
				if err := bindObjectFlag(v.Field(i), raw, def); err != nil {
					return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", flagErrLabel(si, defs, name), err), Flag: flagErrLabel(si, defs, name)}
				}
				continue
			case def.Type == "count":
				// Each argv occurrence appended one marker; the field is the tally.
				if f := v.Field(i); f.CanSet() && f.Kind() == reflect.Int {
					f.SetInt(int64(len(raw)))
				}
				continue
			}
		}
		if def, ok := findFlagDef(defs, name); ok && def.IgnoreCase {
			raw = canonicalEnum(def.Enum, raw)
		}
		layout := ""
		if def, ok := findFlagDef(defs, name); ok {
			layout = def.Layout
		}
		if err := coerceWithLayout(v.Field(i), raw, layout); err != nil {
			secret := false
			if def, ok := findFlagDef(defs, name); ok {
				secret = def.Secret
			}
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %s", flagErrLabel(si, defs, name), coerceMessage(err, secret)), Flag: flagErrLabel(si, defs, name)}
		}
	}
	return nil
}

// argSecret reports whether the positional at index i is declared secret. Arguments are
// positional, so the struct field's index IS the declaration index.
func argSecret(defs []ArgDef, i int) bool {
	return i < len(defs) && defs[i].Secret
}

// labelForFlag is a flag's CLI label (its identifiers) for error messages, falling back
// to the logical name when the definition isn't found.
// flagErrLabel names flag name in a binding error: as typed on argv, else as [labelForFlag].
func flagErrLabel(si scopeInputs, defs []FlagDef, name string) string {
	if t := si.typed[name]; t != "" {
		return t
	}
	return labelForFlag(defs, name)
}

func labelForFlag(defs []FlagDef, name string) string {
	for _, d := range defs {
		if d.Name == name {
			return flagLabel(d)
		}
	}
	return name
}

// bindArgs fills a <Cmd>Arguments struct positionally; a trailing []string field is variadic
// and absorbs the remaining positionals.
func bindArgs(v reflect.Value, args []string, defs []ArgDef) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	idx := 0
	for i := range v.NumField() {
		f := v.Field(i)
		label := "<" + t.Field(i).Tag.Get("rotini") + ">"
		var def ArgDef
		if i < len(defs) {
			def = defs[i]
		}
		if f.Kind() == reflect.Slice { // a slice argument is variadic, whatever its element type
			if err := coerceWithLayout(f, canonicalFor(def, args[min(idx, len(args)):]), def.Layout); err != nil {
				return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %s", label, coerceMessage(err, argSecret(defs, i)))}
			}
			idx = len(args)
			continue
		}
		if idx < len(args) {
			if err := coerceWithLayout(f, canonicalFor(def, args[idx:idx+1]), def.Layout); err != nil {
				return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %s", label, coerceMessage(err, argSecret(defs, i)))}
			}
			idx++
		}
	}
	return nil
}

// splitValue splits one value on a list input's separator, CSV-style: an item in double quotes
// keeps the separator ("a,b"), leading spaces are trimmed, and an empty value is no items — so
// `--tags ""` clears to an empty list. With no separator the value is one item, untouched.
func splitValue(value, sep string) ([]string, error) {
	if sep == "" {
		return []string{value}, nil
	}
	if value == "" {
		return nil, nil
	}
	r := csv.NewReader(strings.NewReader(value))
	r.Comma, _ = utf8.DecodeRuneInString(sep)
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	items, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("could not split %q on %q: %w", value, sep, err)
	}
	return items, nil
}

// variadicAt returns the variadic argument that positional number pos lands in, if any.
// Positionals fill in declaration order and a variadic, always last, takes the rest.
func variadicAt(args []ArgDef, pos int) (ArgDef, bool) {
	for i, a := range args {
		if a.Variadic && pos >= i {
			return a, true
		}
	}
	return ArgDef{}, false
}

// enumHas reports whether v is one of enum's members, ignoring case when asked.
func enumHas(enum []string, v string, ignoreCase bool) bool {
	if !ignoreCase {
		return slices.Contains(enum, v)
	}
	return slices.ContainsFunc(enum, func(m string) bool { return strings.EqualFold(m, v) })
}

// canonicalEnum rewrites each value that matches an enum member case-insensitively to that
// member's declared spelling, leaving any other value for validation to reject.
func canonicalEnum(enum, vals []string) []string {
	if len(enum) == 0 {
		return vals
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v
		for _, m := range enum {
			if strings.EqualFold(m, v) {
				out[i] = m
				break
			}
		}
	}
	return out
}

// canonicalFor applies an argument's case-insensitive enum, if it declares one.
func canonicalFor(def ArgDef, vals []string) []string {
	if !def.IgnoreCase {
		return vals
	}
	return canonicalEnum(def.Enum, vals)
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// coerce sets f from the raw string values, returning an error when one cannot be parsed into
// f's type — including a custom type's own [encoding.TextUnmarshaler] error. It never panics:
// an unparseable value is reported, not silently zeroed.
func coerce(f reflect.Value, raw []string) error {
	if len(raw) == 0 {
		return nil
	}
	// Checked before pointer dereferencing: *url.URL and *time.Location are built by their
	// parsers, not filled in place.
	if parse, ok := valueParsers[f.Type()]; ok {
		last := raw[len(raw)-1]
		v, err := parse(last)
		if err != nil {
			return &coerceError{Value: last, TypeName: typeLabel(f.Type()), Cause: err}
		}
		f.Set(v)
		return nil
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		return coerce(f.Elem(), raw)
	}
	last := raw[len(raw)-1]

	if f.Type() == durationType {
		d, err := parseDuration(last)
		if err != nil {
			return notValid(last, "duration")
		}
		f.SetInt(int64(d))
		return nil
	}
	if f.CanAddr() && f.Addr().Type().Implements(textUnmarshalerType) {
		if err := f.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(last)); err != nil {
			return &coerceError{Value: last, TypeName: typeLabel(f.Type()), Cause: err}
		}
		return nil
	}

	switch f.Kind() {
	case reflect.Bool:
		b, err := parseBool(last)
		if err != nil {
			return notValid(last, "boolean")
		}
		f.SetBool(b)
	case reflect.String:
		f.SetString(last)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil {
			return notValid(last, "integer")
		}
		f.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(last, 10, 64)
		if err != nil {
			return notValid(last, "non-negative integer")
		}
		f.SetUint(n)
	case reflect.Float32, reflect.Float64:
		x, err := strconv.ParseFloat(last, 64)
		if err != nil {
			return notValid(last, "number")
		}
		f.SetFloat(x)
	case reflect.Slice:
		return coerceSlice(f, raw)
	case reflect.Map:
		return coerceMap(f, raw)
	case reflect.Interface:
		if f.Type().NumMethod() != 0 {
			return unsupportedType(f.Type())
		}
		f.Set(reflect.ValueOf(last)) // an `any` input holds the raw string
	default:
		return unsupportedType(f.Type())
	}
	return nil
}

// unsupportedType is the loud refusal for a field type coerce has no rule for: silence would
// zero the field and hide a codegen mistake. The fix is to give the type an UnmarshalText.
func unsupportedType(t reflect.Type) error {
	return fmt.Errorf("cannot parse into %s — the type must implement encoding.TextUnmarshaler", t)
}

// coerceSlice fills a slice field from the raw values — one per repeated flag occurrence, or
// the trailing positionals of a variadic argument — coercing each into the element type.
func coerceSlice(f reflect.Value, raw []string) error {
	out := reflect.MakeSlice(f.Type(), len(raw), len(raw))
	for i, r := range raw {
		if err := coerce(out.Index(i), []string{r}); err != nil {
			return err
		}
	}
	f.Set(out)
	return nil
}

// coerceError is the standard "value isn't a <type>" failure, holding the offending value
// SEPARATELY from its rendering so a caller that knows the input is secret can re-render it
// redacted.
//
// It is a type rather than a formatted string because redaction has to happen where the
// FlagDef/ArgDef is in scope, and that is several frames above where coercion fails. The enum
// and constraint paths could redact inline because they already had the definition; this one
// could not, and so it leaked — a secret flag given a bad value printed the value.
type coerceError struct {
	Value    string // the raw input, unredacted
	TypeName string // what it failed to parse as
	Cause    error  // an underlying decoder error, when there was one
}

func (e *coerceError) Error() string { return e.render(false) }

func (e *coerceError) Unwrap() error { return e.Cause }

// render writes the message with the value redacted or not.
func (e *coerceError) render(secret bool) string {
	msg := fmt.Sprintf("%q is not a valid %s", redactValue(e.Value, secret), e.TypeName)
	// A parser's own message usually quotes the value it rejected ("12XB" has an unknown unit),
	// so a secret's message stops at the type: redacting the value above and then printing the
	// cause would put it straight back.
	if e.Cause != nil && !secret {
		msg += fmt.Sprintf(" (%v)", e.Cause)
	}
	return msg
}

// coerceMessage renders err for a message, redacting the offending value when the input it came
// from is secret. A non-coercion error is rendered as-is: it carries no value of its own.
func coerceMessage(err error, secret bool) string {
	var ce *coerceError
	if secret && errors.As(err, &ce) {
		return ce.render(true)
	}
	return err.Error()
}

// notValid is the standard "value isn't a <type>" coercion error.
func notValid(value, typeName string) error {
	return &coerceError{Value: value, TypeName: typeName}
}

// coerceMap fills a string-keyed map field from raw "key=value" pairs, splitting on the first
// '='. The value is coerced into the map's element type; an `any` element stores the raw
// string. Later pairs win on a duplicate key, and malformed pairs are skipped — validation
// rejects them.
// coerceMapDotted fills a map[string]any flag from "key=value" pairs whose keys are dotted
// paths into nested maps: "image.tag=v2" → m["image"].(map[string]any)["tag"] = "v2". Each
// assignment overwrites whatever sits at its path, creating intermediate maps as needed, so
// later pairs win. Malformed pairs are skipped; an empty path segment is an error.
func coerceMapDotted(f reflect.Value, raw []string) error {
	m := map[string]any{}
	if !reflect.TypeFor[map[string]any]().AssignableTo(f.Type()) {
		return fmt.Errorf("dotted keys need a map[string]any flag, not %s", f.Type())
	}
	for _, pair := range raw {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		segs := strings.Split(k, ".")
		if slices.Contains(segs, "") {
			return fmt.Errorf("invalid key path %q — empty segment", k)
		}
		cur := m
		for _, seg := range segs[:len(segs)-1] {
			next, ok := cur[seg].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[seg] = next
			}
			cur = next
		}
		cur[segs[len(segs)-1]] = inferScalar(v)
	}
	f.Set(reflect.ValueOf(m))
	return nil
}

func coerceMap(f reflect.Value, raw []string) error {
	kt := f.Type().Key()
	if kt.Kind() != reflect.String {
		return nil // only string-keyed maps are supported
	}
	et := f.Type().Elem()
	m := reflect.MakeMapWithSize(f.Type(), len(raw))
	for _, pair := range raw {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		ev := reflect.New(et).Elem()
		if ev.Kind() == reflect.Interface {
			if inferred := inferScalar(v); inferred != nil {
				ev.Set(reflect.ValueOf(inferred))
			}
		} else if err := coerce(ev, []string{v}); err != nil {
			return fmt.Errorf("value for key %q: %w", k, err)
		}
		m.SetMapIndex(reflect.ValueOf(k).Convert(kt), ev)
	}
	f.Set(m)
	return nil
}
