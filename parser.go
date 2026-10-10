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
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// parsedInputs is one invocation's parsed argv, indexed by position in the resolved chain
// (root = 0 … leaf). Keying by position rather than command name means two commands on one
// path never collide, and a statically composed child still reads its own generated types.
type parsedInputs struct {
	scopes  []scopeInputs     // one entry per resolved chain frame, root → leaf
	argvSet []map[string]bool // per frame: logical flag names explicitly set on argv (not defaults, not env/config fallback)

	// span limits validation to the frames an inputs type describes, [lo, hi). A parent
	// reading its own inputs in a cascading hook must not be failed by a descendant's required
	// inputs (which would break `app sub --help`). nil means no limit.
	span *[2]int

	// detached records a word that directly followed an optional-value flag and would have
	// been a valid value for it (`--dry-run server` for `--dry-run=server`), the likely cause
	// of an unexpected positional. [0] is the flag as typed, [1] the word; nil when none.
	detached *[2]string

	// dashedFirst records that a "--" came before the leaf's first positional, which ended
	// command lookup there, so a sub-command's name after it is a positional.
	dashedFirst bool

	// dashSeen records that argv ended flags with a "--" the parser consumed, and dashAt how many
	// positional words the leaf had been given before it. A "--" taken as an argument (after
	// flags stopped, or as a raw word) is not recorded. See [Context.DashIndex].
	dashSeen bool
	dashAt   int

	// lateFlag records the first word after an options_first command's first argument that
	// would have been a flag of the chain. A resulting too-many-arguments error says flags go
	// first. nil when none.
	lateFlag *string

	// dir is the run's injected working directory, which relative existingfile and existingdir
	// values are checked against; "" is the process's.
	dir string

	// clock is what relative time values are measured from; nil reads time.Now.
	clock *runClock
}

// argvAcq is what reading argv values may consult: stdin for a `from: [stdin]` flag's "-",
// and the run's injected directory for a relative `@file` ("" is the process working
// directory).
type argvAcq struct {
	stdin io.Reader
	dir   string
	clock *runClock // what relative time values are measured from; nil reads time.Now
	// goos is the operating system whose rules glob arguments follow; "" is the running one.
	goos string
}

// detachedHint is the hint an unexpected-positional error carries when one of the extra
// words is the value of an optional-value flag written detached, else "".
func (p *parsedInputs) detachedHint(extra []string) string {
	if p == nil || p.detached == nil || !slices.Contains(extra, p.detached[1]) {
		return ""
	}
	return fmt.Sprintf("; %s takes its value attached: %s=%s", p.detached[0], p.detached[0], p.detached[1])
}

// lateFlagHint is the hint a too-many-arguments error carries when one of the extra words is
// a flag typed after an options_first command's first argument, else "".
func (p *parsedInputs) lateFlagHint(extra []string) string {
	if p == nil || p.lateFlag == nil || !slices.Contains(extra, *p.lateFlag) {
		return ""
	}
	return "; flags go before its first argument (it takes options first): " + *p.lateFlag
}

// covers reports whether chain frame i is one this validation pass judges.
func (p *parsedInputs) covers(i int) bool {
	return p == nil || p.span == nil || (i >= p.span[0] && i < p.span[1])
}

// setOnArgv reports whether the flag was explicitly provided on argv in chain frame idx, the
// notion of presence flag groups and dependencies enforce. A default or fallback is not
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
	// typed is the identifier typed for each flag set on argv ("--due", "-d", "--no-color",
	// "--db.host"), so an error about its value names what the user wrote.
	typed map[string]string
	// used is every identifier each flag was set through, in order; [Deprecations] reports
	// from it.
	used map[string][]string
	// origin names where a flag's value came from when its env or config fallback supplied it
	// ("environment variable APP_PORT", `configuration file "app"`), so an error about the
	// value says where to look. A value typed on the command line has none.
	origin map[string]string
	// presenceOnly marks flags present only to satisfy presence rules: a hand-built layer
	// supplied them (see [InputReport.Validate]), and their values are checked elsewhere.
	presenceOnly map[string]bool
	// placeholderArgs counts trailing args that stand in for hand-built positionals the same
	// way: they satisfy the required check, and their values are not read here.
	placeholderArgs int
	// handBuiltArgs marks argument indexes the command line supplied but a hand-built layer
	// overrode: the value that won is checked with the typed rules, not the string read here.
	handBuiltArgs map[int]bool
	// argvArgs counts the positional values the command line gave, before defaults or fallbacks
	// were added after them.
	argvArgs int
	// argOrigin names where an argument's value came from when its env or config fallback
	// supplied it, by argument index; see origin.
	argOrigin map[int]string
	// written maps an argv value or default that declared expansion changed to the value as it
	// was written, so a path error can name both; see [withWritten].
	written map[string]string
	// depValues holds the values flag dependencies compare (Equals) when the store was built from
	// typed inputs, whose flags carry no values here; see [scopeInputs.dependencyValue].
	depValues map[string]string
}

// fromSource is the suffix an error about a fallback value carries, naming its origin; "" for
// a value typed on the command line.
func fromSource(origin string) string {
	if origin == "" {
		return ""
	}
	return " (from " + origin + ")"
}

// withSource appends a fallback value's origin to a value error, so the user can find a value
// they never typed. A non-*ParseError, or an empty origin, is returned unchanged.
func withSource(err error, origin string) error {
	pe, ok := err.(*ParseError) //nolint:errorlint // the value checks return a bare *ParseError
	if !ok || origin == "" {
		return err
	}
	cp := *pe
	cp.Msg += fromSource(origin)
	return &cp
}

// label is how an error names flag fd: as the user typed it, else by its preferred identifier.
func (si scopeInputs) label(fd FlagDef) string {
	if t := si.typed[fd.Name]; t != "" {
		return t
	}
	return flagLabel(fd)
}

// ParseKind classifies a [*ParseError] so a reporter can branch on the failure without matching
// the message. Every kind is the end-user's to fix except [ParseKindInternal], a misuse of the
// parser API by the author.
type ParseKind int

// The parse failure kinds.
const (
	// ParseKindUnspecified is the zero value: a [*ParseError] whose construction
	// site did not classify it (a hand-built error that sets no Kind).
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
	ParseKindInternal                      // a parser API misuse: nil parser/context, a bad out argument, or an inputs type that does not describe the running command
	ParseKindMisplacedFlag                 // a flag was typed where it can't apply, such as before a plugin's name
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
	case ParseKindMisplacedFlag:
		return "misplaced-flag"
	default:
		return "unspecified"
	}
}

// ParseError is a parse-time failure caused by bad input. Its message carries no suggestions
// or usage text; the structured fields let a handler compose its own response: switch on Kind,
// pass Token and Candidates to a [Suggestor], or render help for Command. It unwraps to
// [ErrUsage] ([ErrInternal] for [ParseKindInternal]), so [CategoryOf] can classify it. The
// default reporter exits 1; a program wanting exit code 2 for usage errors maps it in its own
// reporter (see [Category]).
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
	Msg        string    // the human-readable failure
	Command    string    // the command in whose scope parsing failed ("" when not command-scoped)
	Flag       string    // the flag involved, by the identifier or label used ("" when not flag-related)
	Token      string    // the offending argv token or value ("" when none)
	Candidates []string  // the vocabulary Token failed against — sibling commands, declared flags, enum members (nil when none applies)
}

// Error renders the parse failure as a single, user-facing line.
func (e *ParseError) Error() string { return e.Msg }

// Unwrap returns the category sentinel: [ErrInternal] for [ParseKindInternal] and [ErrUsage]
// for every other kind.
func (e *ParseError) Unwrap() error {
	if e.Kind == ParseKindInternal {
		return ErrInternal
	}
	return ErrUsage
}

// Parser is rotini's argv parser: it parses and validates the command line against what the
// resolved chain declares (GNU/POSIX grammar, typed coercion, enum and constraint checks),
// failing with a [*ParseError].
//
// Parsing is opt-in: a CLI that wants raw argv reads [Context.Argv] instead. A handler reads
// the parser with [Context.Parser]:
//
//	parser := rtx.Parser()
//	var in MycliInputs
//	err := parser.Parse(rtx, &in)
type Parser struct{}

// NewParser returns rotini's default [Parser].
func NewParser() *Parser {
	return &Parser{}
}

// Parse binds the running command's arguments into out — a non-nil pointer to the generated
// inputs struct — from the resolved chain and raw argv on rtx, in the json.Unmarshal style:
//
//	var in MycliInputs
//	if err := parser.Parse(rtx, &in); err != nil { /* handler owns it */ }
//
// It applies declared defaults, fills out by reflection from the `rotini:"…"` struct tags, then
// validates. Built-ins and any [encoding.TextUnmarshaler] are coerced, and a trailing []string
// absorbs the remaining positionals. Parse does not consult env or config fallbacks;
// [Context.Inputs] does.
//
// It returns a [*ParseError] when out is not a non-nil pointer, a flag is unknown or missing
// its value, a value cannot be coerced, a required input is absent, a value falls outside a
// declared enum, or any declared constraint, flag group or flag dependency is violated.
//
// Unlike [Context.Inputs] and the per-channel layer methods, Parse does not check that out
// describes the running command: it binds what fits and leaves the rest zeroed, so one struct
// can span a whole tree. A handler reading its own inputs should use [Context.Inputs].
func (p *Parser) Parse(rtx *Context, out any) error {
	store, chain, err := p.parseBind(rtx, out)
	if err != nil {
		return err
	}
	return validateStore(chain, store)
}

// parseBind parses argv into a store, expands the values of inputs that declare `expand`, and
// binds the store into out without validating — that is [validate]'s job. It returns the store
// and chain for that deferred pass. The [InputReader] runs the same steps with its own view of
// the environment in between (see [parseArgvStore]).
func (p *Parser) parseBind(rtx *Context, out any) (*parsedInputs, []Command, error) {
	store, chain, err := parseArgvStore(p, rtx, out)
	if err != nil {
		return nil, nil, err
	}
	v := reflect.ValueOf(out).Elem()
	anchor := frameAnchor(v, chain, rtx.frameIndex())
	if err := expandStore(v, chain, store, anchor, layerView(rtx, chain, v, store)); err != nil {
		return nil, nil, err
	}
	if err := bindInputs(v, store, chain, anchor); err != nil {
		return nil, nil, err
	}
	return store, chain, nil
}

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized flag, or one
// missing its value, is a [*ParseError]. Chain command tokens are consumed, and everything
// after the leaf command and after "--" is a positional of the leaf. Declared defaults are
// applied; required and enum checks are [validate]'s job.
func parseInto(chain []Command, argv []string, acq argvAcq) (*parsedInputs, error) {
	store, err := parseArgvTokens(chain, argv, acq)
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
// A count flag takes no value (an inline one is an error); a bool defaults to "true" but honors
// an inline value; any other flag uses an inline value if present, else its implicit value
// without consuming the next token, else the next token (running out is an error).
func flagTokenValue(fdef FlagDef, name, inline string, hasInline bool, argv []string, i int) (value string, next int, err error) {
	switch {
	case fdef.Type == "count":
		if hasInline {
			return "", i, &ParseError{Kind: ParseKindInvalidValue, Msg: name + " counts occurrences and takes no value", Flag: name}
		}
		return "1", i, nil // each occurrence appends one marker; the input reader tallies them
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
			return "", i, &ParseError{Kind: ParseKindNeedsValue, Msg: name + " needs a value", Flag: name}
		}
		return argv[i], i, nil
	}
}

// flagTokenWidth reports how many words after argv[i] the flag token argv[i] consumes, given
// the commands reached so far. It runs consumeFlagToken with nothing recorded, so the command
// resolver and completion always agree with the parser about which word is a flag's value. An
// unknown or malformed flag consumes none; the parse reports it.
func flagTokenWidth(chain []Command, argv []string, i int) int {
	extra, err := consumeFlagToken(chain, argv[i], argv, i, func(int, FlagDef, string, string) error { return nil })
	if err != nil {
		return 0
	}
	return extra
}

// consumeFlagToken parses one flag token from argv[i], records it, and reports how many extra
// argv entries it consumed — a separate value word, or the tail of a short cluster.
//
// A token matching no declared identifier is retried as a POSIX short cluster (-vh → -v -h,
// -n5 → -n 5). Long flags never cluster, so an unmatched one is an unknown-flag error carrying
// the chain's vocabulary for a [Suggestor].
func consumeFlagToken(chain []Command, tok string, argv []string, i int, addFlag func(int, FlagDef, string, string) error) (int, error) {
	name, inline, hasInline := splitFlag(tok)

	fdef, idx, negated, ok := findFlagMatch(chain, name)
	if !ok {
		// --db.host=h sets one field of the object flag --db: it is the occurrence host=h.
		if ofd, oidx, field, isField := objectFieldFlag(chain, name); isField {
			value, next, err := flagTokenValue(FlagDef{Type: "string"}, name, inline, hasInline, argv, i)
			if err != nil {
				return 0, err
			}
			// Errors label the object flag (--db); the message names the field.
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

	// A negated form is itself the value (false), so an inline value on it is rejected.
	if negated {
		if hasInline {
			return 0, &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s is the negated form and takes no value; use --%s to set one", name, fdef.Name),
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
func (p *parsedInputs) recordArgvFlag(idx int, fd FlagDef, value, typed string, acq argvAcq) error {
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
	if err := refuseSecretLiteral(fd.Secret, fd.From, typed, typed, value); err != nil {
		return err
	}
	acquired := acquiresValue(fd, value)
	value, err := resolveFlagValue(fd, typed, value, acq)
	if err != nil {
		return err
	}
	split := splitValue
	if acquired && splitsAcquiredLines(fd) {
		split = splitAcquired // a file's (or stdin's) lines are separate values
	}
	values, err := split(value, fd.Separator)
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
// argument it lands in, if that declares one. A variadic followed by fixed arguments splits
// nothing, since where it ends is known only once every word is in.
func (p *parsedInputs) recordArg(leaf Command, idx int, value string, acq argvAcq) error {
	if ad, ok := acquiringArg(leaf.Arguments, len(p.scopes[idx].args)); ok {
		if err := refuseSecretLiteral(ad.Secret, ad.From, "<"+ad.Name+">", "", value); err != nil {
			return err
		}
		resolved, err := resolveAcquired(ad.From, "<"+ad.Name+">", "", value, acq)
		if err != nil {
			return err
		}
		value = resolved
	}
	values := []string{value}
	if def, ok := variadicAt(leaf.Arguments, len(p.scopes[idx].args)); ok && def.Separator != "" && !hasArgTail(leaf.Arguments) {
		var err error
		if values, err = splitValue(value, def.Separator); err != nil {
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("<%s>: %v", def.Name, err)}
		}
	}
	p.scopes[idx].args = append(p.scopes[idx].args, values...)
	return nil
}

// acquiringArg returns the argument positional number pos fills when it declares acquisition
// modes ([ArgDef.From]): only an argument before any variadic one, whose position is known as
// the words arrive.
func acquiringArg(args []ArgDef, pos int) (ArgDef, bool) {
	for i, a := range args {
		if a.Variadic {
			return ArgDef{}, false
		}
		if i == pos {
			return a, len(a.From) > 0
		}
	}
	return ArgDef{}, false
}

// parseArgvTokens is parseInto without defaults: exactly what argv supplied. It records each
// explicitly set flag in the store's argvSet, the single source of truth for "set on the
// command line". acq.stdin backs the from:stdin sentinel and is read only when a "-" value
// appears on an opted-in flag; acq.dir is where relative `@file` and existingfile paths resolve.
func parseArgvTokens(chain []Command, argv []string, acq argvAcq) (*parsedInputs, error) {
	store := &parsedInputs{
		scopes:  make([]scopeInputs, len(chain)),
		argvSet: make([]map[string]bool, len(chain)),
		dir:     acq.dir,
		clock:   acq.clock,
	}
	leaf := len(chain) - 1 // chain index of the leaf command
	depth := 1             // index of the next chain frame we might descend into
	startedArgs := false   // a positional has been seen: command descent is over
	leafWords := 0         // positional words given to the leaf so far (words, not split values)
	pt := passthroughArg(chain[leaf].Arguments)

	// Where flags stop. Three states, and every argv walker reads them the same way:
	//   - terminated: no more flags, and a later "--" is an argument. A consumed "--" sets it,
	//     and so does the first argument of an options_first leaf.
	//   - raw: the leaf's passthrough argument has started; every word is kept as typed, not
	//     split on a separator.
	//   - passthrough(): a passthrough command was entered; every word after its name is raw.
	terminated := false
	optionsDone := false // terminated by an options_first leaf's first argument, not by "--"
	raw := false

	addFlag := func(idx int, fd FlagDef, value, typed string) error {
		return store.recordArgvFlag(idx, fd, value, typed, acq)
	}
	addArg := func(value string) error {
		return store.recordArg(chain[leaf], leaf, value, acq)
	}
	passthrough := func() bool {
		return chain[leaf].Passthrough && depth == len(chain)
	}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		if raw || passthrough() {
			store.scopes[leaf].args = append(store.scopes[leaf].args, tok)
			continue
		}

		if !terminated && tok == "--" {
			store.dashedFirst = !startedArgs
			store.dashSeen, store.dashAt = true, leafWords
			terminated = true
			startedArgs = true
			continue
		}

		if !terminated && isFlag(chain[:depth], tok) {
			// Only the commands reached so far are eligible: a flag typed before a sub-command's
			// name belongs to one of its ancestors, never to it.
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
		if depth == len(chain) && leafWords == pt {
			raw = true // the passthrough argument's first word
			store.scopes[leaf].args = append(store.scopes[leaf].args, tok)
			continue
		}
		if optionsDone && store.lateFlag == nil && isFlag(chain[:depth], tok) {
			if name, _, _ := splitFlag(tok); flagNamed(chain[:depth], name) {
				store.lateFlag = &tok
			}
		}
		if err := addArg(tok); err != nil {
			return nil, err
		}
		leafWords++
		if chain[leaf].OptionsFirst && depth == len(chain) && !terminated {
			terminated, optionsDone = true, true
		}
	}
	store.scopes[leaf].argvArgs = len(store.scopes[leaf].args)

	store.expandGlobs(chain[leaf].Arguments, leaf, acq.goos)
	return store, nil
}

// passthroughArg returns the index of args' passthrough argument, which lint keeps last, or
// -1 when there is none.
func passthroughArg(args []ArgDef) int {
	if n := len(args); n > 0 && args[n-1].Passthrough {
		return n - 1
	}
	return -1
}

// flagNamed reports whether name is an identifier (or negated form) of a flag on chain, or a
// short cluster whose first letter is one.
func flagNamed(chain []Command, name string) bool {
	if _, _, _, ok := findFlagMatch(chain, name); ok {
		return true
	}
	if isShortCluster(name) {
		_, _, ok := findFlagIndex(chain, name[:2])
		return ok
	}
	return false
}

// noteDetached records the first bare optional-value flag followed by a word that would have
// been a valid value for it, so a resulting extra-positional error can say what the user
// probably meant. See parsedInputs.detached.
func noteDetached(store *parsedInputs, chain []Command, tok string, argv []string, i int) {
	if store.detached != nil || strings.Contains(tok, "=") || i+1 >= len(argv) {
		return
	}
	fd, _, ok := findFlagIndex(chain, tok)
	next := argv[i+1]
	if !ok || fd.ImplicitValue == "" || isFlag(chain, next) || next == "--" {
		return
	}
	if e := flagEnum(fd); e.declared() && !e.has(next) {
		return
	}
	store.detached = &[2]string{tok, next}
}

// shortCircuited reports whether a short-circuit flag ([FlagDef.ShortCircuit]) is set to true
// on the command line anywhere on the resolved chain. Such a flag replaces the command's normal
// run, so every declared requirement of the chain is waived: required inputs, enums, bounds,
// patterns, path checks, flag groups and dependencies. What cannot be read at all (an unknown
// flag or command, an uncoercible value, extra positionals, a malformed map) is still an
// error, except in a configuration file, which the input reader skips. A default or fallback
// never sets a short-circuit flag.
func shortCircuited(chain []Command, store *parsedInputs) bool {
	if store == nil {
		return false
	}
	for i, f := range chain {
		if i >= len(store.scopes) {
			break
		}
		for _, fd := range f.Flags {
			if !fd.ShortCircuit || !store.setOnArgv(i, fd.Name) {
				continue
			}
			vals := store.scopes[i].flags[fd.Name]
			if len(vals) == 0 {
				continue
			}
			if on, err := strconv.ParseBool(vals[len(vals)-1]); err == nil && on {
				return true
			}
		}
	}
	return false
}

// checkFlagShape is the part of [checkFlagValues] a short-circuited run keeps: a map value
// must be key=value pairs, which is a matter of reading the value, not of meeting a
// requirement.
func checkFlagShape(fd FlagDef, label string, vals []string) error {
	if !isMapType(fd.Type) {
		return checkValueShape(fd.Type, label, label, vals, fd.Secret)
	}
	for _, v := range vals {
		if err := checkMapPair(label, v, fd.Secret); err != nil {
			return err
		}
	}
	return nil
}

// checkMapPair reports a map input's value that is not a key=value pair with a non-empty key.
// label names the input as the user supplied it.
func checkMapPair(label, v string, secret bool) error {
	if msg := mapPairProblem(label, v, secret); msg != "" {
		return &ParseError{Kind: ParseKindInvalidValue, Msg: msg, Flag: label}
	}
	return nil
}

// mapPairProblem is the message for a map entry that is not key=value or whose key is empty or
// only whitespace, else "". Flags and environment variables share it.
func mapPairProblem(label, v string, secret bool) string {
	key, _, ok := strings.Cut(v, "=")
	switch {
	case !ok:
		return fmt.Sprintf("%s expects key=value pairs (got %q)", label, redactValue(v, secret))
	case strings.TrimSpace(key) == "":
		return fmt.Sprintf("%s needs a key before \"=\" (got %q)", label, redactValue(v, secret))
	}
	return ""
}

// shapeTypes are the declared types whose values a short-circuited run still converts. Such a
// run may never bind the command that declares an input, so this is where a value that is not
// a number, a duration or a bool is still reported, as the command line always reports one.
var shapeTypes = map[string]reflect.Type{
	"int": reflect.TypeFor[int](), "int8": reflect.TypeFor[int8](), "int16": reflect.TypeFor[int16](),
	"int32": reflect.TypeFor[int32](), "int64": reflect.TypeFor[int64](),
	"uint": reflect.TypeFor[uint](), "uint8": reflect.TypeFor[uint8](), "uint16": reflect.TypeFor[uint16](),
	"uint32": reflect.TypeFor[uint32](), "uint64": reflect.TypeFor[uint64](),
	"float32": reflect.TypeFor[float32](), "float64": reflect.TypeFor[float64](),
	"bool": reflect.TypeFor[bool](), "time.Duration": durationType,
}

// checkValueShape reports the first of vals that does not convert to typ (or, for a list, its
// element type), as binding would. Types outside shapeTypes are left to binding.
func checkValueShape(typ, label, flag string, vals []string, secret bool) error {
	t, ok := shapeTypes[strings.TrimPrefix(typ, "[]")]
	if !ok {
		return nil
	}
	for _, v := range vals {
		if err := coerce(reflect.New(t).Elem(), []string{v}); err != nil {
			return coerceFailure(label, flag, err, secret)
		}
	}
	return nil
}

// checkFlagValues checks one flag's argv values against its enum, its key=value shape when it
// is a map, and its constraints. label is the flag as the user typed it.
func checkFlagValues(fd FlagDef, label string, vals []string, dir string, clock *runClock) error {
	enum := flagEnum(fd)
	for _, v := range vals {
		if enum.declared() && !enum.has(v) {
			return enum.violation(label, label, v, fd.Secret)
		}
		if isMapType(fd.Type) {
			if err := checkMapPair(label, v, fd.Secret); err != nil {
				return err
			}
		}
	}
	if err := checkConstraints(label, fd.Type, fd.Constraints, vals, fd.Secret, dir); err != nil {
		return err
	}
	if fd.UniqueItems && !isObjectFlag(fd) {
		return checkUniqueItems(label, vals, uniqueKeyFor(fd.Type, flagTimeSpec(fd, clock), flagEnum(fd)), fd.Secret)
	}
	return nil
}

// strayCommand reports a positional on a command that branches but takes no arguments: a
// mistyped sub-command. The error carries the sibling vocabulary for a [Suggestor]. A command's
// own name typed after "--" is not mistyped: "--" ended command lookup, which the message says,
// and the name is left out of the vocabulary.
func strayCommand(leaf Command, si scopeInputs, store *parsedInputs) error {
	if len(leaf.Commands) == 0 || len(leaf.Arguments) > 0 || len(si.args) == 0 {
		return nil
	}
	tok := si.args[0]
	if store != nil && store.dashedFirst && namesChild(leaf, tok) {
		return &ParseError{
			Kind:       ParseKindUnknownCommand,
			Msg:        fmt.Sprintf("%q after \"--\" is not read as a command, and %q takes no arguments", tok, leaf.Name),
			Command:    leaf.Name,
			Token:      tok,
			Candidates: slices.DeleteFunc(childCommandNames(leaf), func(n string) bool { return n == tok }),
		}
	}
	return &ParseError{
		Kind:       ParseKindUnknownCommand,
		Msg:        fmt.Sprintf("unknown command %q for %q", tok, leaf.Name) + store.detachedHint(si.args[:1]),
		Command:    leaf.Name,
		Token:      tok,
		Candidates: childCommandNames(leaf),
	}
}

// extraPositionals reports positionals the leaf has no argument for, unless a variadic argument
// absorbs them.
func extraPositionals(chain []Command, si scopeInputs, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	n := len(leaf.Arguments)
	if hasVariadicArg(leaf.Arguments) || len(si.args) <= n {
		return nil
	}
	hint := store.detachedHint(si.args[n:]) + store.lateFlagHint(si.args[n:]) // only a word that is one too many
	if n == 0 {
		return &ParseError{Kind: ParseKindNoArguments, Msg: fmt.Sprintf("%s takes no arguments (got %d)", pathOf(chain), len(si.args)) + hint, Command: leaf.Name}
	}
	return &ParseError{Kind: ParseKindTooManyArguments, Msg: fmt.Sprintf("%s accepts at most %d %s (got %d)", pathOf(chain), n, plural("argument", n), len(si.args)) + hint, Command: leaf.Name}
}

// validate enforces the chain's declarations against a parsed store, limited to the frames the
// store's span covers: a stray positional on a branch-only command is a mistyped sub-command,
// required inputs must be present or defaulted, values must satisfy enums and constraints, and
// extra positionals are rejected.
func validate(chain []Command, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]
	waived := shortCircuited(chain, store)
	// A short circuit usually stops the run before the commands below the one reading its
	// inputs check their own, so the input that can't be read is checked for the whole chain.
	covered := func(i int) bool { return waived || store.covers(i) }
	leafCovered := covered(len(chain) - 1)

	if leafCovered {
		if err := strayCommand(leaf, si, store); err != nil {
			return err
		}
	}

	if !waived {
		if err := requiredErrors(chain, store); err != nil {
			return err
		}
		if err := stdinDashOnce(chain, store); err != nil {
			return err
		}
	}

	for i, f := range chain {
		if !covered(i) {
			continue
		}
		fsi := store.scopes[i]
		for _, fd := range f.Flags {
			if fsi.presenceOnly[fd.Name] {
				continue
			}
			var err error
			if waived {
				err = checkFlagShape(fd, fsi.label(fd), fsi.flags[fd.Name])
			} else if err = checkRepeat(fd, fsi.label(fd), fsi.flags[fd.Name], store.setOnArgv(i, fd.Name)); err == nil {
				err = checkFlagValues(fd, fsi.label(fd), fsi.flags[fd.Name], store.dir, store.clock)
			}
			if err != nil {
				return withSource(withWritten(err, fsi.flags[fd.Name], fsi.written), fsi.origin[fd.Name])
			}
		}
	}

	if !leafCovered {
		return nil
	}
	if err := extraPositionals(chain, si, store); err != nil {
		return err
	}
	return validateArgs(leaf, si, waived, store.dir, store.clock)
}

// validateArgs checks the leaf's positional values: every rule normally, and only whether each
// value can be read at all under a short circuit.
func validateArgs(leaf Command, si scopeInputs, waived bool, dir string, clock *runClock) error {
	args := si.args[:len(si.args)-si.placeholderArgs] // only values this store actually read
	spans := argSpans(leaf.Arguments, len(si.args))
	for i, ad := range leaf.Arguments {
		s := spans[i]
		if (s[0] < s[1] && s[1] > len(args)) || si.handBuiltArgs[i] {
			continue // a hand-built positional: its value is checked with the typed rules
		}
		var vals []string
		if s[0] < s[1] {
			vals = args[s[0]:s[1]]
		}
		if waived {
			if len(vals) == 0 {
				continue
			}
			if err := checkValueShape(ad.Type, "<"+ad.Name+">", "", vals, ad.Secret); err != nil {
				return err
			}
			continue
		}
		if len(vals) == 0 && !ad.Variadic {
			continue // a non-variadic argument that was not provided — requiredErrors covers absence
		} // an absent variadic still gets a MinItems check
		if err := checkArgValues(ad, vals, dir, clock); err != nil {
			return withSource(withWritten(err, vals, si.written), si.argOrigin[i])
		}
	}
	return nil
}

// checkArgValues checks one argument's values against its enum and its constraints; the
// positional counterpart of [checkFlagValues].
func checkArgValues(ad ArgDef, vals []string, dir string, clock *runClock) error {
	enum := argEnum(ad)
	for _, v := range vals {
		if enum.declared() && !enum.has(v) {
			return enum.violation("<"+ad.Name+">", "", v, ad.Secret)
		}
	}
	label := "<" + ad.Name + ">"
	if err := checkConstraints(label, ad.Type, ad.Constraints, vals, ad.Secret, dir); err != nil {
		return err
	}
	if ad.UniqueItems {
		return checkUniqueItems(label, vals, uniqueKeyFor(ad.Type, argTimeSpec(ad, clock), argEnum(ad)), ad.Secret)
	}
	return nil
}

// hasVariadicArg reports whether any of a command's arguments is variadic.
func hasVariadicArg(args []ArgDef) bool {
	for _, a := range args {
		if a.Variadic {
			return true
		}
	}
	return false
}

// checkConstraints enforces an input's declared bounds against its supplied values (one for a
// scalar, possibly many for a repeatable flag or variadic argument). Numeric bounds apply to
// numeric and measured types, length and pattern to strings and paths, item counts to arrays
// and maps; per-value checks apply to each element. A constraint on any other type, which a
// valid spec cannot produce, is skipped. A secret input's value and length are redacted.
func checkConstraints(label, typ string, c Constraints, values []string, secret bool, dir string) error {
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
			// A path is a string, so its length and pattern bounds apply too.
			if err = checkStringBounds(label, c, v, secret); err == nil {
				err = checkPathExists(label, elem, v, dir)
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

// isPathType reports whether typ is a path kind: existingfile, existingdir, inputfile or
// outputfile. The generated field is a plain string; only the type name tells the parser to
// check the path.
func isPathType(typ string) bool {
	return typ == "existingfile" || typ == "existingdir" || isStreamPathType(typ)
}

// checkPathExists enforces an existingfile/existingdir type at parse time, so the error names
// the flag the user typed. It checks only existence and kind; cleaning, resolving symlinks and
// creating missing files are left to the handler, and "~" and "$VAR" are expanded beforehand
// only for inputs that declare `expand`. A relative value is checked against dir, the run's
// injected directory ("" is the process working directory).
func checkPathExists(label, typ, value, dir string) error {
	if isStreamPathType(typ) {
		return checkStreamPath(label, typ, value, dir)
	}
	info, err := os.Stat(joinDir(dir, value))
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
		// A permission or I/O failure is reported in rotini's words, never the raw OS error.
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

// checkItemCount enforces how many values an array or map input may carry.
func checkItemCount(label string, c Constraints, n int) error {
	switch {
	case c.MinItems > 0 && n < c.MinItems:
		return constraintViolation("%s needs at least %d %s (got %d)", label, c.MinItems, plural("value", c.MinItems), n)
	case c.MaxItems != nil && n > *c.MaxItems:
		return constraintViolation("%s accepts at most %d %s (got %d)", label, *c.MaxItems, plural("value", *c.MaxItems), n)
	}
	return nil
}

// checkNumericBounds enforces the numeric bounds on one value. An unparseable value is
// skipped; coerce reports it.
func checkNumericBounds(label string, c Constraints, v string, secret bool, parse func(string) (float64, bool), format func(float64) string) error {
	n, numeric := parse(v)
	if !numeric {
		return nil
	}
	return checkNumberBounds(label, c, n, redactValue(v, secret), format)
}

// checkNumberBounds is the numeric rule core shared by the command-line path and
// [Context.CheckInputs]: n is the value, got the text an error shows for it (already redacted
// for a secret input), and format renders a bound. Declaring any bound also requires a finite
// number, so NaN and ±Inf are rejected; an unbounded float accepts them.
func checkNumberBounds(label string, c Constraints, n float64, got string, format func(float64) string) error {
	switch {
	case hasNumericBounds(c) && (math.IsNaN(n) || math.IsInf(n, 0)):
		return constraintViolation("%s must be a finite number (got %s)", label, got)
	case c.Minimum != nil && n < *c.Minimum:
		return constraintViolation("%s must be >= %s (got %s)", label, format(*c.Minimum), got)
	case c.Maximum != nil && n > *c.Maximum:
		return constraintViolation("%s must be <= %s (got %s)", label, format(*c.Maximum), got)
	case c.ExclusiveMinimum != nil && n <= *c.ExclusiveMinimum:
		return constraintViolation("%s must be > %s (got %s)", label, format(*c.ExclusiveMinimum), got)
	case c.ExclusiveMaximum != nil && n >= *c.ExclusiveMaximum:
		return constraintViolation("%s must be < %s (got %s)", label, format(*c.ExclusiveMaximum), got)
	case c.MultipleOf != nil && !isMultipleOf(n, *c.MultipleOf):
		return constraintViolation("%s must be a multiple of %s (got %s)", label, format(*c.MultipleOf), got)
	}
	return nil
}

// hasNumericBounds reports whether c declares any numeric bound.
func hasNumericBounds(c Constraints) bool {
	return c.Minimum != nil || c.Maximum != nil || c.ExclusiveMinimum != nil || c.ExclusiveMaximum != nil || c.MultipleOf != nil
}

// parseNumber reports whether v is a number, and its value.
func parseNumber(v string) (float64, bool) {
	n, err := strconv.ParseFloat(v, 64)
	return n, err == nil
}

// checkStringBounds enforces the length and pattern bounds on one value. A secret's length is
// redacted, since the length alone leaks information about a credential.
func checkStringBounds(label string, c Constraints, v string, secret bool) error {
	ln := utf8.RuneCountInString(v)
	gotLen := strconv.Itoa(ln)
	if secret {
		gotLen = "[redacted]"
	}
	switch {
	case c.MinLength > 0 && ln < c.MinLength:
		return constraintViolation("%s must be at least %d %s long (got %s)", label, c.MinLength, plural("character", c.MinLength), gotLen)
	case c.MaxLength != nil && ln > *c.MaxLength:
		return constraintViolation("%s must be at most %d %s long (got %s)", label, *c.MaxLength, plural("character", *c.MaxLength), gotLen)
	}
	if c.Pattern != "" {
		if re, err := compiledPattern(c.Pattern); err == nil && !re.MatchString(v) {
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

// redactValue returns "[redacted]" for a secret input, else the value unchanged, so a secret
// value never appears in an error.
func redactValue(v string, secret bool) string {
	if secret {
		return "[redacted]"
	}
	return v
}

// validateFlagGroups enforces each command's cross-flag presence rules. Set means explicitly
// provided on argv (a default or fallback does not count), read from the store's argvSet so
// flags inside short clusters count.
func validateFlagGroups(chain []Command, store *parsedInputs) error {
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

// checkFlagGroup applies one group's rule given the labels set and the full membership.
func checkFlagGroup(kind FlagGroupKind, set, all []string) error {
	switch kind {
	case FlagGroupMutuallyExclusive:
		if len(set) > 1 {
			return constraintViolation("flags %s are mutually exclusive", joinAnd(set))
		}
	case FlagGroupRequiredTogether:
		if n := len(set); n > 0 && n < len(all) {
			return constraintViolation("flags %s must be used together", strings.Join(all, ", "))
		}
	case FlagGroupOneOf:
		switch {
		case len(set) == 0:
			return constraintViolation("exactly one of %s is required", strings.Join(all, ", "))
		case len(set) > 1:
			return constraintViolation("flags %s are mutually exclusive", joinAnd(set))
		}
	case FlagGroupAtLeastOne:
		if len(set) == 0 {
			return constraintViolation("at least one of %s is required", strings.Join(all, ", "))
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

// numericFamily is every type string whose values are range-checkable numbers: the Go numeric
// types plus the JSON Schema aliases, which a hand-built Definition may use.
var numericFamily = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"integer": true, "number": true,
}

func isNumericType(typ string) bool { return numericFamily[typ] }

// measured is a type whose values are quantities in their own unit (a duration in
// nanoseconds, a size in bytes): numeric bounds apply once a value is read in that unit, and
// print back in the type's spelling ("<= 30d", "<= 1Gi").
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

// flagDefaults returns the occurrences a flag's declared default seeds when the user supplied
// none: the single Default, else every element of Defaults. Both empty means no default, so
// the flag stays unset ("absent" remains distinct from "explicitly empty"). Default wins if a
// hand-built Definition sets both.
func flagDefaults(fd FlagDef) []string {
	if fd.Default != "" {
		return []string{fd.Default}
	}
	if len(fd.Defaults) == 0 {
		return nil
	}
	return slices.Clone(fd.Defaults)
}

// applyDefaults fills in declared flag and trailing-argument defaults for inputs
// the user did not provide, so handlers and required-checks see them.
func applyDefaults(chain []Command, store *parsedInputs) {
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
	if hasArgTail(args) {
		return // arguments after a variadic take no default, so there is no trailing gap to fill
	}
	for i := len(store.scopes[leaf].args); i < len(args); i++ {
		if args[i].Default == "" {
			break // can't fill a gap before a defaultless argument
		}
		store.scopes[leaf].args = append(store.scopes[leaf].args, args[i].Default)
	}
}

// requiredErrors reports required flags (across the covered chain) and required leaf
// arguments that were neither provided nor defaulted.
func requiredErrors(chain []Command, store *parsedInputs) error {
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
	spans := argSpans(leaf.Arguments, len(si.args))
	for i, ad := range leaf.Arguments {
		if store.covers(len(chain)-1) && ad.Required && spans[i][0] >= spans[i][1] {
			missing = append(missing, "<"+ad.Name+">")
		}
	}
	if len(missing) > 0 {
		return &ParseError{Kind: ParseKindMissingRequired, Msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

// flagLabel names a flag independent of what was typed: its first long identifier, else its
// first identifier, else --<name>.
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
// With "file", a value starting with '@' becomes the named file's contents, and one starting
// with "@@" is the value with one '@' removed, read from no file; with "stdin", a value of
// exactly "-" becomes the piped stdin, which must not be empty. Resolved text has one
// trailing line ending removed (trimAcquiredPayload, as the stdin channel does) and is then
// coerced and validated like a literal value. Without the matching mode, '@' and '-' are
// ordinary characters. Sentinels are argv grammar only: defaults and fallbacks never resolve.
// A relative `@file` path resolves against acq.dir; the error names it as typed.
func resolveFlagValue(fd FlagDef, label, value string, acq argvAcq) (string, error) {
	return resolveAcquired(fd.From, label, label, value, acq)
}

// resolveAcquired applies acquisition modes from to one argv value; see [resolveFlagValue].
// label names the input in a message, and flag is its flag label, "" for an argument.
func resolveAcquired(from []string, label, flag, value string, acq argvAcq) (string, error) {
	switch {
	case strings.HasPrefix(value, "@@") && slices.Contains(from, "file"):
		return value[1:], nil // a doubled @ is one literal @: @@alice is the value @alice
	case strings.HasPrefix(value, "@") && slices.Contains(from, "file"):
		path := joinDir(acq.dir, value[1:])
		data, err := os.ReadFile(path)
		if err != nil {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  label + ": " + unreadableFile(value[1:], path, err),
				Flag: flag, Token: value,
			}
		}
		return trimAcquiredPayload(string(data)), nil
	case value == "-" && slices.Contains(from, "stdin"):
		data, err := readStdin(acq.stdin)
		if err != nil {
			// An interrupted read keeps its own error, so the run's signal cause stays reachable.
			if ie, ok := errors.AsType[*InputError](err); ok {
				return "", ie
			}
			return "", &ParseError{Kind: ParseKindInvalidValue, Msg: label + ": could not read stdin", Flag: flag}
		}
		if len(data) == 0 {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: stdin is empty; %q asks for a piped value", label, "-"),
				Flag: flag,
			}
		}
		return trimAcquiredPayload(string(data)), nil
	}
	return value, nil
}

// unreadableFile says why an `@file` value could not be read in rotini's words rather than the
// platform-specific OS error text, as checkPathExists does. typed is the path as the user wrote
// it, path where it resolved.
func unreadableFile(typed, path string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("no such file: %q", typed)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("permission denied reading %q", typed)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return fmt.Sprintf("%q is a directory, not a file", typed)
	}
	return fmt.Sprintf("cannot read %q", typed)
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
//
// body and inline arrive split at the token's first "=", so for a value-taking flag the rest of
// the cluster is rejoined with it: -lapp=web is -l "app=web". An "=value" directly after the
// last flag belongs to that flag, a bool included (-Aw=false sets -w false).
func parseCluster(chain []Command, body, inline string, hasInline bool, argv []string, i int, addFlag func(idx int, fd FlagDef, value, typed string) error) (int, error) {
	var prev FlagDef
	for k := range len(body) {
		short := "-" + body[k:k+1]
		fdef, idx, ok := findFlagIndex(chain, short)
		if !ok {
			if err := digitsAfterSwitch(prev, body, k, hasInline); err != nil {
				return 0, err
			}
			return 0, &ParseError{Kind: ParseKindUnknownFlag, Msg: fmt.Sprintf("unknown flag %q", short), Flag: short, Token: short, Candidates: chainFlagIdentifiers(chain)}
		}
		prev = fdef
		if fdef.Type == "bool" && hasInline && k == len(body)-1 {
			return 0, addFlag(idx, fdef, inline, short)
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
		case rest != "" && hasInline:
			return 0, addFlag(idx, fdef, rest+"="+inline, short)
		case rest != "":
			return 0, addFlag(idx, fdef, rest, short)
		case hasInline:
			return 0, addFlag(idx, fdef, inline, short)
		case fdef.ImplicitValue != "":
			return 0, addFlag(idx, fdef, fdef.ImplicitValue, short)
		default:
			if i+1 >= len(argv) {
				return 0, &ParseError{Kind: ParseKindNeedsValue, Msg: short + " needs a value", Flag: short}
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

// digitsAfterSwitch reports a cluster whose undeclared rest, from position k, is all digits
// right after a count or bool flag, as in -v3: a number given to a flag that takes none. It
// carries no candidates, since the digits name no flag.
func digitsAfterSwitch(prev FlagDef, body string, k int, hasInline bool) error {
	if k == 0 || hasInline || (prev.Type != "count" && prev.Type != "bool") {
		return nil
	}
	digits := body[k:]
	if strings.TrimLeft(digits, "0123456789") != "" {
		return nil
	}
	flag := "-" + body[k-1:k]
	typed := flag + digits
	if prev.Type == "bool" {
		return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s takes no value (got %q)", flag, typed), Flag: flag, Token: typed}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n > 3 {
		n = 3
	}
	example := flag + strings.Repeat(body[k-1:k], max(n, 1)-1)
	return &ParseError{
		Kind:  ParseKindInvalidValue,
		Msg:   fmt.Sprintf("%s counts occurrences and takes no value; repeat it instead (%s), not %q", flag, example, typed),
		Flag:  flag,
		Token: typed,
	}
}

// findFlagIndex searches the chain leaf→root for a flag whose identifiers include name,
// returning its definition and the owning command's chain index.
func findFlagIndex(chain []Command, name string) (FlagDef, int, bool) {
	f, i, _, ok := findFlagMatch(chain, name)
	return f, i, ok
}

// findFlagMatch is findFlagIndex plus whether name matched a negated form ("--no-color" for a
// negatable "--color"). A declared identifier always wins over a negated one, so a declared
// "--no-cache" keeps its own meaning.
func findFlagMatch(chain []Command, name string) (def FlagDef, idx int, negated, ok bool) {
	for i, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if f.hasIdentifier(name) {
				return f, i, false, true
			}
		}
	}
	for i, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if f.Negatable && slices.Contains(negatedForms(f), name) {
				return f, i, true, true
			}
		}
	}
	return FlagDef{}, -1, false, false
}

// negatedIdentifiers returns the negated forms of a negatable flag: its declared [FlagDef.Negation],
// else the "--no-<x>" form of each long identifier. Short identifiers have no negated form.
func negatedIdentifiers(f FlagDef) []string {
	if !f.Negatable {
		return nil
	}
	if f.Negation != "" {
		return []string{f.Negation}
	}
	var out []string
	for _, id := range f.Identifiers {
		if name, ok := strings.CutPrefix(id, "--"); ok {
			out = append(out, "--no-"+name)
		}
	}
	return out
}

// namesChild reports whether tok names a sub-command or declared plugin of cur, by name or
// alias.
func namesChild(cur Command, tok string) bool {
	_, isChild := findChild(cur, tok)
	_, isPlugin := findPlugin(cur, tok)
	return isChild || isPlugin
}

// childCommandNames is the dispatchable vocabulary of a command's visible children, for a
// mistyped-command [*ParseError]'s Candidates.
func childCommandNames(cur Command) []string {
	var names []string
	for _, c := range cur.Commands {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name)
		names = append(names, c.Aliases...)
	}
	for _, r := range cur.Plugins {
		names = append(names, r.Name)
		names = append(names, r.Aliases...)
	}
	return names
}

// chainFlagIdentifiers is the non-hidden flag vocabulary of the whole chain, for an
// unknown-flag [*ParseError]'s Candidates.
func chainFlagIdentifiers(chain []Command) []string {
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
// root→leaf order. Field i binds to chain[offset+i], where offset ([frameAnchor]) places the
// last field on the caller's own frame; extra parent frames from a statically composed subtree
// go unbound.
func bindInputs(v reflect.Value, p *parsedInputs, chain []Command, offset int) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	if offset < 0 || offset+v.NumField() > len(p.scopes) {
		// Not an error here: bindInputs runs before argv is validated, and a short chain is
		// usually a symptom (an unknown command, a stray positional) with a better diagnostic.
		// checkFrameFit reports a genuine misfit after validation.
		return nil
	}
	if err := checkChainAlignment(v, chain, offset); err != nil {
		return err
	}
	for i := range v.NumField() {
		if err := bindCommandInputs(v.Field(i), p.scopes[offset+i], chain[offset+i], p.clock); err != nil {
			return err
		}
	}
	return nil
}

// frameAnchor is the chain index that field 0 of an inputs struct maps to.
//
// An inputs struct's last field describes the command whose handler is reading it, and the
// fields before it that command's ancestors, in order. So the anchor counts back from the
// caller's own frame:
//
//	offset = self - n + 1
//
// self is the index of [Context.Command], the command whose hook is running (see [AsCommand]).
// For a leaf hook this reduces to len(chain) - n.
//
// The anchor comes from the caller, never from the struct's shape: anchoring at the leaf would
// hand a cascading hook on a middle frame a descendant's flags, and anchoring at the root would
// miss a composed child's own frames, both silently.
func frameAnchor(v reflect.Value, chain []Command, self int) int {
	if v.Kind() != reflect.Struct {
		return 0
	}
	if self < 0 || self >= len(chain) {
		self = len(chain) - 1
	}
	return self - v.NumField() + 1
}

// checkFrameFit rejects an inputs struct that describes more commands than the caller's own
// frame is deep, which is what reading a descendant's inputs type looks like; [bindInputs]
// would otherwise skip it silently and leave it zeroed. It runs after argv is validated, so a
// short chain caused by a bad command line reports that error instead.
func checkFrameFit(v reflect.Value, chain []Command, self int) error {
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
					"command; a descendant's type cannot be collected from a shallower hook",
				displayTypeName(v.Type()), n, pathOf(chain[:self+1]), self+1),
		}
	}
	return nil
}

// checkChainAlignment rejects an inputs struct that does not describe the running command.
//
// Field i maps to chain[offset+i] ([frameAnchor]), so a shorter type always aligns against
// some frames: reading an ancestor's type from a deeper command would otherwise bind zeros, or
// the wrong command's values where flag names coincide, without any error.
//
// The check requires every flag a field names to be declared by the frame it maps onto; a
// weaker "shares some flag" test would pass on flags every command shares, such as help. Only
// flags are checked, since positional arguments carry no names to compare.
func checkChainAlignment(v reflect.Value, chain []Command, offset int) error {
	for i := range v.NumField() {
		flags := commandFlags(v.Field(i))
		if !flags.IsValid() || flags.NumField() == 0 {
			continue
		}
		frame := chain[offset+i]
		if frame.Name == "" && len(frame.Flags) == 0 {
			continue // an anonymous frame (a hand-built chain) declares nothing to check against
		}
		for sf := range typeLeaves(flags.Type()) {
			name := sf.Tag.Get("rotini")
			if name == "" {
				continue
			}
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
func pathOf(chain []Command) string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.Name
	}
	return strings.Join(names, " ")
}

// bindCommandInputs fills a <Cmd>CommandInputs struct's Flags and Arguments.
func bindCommandInputs(v reflect.Value, si scopeInputs, frame Command, clock *runClock) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := range v.NumField() {
		switch t.Field(i).Name {
		case "Flags":
			if err := bindFlags(v.Field(i), si, frame.Flags, clock); err != nil {
				return err
			}
		case "Arguments":
			if err := bindArgs(v.Field(i), si.args, frame.Arguments, clock); err != nil {
				return err
			}
		}
	}
	return nil
}

// bindFlags fills a <Cmd>Flags struct by matching each field's `rotini:"<name>"` tag against
// the parsed values, surfacing a coercion failure as a usage error naming the flag.
func bindFlags(v reflect.Value, si scopeInputs, defs []FlagDef, clock *runClock) error {
	flags := si.flags
	if v.Kind() != reflect.Struct {
		return nil
	}
	for sf, field := range structLeaves(v) {
		name := sf.Tag.Get("rotini")
		if name == "" {
			continue
		}
		raw, ok := flags[name]
		if !ok {
			continue
		}
		// An undeclared tag leaves def zero: plain coercion, not secret.
		def, declared := findFlagDef(defs, name)
		label := flagErrLabel(si, defs, name)
		if declared {
			switch {
			case def.DottedKeys:
				if err := coerceMapDotted(field, raw); err != nil {
					return coerceFailure(label, label, err, def.Secret)
				}
				continue
			case isObjectFlag(def):
				if err := bindObjectFlag(field, raw, def); err != nil {
					if _, dup := errors.AsType[duplicateObjectError](err); dup {
						return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s %v", label, err), Flag: label}
					}
					return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", label, err), Flag: label}
				}
				continue
			case def.Type == "count":
				// Each argv occurrence appended one marker; the field is the tally.
				if f := field; f.CanSet() && f.Kind() == reflect.Int {
					f.SetInt(int64(len(raw)))
				}
				continue
			}
		}
		raw = flagEnum(def).canonical(raw)
		if err := coerceTime(field, raw, flagTimeSpec(def, clock)); err != nil {
			return coerceFailure(label, label, err, def.Secret)
		}
	}
	return nil
}

// argSecret reports whether the positional at index i is declared secret; the struct field
// index is the declaration index.
func argSecret(defs []ArgDef, i int) bool {
	return i < len(defs) && defs[i].Secret
}

// flagErrLabel names flag name in a binding error: as typed on argv, else as [labelForFlag]. It
// is [scopeInputs.label] by logical name, for a tag whose definition may be missing.
func flagErrLabel(si scopeInputs, defs []FlagDef, name string) string {
	if t := si.typed[name]; t != "" {
		return t
	}
	return labelForFlag(defs, name)
}

// labelForFlag is a flag's CLI label (its identifiers) for error messages, falling back
// to the logical name when the definition isn't found.
func labelForFlag(defs []FlagDef, name string) string {
	for _, d := range defs {
		if d.Name == name {
			return flagLabel(d)
		}
	}
	return name
}

// bindArgs fills a <Cmd>Arguments struct positionally; a []string field is variadic and
// absorbs what the fields around it leave (see argSpans).
func bindArgs(v reflect.Value, args []string, defs []ArgDef, clock *runClock) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	spans := argSpans(fieldArgDefs(v, defs), len(args))
	for i := range v.NumField() {
		f := v.Field(i)
		label := "<" + t.Field(i).Tag.Get("rotini") + ">"
		var def ArgDef
		if i < len(defs) {
			def = defs[i]
		}
		s := spans[i]
		if f.Kind() != reflect.Slice && s[0] >= s[1] {
			continue
		}
		if err := coerceTime(f, argEnum(def).canonical(args[s[0]:s[1]]), argTimeSpec(def, clock)); err != nil {
			return coerceFailure(label, "", err, argSecret(defs, i))
		}
	}
	return nil
}

// fieldArgDefs pairs each field of an Arguments struct with its declaration, reading a slice
// field as variadic (whatever its element type) where defs doesn't reach.
func fieldArgDefs(v reflect.Value, defs []ArgDef) []ArgDef {
	out := make([]ArgDef, v.NumField())
	for i := range out {
		if i < len(defs) {
			out[i] = defs[i]
		}
		out[i].Variadic = v.Field(i).Kind() == reflect.Slice
	}
	return out
}

// splitValue splits one value on a list input's separator, CSV-style: an item in double quotes
// keeps the separator ("a,b"), leading spaces are trimmed, and an empty value is no items — so
// `--tags ""` clears to an empty list. With no separator the value is one item, untouched. A
// NUL separator splits on the byte alone.
func splitValue(value, sep string) ([]string, error) {
	if sep == "" {
		return []string{value}, nil
	}
	if value == "" {
		return nil, nil
	}
	if sep == "\x00" {
		return strings.Split(value, sep), nil // NUL can't be a CSV delimiter; nothing is quoted
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

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// coerce sets f from the raw string values (the last one, for a scalar), returning an error
// when a value cannot be parsed into f's type, including a custom type's own
// [encoding.TextUnmarshaler] error. An unparseable value is reported, never silently zeroed.
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
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return coerceNumber(f, last)
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

// unsupportedType is the error for a field type coerce has no rule for; the fix is to give the
// type an UnmarshalText method.
func unsupportedType(t reflect.Type) error {
	return authorMistake(fmt.Sprintf("cannot parse into %s; the type must implement encoding.TextUnmarshaler", t))
}

// authorMistake is a coercion failure no value could have avoided: the field's type is wrong
// for what the Definition declares. [coerceFailure] reports it as [ParseKindInternal], not as
// the end user's invalid value.
type authorMistake string

func (e authorMistake) Error() string { return string(e) }

// coerceFailure is the error for a value that could not be coerced into its field: the user's
// invalid value, or for an [authorMistake] the program's bug. label names the input as the
// user wrote it; flag is the flag label, "" for an argument.
func coerceFailure(label, flag string, err error, secret bool) *ParseError {
	if mistake, ok := errors.AsType[authorMistake](err); ok {
		return &ParseError{Kind: ParseKindInternal, Msg: fmt.Sprintf("rotini: %s: %s", label, mistake), Flag: flag}
	}
	return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %s", label, coerceMessage(err, secret)), Flag: flag}
}

// coerceSlice fills a slice field from the raw values (one per repeated flag occurrence, or the
// trailing positionals of a variadic argument), coercing each into the element type.
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

// coerceError is the standard "value isn't a <type>" failure. It holds the offending value
// separately from its rendering because redaction must happen where the FlagDef/ArgDef is in
// scope, several frames above where coercion fails; a caller that knows the input is secret
// re-renders it redacted.
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
	// A cause usually quotes the rejected value, so a secret's message omits it.
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

// coerceNumber fills an integer, unsigned or float field. Each is parsed at 64 bits and then
// checked against the field's width, so a value too big for a narrower field is rejected
// rather than wrapped.
func coerceNumber(f reflect.Value, last string) error {
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(last, 10, 64)
		if err != nil {
			return notValid(last, "non-negative integer")
		}
		if f.OverflowUint(n) {
			return outOfRange(last, f.Type())
		}
		f.SetUint(n)
	case reflect.Float32, reflect.Float64:
		x, err := strconv.ParseFloat(last, 64)
		if err != nil {
			return notValid(last, "number")
		}
		if f.OverflowFloat(x) {
			return outOfRange(last, f.Type())
		}
		f.SetFloat(x)
	default:
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil {
			return notValid(last, "integer")
		}
		if f.OverflowInt(n) {
			return outOfRange(last, f.Type())
		}
		f.SetInt(n)
	}
	return nil
}

// outOfRange is the error for a number that parsed but does not fit its field's type, naming
// the range: `"300" is not a valid int8 (out of range: -128 to 127)`.
func outOfRange(value string, t reflect.Type) error {
	var bounds string
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		bounds = fmt.Sprintf("%d to %d", int64(-1)<<(bits-1), int64(1)<<(bits-1)-1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bounds = fmt.Sprintf("0 to %d", uint64(1)<<t.Bits()-1)
	default: // float32: a float64 field cannot overflow a value ParseFloat(…, 64) accepted
		bounds = fmt.Sprintf("±%g", math.MaxFloat32)
	}
	return &coerceError{Value: value, TypeName: typeLabel(t), Cause: fmt.Errorf("out of range: %s", bounds)}
}

// coerceMapDotted fills a map[string]any flag from "key=value" pairs whose keys are dotted
// paths into nested maps: "image.tag=v2" → m["image"].(map[string]any)["tag"] = "v2". Values
// are typed by [inferScalar]; intermediate maps are created as needed and later pairs win.
// Malformed pairs are skipped; an empty path segment is an error.
func coerceMapDotted(f reflect.Value, raw []string) error {
	m := map[string]any{}
	if !reflect.TypeFor[map[string]any]().AssignableTo(f.Type()) {
		return authorMistake(fmt.Sprintf("dotted keys need a map[string]any flag, not %s", f.Type()))
	}
	for _, pair := range raw {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		segs := strings.Split(k, ".")
		if slices.Contains(segs, "") {
			return fmt.Errorf("invalid key path %q; empty segment", k)
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

// coerceMap fills a string-keyed map field from raw "key=value" pairs, splitting on the first
// '='. The value is coerced into the map's element type; an `any` element gets [inferScalar]'s
// reading of it (a bool, number, nil or the string). Later pairs win on a duplicate key, and
// malformed pairs are skipped — validation rejects them.
func coerceMap(f reflect.Value, raw []string) error {
	kt := f.Type().Key()
	if kt.Kind() != reflect.String {
		return authorMistake(fmt.Sprintf("cannot parse into %s; a map flag's keys must be strings", f.Type()))
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
