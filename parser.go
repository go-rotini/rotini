package rotini

import (
	"encoding"
	"fmt"
	"io"
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

// KeyParser is the conventional registry key the generated main binds the
// [Parser] under (and handlers retrieve it by) — see [Program.Bind].
const KeyParser = "parser"

// parsedInputs is one invocation's parsed argv, indexed by position in the
// resolved command chain (root = 0 … leaf). [Parse] builds it via [parseInto] and
// reads it back through the reflective binder. Keying by chain position — not by
// command name — means two commands on a single path can never collide, and a
// statically-composed child still reads its inputs through its own generated types
// unchanged: those describe its own root→leaf tail of the chain, bound leaf-first.
type parsedInputs struct {
	scopes  []scopeInputs     // one entry per resolved chain frame, root → leaf
	argvSet []map[string]bool // per frame: logical flag names explicitly set on argv (not defaults, not env/config fallback)
}

// setOnArgv reports whether the flag with logical name was explicitly provided
// on argv in chain frame idx — the presence convention flag groups and
// dependencies enforce (a default or env/config fallback does not count).
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
}

// ParseKind classifies a [ParseError] by what went wrong, so a funnel can
// branch on the failure WITHOUT matching the human message — the message is
// presentation, the kind is data. Most kinds are the end-user's to fix (a typo,
// a bad value); [ParseKindInternal] is a misuse of the parser API itself (a nil
// parser/context or a bad out argument) — still surfaced as a usage-shaped
// [*ParseError] for uniformity, but the author's bug.
type ParseKind int

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

// ParseError is a parse-time failure caused by bad input. It is data, not
// presentation: the message carries no opinions (no suggestions, no usage
// dump), and the structured fields let a handler compose its own response —
// switch on [ParseError.Kind] to branch, pair Token with Candidates and a bound
// tortellini.Suggestor for "did you mean", or render help for Command. Handlers
// conventionally map it to exit code 2 (the usual CLI usage-error code). It
// unwraps to [ErrUsage], so CategoryOf classifies it as CategoryUsage; retrieve
// the fields with errors.As:
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

func (e *ParseError) Error() string { return e.Msg }

func (e *ParseError) Unwrap() error { return ErrUsage }

// Parser is rotini's argument parser, and it is a *service*: a CLI binds it to the
// context registry under the key "parser" so that (1) parsing is opt-in — a CLI
// that wants raw argv binds nothing and reads Context.Args itself — and
// (2) the registry is a dependency-injection seam (the same handler code runs
// whether you bound the production parser or a double). A handler retrieves it and
// calls [Parser.Parse]:
//
//	// main.go
//	rth.Program.Bind(rotini.KeyParser, rotini.NewParser()).Execute()
//
//	// a handler
//	parser := rotini.MustGet[*rotini.Parser](rtx, rotini.KeyParser)
//	var in rtg.MycliInputs
//	err := parser.Parse(rtx, &in)
type Parser struct{}

// NewParser returns rotini's default [Parser], ready to bind under the "parser"
// registry key.
func NewParser() *Parser {
	return &Parser{}
}

// Parse binds the running command's arguments into out — a non-nil pointer to the
// typed inputs struct rtg emits (e.g. &rtg.MycliInputs{}) — using the resolved
// command and raw argv on rtx, in the json.Unmarshal style:
//
//	var in rtg.MycliInputs
//	if err := parser.Parse(rtx, &in); err != nil { /* handler owns it */ }
//
// It applies declared defaults and validates required/enum, then fills out by
// reflection from the `rotini:"…"` struct tags (each per-command field carries
// `scope=<command-name>`, each flag/argument field its logical name; built-ins and
// any encoding.TextUnmarshaler are coerced; a trailing []string absorbs remaining
// positionals). Parse returns an error when out is not a non-nil pointer, an
// unknown flag is given, a flag's value is missing, a required input is absent, or
// a value is outside a declared enum — print it (see [Usage]), rtx.Exit, or fall
// back to Context.Args.
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

// Deprecation is a deprecated CLI token found in this invocation's argv: the specific
// deprecated identifier/alias used, the kind of input, and that input's logical name. It
// is pure identification — the framework attaches no message and does nothing with it; the
// handler decides (print a warning, emit telemetry, fail the run, ignore). It implements
// error so it can be returned or printed directly.
type Deprecation struct {
	Kind       string // "flag" or "command"
	Name       string // the input's logical name (the flag/command name)
	Identifier string // the deprecated token actually used on argv (e.g. "--conf", "build")
}

func (d Deprecation) Error() string {
	return fmt.Sprintf("deprecated %s identifier %q was used", d.Kind, d.Identifier)
}

// Deprecations scans this invocation's argv against the spec's deprecated_identifiers and
// returns each deprecated token that was actually used — a command invoked via a deprecated
// alias, or a flag set via a deprecated identifier (the non-deprecated spellings are
// unaffected). It is a data feed only: the framework prints nothing; the handler decides
// what to do with each (warn, telemetry, exit, ignore). It does not parse and holds no
// parser state — call it any time the context's chain is resolved (typically after Parse).
func (p *Parser) Deprecations(rtx *Context) []Deprecation {
	if rtx == nil {
		return nil
	}
	argv := rtx.Args
	var out []Deprecation
	for _, frame := range rtx.Chain() {
		// A command invoked via one of its deprecated aliases (frame.Matched is the token
		// that resolved it).
		for _, alias := range frame.DeprecatedIdentifiers {
			if frame.Matched == alias {
				out = append(out, Deprecation{Kind: "command", Name: frame.Name, Identifier: alias})
				break
			}
		}
		// A flag set via one of its deprecated identifiers.
		for _, fd := range frame.Flags {
			for _, id := range fd.DeprecatedIdentifiers {
				if flagWasSet(argv, []string{id}) {
					out = append(out, Deprecation{Kind: "flag", Name: fd.Name, Identifier: id})
				}
			}
		}
	}
	return out
}

// parseBind parses argv into a store and binds it into out, but performs no
// required/enum/constraint validation — that is [validate]'s job. It is the shared
// front half of [Parser.Parse] (which then validates the argv-only store) and of the
// [Binder] (which first reconciles env/config fallbacks into the store, then
// validates last — so a required input is satisfiable from any source, not just
// argv). It returns the store and the resolved chain for that deferred validation.
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
	store, err := parseInto(chain, rtx.Args, rtx.Stdin)
	if err != nil {
		return nil, nil, err
	}
	if err := bindInputs(rv.Elem(), store, chain); err != nil {
		return nil, nil, err
	}
	return store, chain, nil
}

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized
// flag, or a flag missing its value, is a [ParseError]. Command tokens already in
// the chain are consumed; everything after the leaf command (and after "--") is a
// positional argument of the leaf. Declared defaults are applied. parseInto does
// not check required inputs or enums — that is [validate]'s job — so a handler can
// inspect what was supplied before deciding how strict to be.
func parseInto(chain []ResolvedCommand, argv []string, stdin io.Reader) (*parsedInputs, error) {
	store, err := parseArgvTokens(chain, argv, stdin)
	if err != nil {
		return nil, err
	}
	applyDefaults(chain, store)
	return store, nil
}

// parseArgvTokens is parseInto minus defaults: exactly what argv supplied,
// nothing more. It records each explicitly-set flag in the store's argvSet, the
// single source of truth for "set on the command line" (flag groups,
// dependencies, and the argv overlay layer all read it). stdin backs the
// from:stdin sentinel — a flag value of exactly "-" on a flag that opted in
// (it is only read when such a value actually appears).
func parseArgvTokens(chain []ResolvedCommand, argv []string, stdin io.Reader) (*parsedInputs, error) {
	store := &parsedInputs{
		scopes:  make([]scopeInputs, len(chain)),
		argvSet: make([]map[string]bool, len(chain)),
	}
	leaf := len(chain) - 1 // chain index of the leaf command
	depth := 1             // index of the next chain frame we might descend into
	startedArgs := false   // a positional has been seen: command descent is over
	terminated := false    // "--" has been seen: flag parsing is over too

	addFlag := func(idx int, fd FlagDef, value string) error {
		value, err := resolveFlagValue(fd, value, stdin)
		if err != nil {
			return err
		}
		if store.scopes[idx].flags == nil {
			store.scopes[idx].flags = map[string][]string{}
		}
		store.scopes[idx].flags[fd.Name] = append(store.scopes[idx].flags[fd.Name], value)
		if store.argvSet[idx] == nil {
			store.argvSet[idx] = map[string]bool{}
		}
		store.argvSet[idx][fd.Name] = true
		return nil
	}
	addArg := func(value string) {
		store.scopes[leaf].args = append(store.scopes[leaf].args, value)
	}

	// Passthrough: once the chain's passthrough leaf has been entered, every
	// remaining token — flag-shaped, "--", anything — is a raw positional.
	passthrough := func() bool {
		return chain[len(chain)-1].Passthrough && depth == len(chain)
	}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		if passthrough() {
			addArg(tok)
			continue
		}

		if !terminated && tok == "--" { // explicit end of flags; the rest are
			terminated = true  // positional, even flag-looking tokens (and any
			startedArgs = true // further "--" is a literal positional)
			continue
		}

		if !terminated && isFlag(tok) {
			name, inline, hasInline := splitFlag(tok)
			fdef, idx, ok := findFlagIndex(chain, name)
			if !ok {
				// No exact identifier — try POSIX clustered short flags: -vh → -v
				// -h, -n5 → -n 5. Long flags ("--" prefix) never cluster.
				if isShortCluster(name) {
					consumed, err := parseCluster(chain, name[1:], inline, hasInline, argv, i, addFlag)
					if err != nil {
						return nil, err
					}
					i += consumed
					continue
				}
				return nil, &ParseError{
					Kind:       ParseKindUnknownFlag,
					Msg:        fmt.Sprintf("unknown flag %q", name),
					Flag:       name,
					Token:      name,
					Candidates: chainFlagIdentifiers(chain),
				}
			}
			var value string
			switch {
			case fdef.Type == "count":
				if hasInline {
					return nil, &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("flag %q counts occurrences and takes no value", name), Flag: name}
				}
				value = "1" // each occurrence appends one marker; the binder tallies them
			case fdef.Type == "bool":
				value = "true"
				if hasInline {
					value = inline
				}
			case hasInline:
				value = inline
			default:
				i++
				if i >= len(argv) {
					return nil, &ParseError{Kind: ParseKindNeedsValue, Msg: fmt.Sprintf("flag %q needs a value", name), Flag: name}
				}
				value = argv[i]
			}
			if err := addFlag(idx, fdef, value); err != nil {
				return nil, err
			}
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
		addArg(tok)
	}

	return store, nil
}

// validate enforces the declarative constraints on the resolved chain against a
// parsed store: a stray positional on a branch-only command is a mistyped
// sub-command; required flags/arguments must be present (or defaulted); and any
// value for a flag or argument that declares an Enum must be one of its members.
// It is the strict half of [Parse]; a handler that wants laxer behavior can bind
// inputs without it.
func validate(chain []ResolvedCommand, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]

	// A stray positional on a command that branches but takes no arguments is a
	// mistyped sub-command, not an argument. The error carries the sibling
	// vocabulary so a handler (with a bound tortellini.Suggestor) can offer corrections.
	if len(leaf.Commands) > 0 && len(leaf.Arguments) == 0 && len(si.args) > 0 {
		tok := si.args[0]
		return &ParseError{
			Kind:       ParseKindUnknownCommand,
			Msg:        fmt.Sprintf("unknown command %q for %q", tok, leaf.Name),
			Command:    leaf.Name,
			Token:      tok,
			Candidates: childCommandNames(leaf),
		}
	}

	if err := requiredErrors(chain, store); err != nil {
		return err
	}

	for i, f := range chain {
		fsi := store.scopes[i]
		for _, fd := range f.Flags {
			vals := fsi.flags[fd.Name]
			for _, v := range vals {
				if len(fd.Enum) > 0 && !slices.Contains(fd.Enum, v) {
					return &ParseError{
						Kind:       ParseKindEnumViolation,
						Msg:        fmt.Sprintf("invalid value %q for %s (one of: %s)", redactValue(v, fd.Secret), flagLabel(fd), strings.Join(fd.Enum, ", ")),
						Flag:       flagLabel(fd),
						Token:      redactValue(v, fd.Secret),
						Candidates: fd.Enum,
					}
				}
				if isMapType(fd.Type) && !strings.Contains(v, "=") {
					return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s expects key=value pairs (got %q)", flagLabel(fd), redactValue(v, fd.Secret)), Flag: flagLabel(fd)}
				}
			}
			if err := checkConstraints(flagLabel(fd), fd.Type, fd.Constraints, vals, fd.Secret); err != nil {
				return err
			}
		}
	}

	// With no variadic argument to absorb them, more positionals than declared
	// arguments is a usage error rather than a silent drop. (A branch-only command's
	// stray positional was handled above as a mistyped sub-command.)
	if n := len(leaf.Arguments); !hasVariadicArg(leaf.Arguments) && len(si.args) > n {
		if n == 0 {
			return &ParseError{Kind: ParseKindNoArguments, Msg: fmt.Sprintf("%q takes no arguments (got %d)", leaf.Name, len(si.args)), Command: leaf.Name}
		}
		return &ParseError{Kind: ParseKindTooManyArguments, Msg: fmt.Sprintf("%q accepts at most %d %s (got %d)", leaf.Name, n, plural("argument", n), len(si.args)), Command: leaf.Name}
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
			if len(ad.Enum) > 0 && !slices.Contains(ad.Enum, v) {
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

// checkConstraints enforces an input's declared numeric/string/array bounds against
// the value(s) supplied for it (one element for a scalar; possibly many for a
// repeatable flag or variadic argument). label is the human-facing identifier; typ is
// the resolved Go type. A zero bound (or empty pattern) is unset and skipped. Numeric
// bounds apply to the full int/uint/float family, length/pattern to strings, and item
// counts to arrays/maps; for a repeatable input the per-value checks apply to each
// ELEMENT (a []int's minimum bounds every occurrence). A constraint declared on a
// type none of those fit cannot come from a valid spec (lintConstraintApplicability
// rejects it at validate time); a hand-built Definition that does it anyway is
// skipped here, not guessed at. When secret is true the offending value (and its
// length) is redacted in the error.
func checkConstraints(label, typ string, c Constraints, values []string, secret bool) error {
	if isArrayType(typ) || isMapType(typ) {
		switch n := len(values); {
		case c.MinItems > 0 && n < c.MinItems:
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s needs at least %d %s (got %d)", label, c.MinItems, plural("value", c.MinItems), n)}
		case c.MaxItems > 0 && n > c.MaxItems:
			return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s accepts at most %d %s (got %d)", label, c.MaxItems, plural("value", c.MaxItems), n)}
		}
	}
	elem := constraintElemType(typ)
	for _, v := range values {
		switch {
		case isNumericType(elem):
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue // not range-checkable; coerce already tolerates malformed input
			}
			if c.Minimum != nil && n < *c.Minimum {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be >= %s (got %s)", label, formatNum(*c.Minimum), redactValue(v, secret))}
			}
			if c.Maximum != nil && n > *c.Maximum {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be <= %s (got %s)", label, formatNum(*c.Maximum), redactValue(v, secret))}
			}
			if c.ExclusiveMinimum != nil && n <= *c.ExclusiveMinimum {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be > %s (got %s)", label, formatNum(*c.ExclusiveMinimum), redactValue(v, secret))}
			}
			if c.ExclusiveMaximum != nil && n >= *c.ExclusiveMaximum {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be < %s (got %s)", label, formatNum(*c.ExclusiveMaximum), redactValue(v, secret))}
			}
			if c.MultipleOf != nil && !isMultipleOf(n, *c.MultipleOf) {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be a multiple of %s (got %s)", label, formatNum(*c.MultipleOf), redactValue(v, secret))}
			}
		case elem == "string":
			ln := utf8.RuneCountInString(v)
			gotLen := strconv.Itoa(ln)
			if secret {
				gotLen = "[redacted]"
			}
			if c.MinLength > 0 && ln < c.MinLength {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be at least %d %s long (got %s)", label, c.MinLength, plural("character", c.MinLength), gotLen)}
			} else if c.MaxLength > 0 && ln > c.MaxLength {
				return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must be at most %d %s long (got %s)", label, c.MaxLength, plural("character", c.MaxLength), gotLen)}
			}
			if c.Pattern != "" {
				if ok, err := regexp.MatchString(c.Pattern, v); err == nil && !ok {
					return &ParseError{Kind: ParseKindConstraintViolation, Msg: fmt.Sprintf("%s must match %s (got %q)", label, c.Pattern, redactValue(v, secret))}
				}
			}
		}
	}
	return nil
}

// isMultipleOf reports whether n is an integer multiple of m (JSON Schema
// semantics: the division yields an integer), with a small relative tolerance
// for float representation (1.2 / 0.1 must count). A non-positive m never
// matches — validation rejects it before it gets here.
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

// validateFlagGroups enforces each command's cross-flag presence rules (mutually
// exclusive / required together / one-of / at-least-one). "Set" means explicitly
// provided on argv — a default or env/config fallback does not count (matching the
// command-line-presence convention of cobra/clap) — read from the store's argvSet
// (which, unlike a raw argv re-scan, also sees flags set inside short clusters).
// Each violation is a usage error.
func validateFlagGroups(chain []ResolvedCommand, store *parsedInputs) error {
	for i, f := range chain {
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

// validateFlagDependencies enforces each command's conditional cross-flag requirements:
// when a dependency's When flag is explicitly set on argv, every flag it Requires must
// also be set. "Set" follows the same explicit-argv convention as flag groups (the
// store's argvSet); a missing requirement is a usage error naming the absent flag(s)
// and the trigger.
func validateFlagDependencies(chain []ResolvedCommand, store *parsedInputs) error {
	for i, f := range chain {
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

// numericFamily is every resolved type string whose values are range-checkable
// numbers — the full int/uint/float vocabulary plus the JSON-Schema aliases
// (hand-built Definitions may use either spelling). The old allowlist was
// int|float64 only, which silently ignored bounds on every other numeric type
// (production-readiness R1).
var numericFamily = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"integer": true, "number": true,
}

func isNumericType(typ string) bool { return numericFamily[typ] }

// constraintElemType is the type a constraint's PER-VALUE checks apply to: the
// element type for an array/repeatable input (each occurrence is one value),
// the type itself otherwise. Item-count bounds stay on the collection.
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
func applyDefaults(chain []ResolvedCommand, store *parsedInputs) {
	for i, f := range chain {
		for _, fd := range f.Flags {
			if fd.Default == "" {
				continue
			}
			if store.scopes[i].flags == nil {
				store.scopes[i].flags = map[string][]string{}
			}
			if _, ok := store.scopes[i].flags[fd.Name]; !ok {
				store.scopes[i].flags[fd.Name] = []string{fd.Default}
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
		if ad.Required && i >= len(si.args) {
			missing = append(missing, "<"+ad.Name+">")
		}
	}
	if len(missing) > 0 {
		return &ParseError{Kind: ParseKindMissingRequired, Msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

func flagLabel(f FlagDef) string {
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

// resolveFlagValue applies a flag's declared acquisition modes (spec `from:`)
// to one argv-supplied value. With "file", a value starting with '@' is
// replaced by the named file's contents; with "stdin", a value of exactly "-"
// is replaced by the piped stdin (which must not be empty — giving "-" demands
// a pipe). Resolved text is whitespace-trimmed (token files end in a newline)
// and then flows through the same coercion/enum/constraint checks as a literal
// value — the flag's value IS the resolved text. Without the matching `from`
// mode, '@' and '-' are ordinary characters. Declared defaults and env/config
// fallbacks never resolve — sentinels are argv grammar.
func resolveFlagValue(fd FlagDef, value string, stdin io.Reader) (string, error) {
	switch {
	case strings.HasPrefix(value, "@") && slices.Contains(fd.From, "file"):
		data, err := os.ReadFile(value[1:])
		if err != nil {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: cannot read %q: %v", flagLabel(fd), value, err),
				Flag: flagLabel(fd), Token: value,
			}
		}
		return strings.TrimSpace(string(data)), nil
	case value == "-" && slices.Contains(fd.From, "stdin"):
		data, err := readStdin(stdin)
		if err != nil {
			return "", &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: read stdin: %v", flagLabel(fd), err), Flag: flagLabel(fd)}
		}
		if len(data) == 0 {
			return "", &ParseError{
				Kind: ParseKindInvalidValue,
				Msg:  fmt.Sprintf("%s: stdin is empty — %q asks for a piped value", flagLabel(fd), "-"),
				Flag: flagLabel(fd),
			}
		}
		return strings.TrimSpace(string(data)), nil
	}
	return value, nil
}

// isShortCluster reports whether name is a candidate POSIX short-flag cluster: a
// single-dash token with more than one character (e.g. "-vh", "-n5") — as opposed
// to a long flag ("--x") or a bare short flag ("-v", which the exact match already
// handled). Clustering is tried only after an exact-identifier lookup misses.
func isShortCluster(name string) bool {
	return len(name) > 2 && name[0] == '-' && name[1] != '-'
}

// parseCluster expands a POSIX short-flag cluster (body is the characters after
// the leading '-', e.g. "vh" from -vh, or "n5" from -n5) against the chain. Each
// character is a single short flag, looked up as "-<c>": booleans are set in turn,
// and the first value-taking flag consumes the rest of the cluster, then the
// inline "=value", then the next argv token — whichever is present. It returns how
// many extra argv tokens it consumed (0 or 1).
func parseCluster(chain []ResolvedCommand, body, inline string, hasInline bool, argv []string, i int, addFlag func(idx int, fd FlagDef, value string) error) (int, error) {
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
			if err := addFlag(idx, fdef, v); err != nil {
				return 0, err
			}
			continue
		}
		// A value-taking flag ends the cluster: its value is whatever follows.
		switch rest := body[k+1:]; {
		case rest != "":
			return 0, addFlag(idx, fdef, rest)
		case hasInline:
			return 0, addFlag(idx, fdef, inline)
		default:
			if i+1 >= len(argv) {
				return 0, &ParseError{Kind: ParseKindNeedsValue, Msg: fmt.Sprintf("flag %q needs a value", short), Flag: short}
			}
			return 1, addFlag(idx, fdef, argv[i+1])
		}
	}
	// Every flag in the cluster was boolean; a trailing "=value" has nothing to bind.
	if hasInline {
		return 0, &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("flag %q does not take a value", "-"+body), Flag: "-" + body}
	}
	return 0, nil
}

// findFlagIndex searches the resolved chain leaf→root for a flag whose identifiers
// include name, returning its definition and the chain index of the command that
// owns it.
func findFlagIndex(chain []ResolvedCommand, name string) (FlagDef, int, bool) {
	for i, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if slices.Contains(f.Identifiers, name) {
				return f, i, true
			}
		}
	}
	return FlagDef{}, -1, false
}

// childCommandNames is the dispatchable-name vocabulary of a command's visible
// children — sub-command names and aliases, plus declared remotes — for a
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

// chainFlagIdentifiers is the declared, non-hidden flag vocabulary of the whole
// resolved chain (ancestor flags resolve on descendants), for an unknown-flag
// [ParseError]'s Candidates.
func chainFlagIdentifiers(chain []ResolvedCommand) []string {
	var ids []string
	for _, v := range slices.Backward(chain) {
		for _, f := range v.Flags {
			if !f.Hidden {
				ids = append(ids, f.Identifiers...)
			}
		}
	}
	return ids
}

// bindInputs fills a <Cmd>Inputs struct: one field per command on the resolved
// path, in root→leaf order. The fields bind to the tail of the chain aligned at
// the leaf, so each command's inputs come from the right frame no matter how deep
// it was reached — including a statically-composed subtree reached under extra
// parent frames, which simply go unbound.
func bindInputs(v reflect.Value, p *parsedInputs, chain []ResolvedCommand) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	offset := len(p.scopes) - v.NumField()
	if offset < 0 {
		return nil // the struct names more commands than the chain has frames
	}
	for i := range v.NumField() {
		if err := bindCommandInputs(v.Field(i), p.scopes[offset+i], chain[offset+i]); err != nil {
			return err
		}
	}
	return nil
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
			if err := bindFlags(v.Field(i), si.flags, frame.Flags); err != nil {
				return err
			}
		case "Arguments":
			if err := bindArgs(v.Field(i), si.args); err != nil {
				return err
			}
		}
	}
	return nil
}

// bindFlags fills a <Cmd>Flags struct by matching each field's `rotini:"<name>"`
// tag against the parsed flag values, surfacing a coercion failure as a usage error
// naming the flag (by its CLI identifiers).
func bindFlags(v reflect.Value, flags map[string][]string, defs []FlagDef) error {
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
					return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", labelForFlag(defs, name), err), Flag: labelForFlag(defs, name)}
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
		if err := coerce(v.Field(i), raw); err != nil {
			return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", labelForFlag(defs, name), err), Flag: labelForFlag(defs, name)}
		}
	}
	return nil
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

// bindArgs fills a <Cmd>Arguments struct positionally; a trailing []string field
// is variadic and absorbs all remaining positionals. A coercion failure is a usage
// error naming the argument.
func bindArgs(v reflect.Value, args []string) error {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	idx := 0
	for i := range v.NumField() {
		f := v.Field(i)
		label := "<" + t.Field(i).Tag.Get("rotini") + ">"
		if f.Kind() == reflect.Slice { // a slice argument is variadic, whatever its element type
			if err := coerce(f, args[min(idx, len(args)):]); err != nil {
				return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", label, err)}
			}
			idx = len(args)
			continue
		}
		if idx < len(args) {
			if err := coerce(f, args[idx:idx+1]); err != nil {
				return &ParseError{Kind: ParseKindInvalidValue, Msg: fmt.Sprintf("%s: %v", label, err)}
			}
			idx++
		}
	}
	return nil
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// coerce sets f from the raw string value(s), returning an error when a value cannot be
// parsed into f's type — including a custom type's own [encoding.TextUnmarshaler] error,
// the per-type parse+validate hook. (The caller turns that into a usage error naming the
// flag/argument.) It never panics: an unparseable value is reported, not silently zeroed.
func coerce(f reflect.Value, raw []string) error {
	if len(raw) == 0 {
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
		d, err := time.ParseDuration(last)
		if err != nil {
			return notValid(last, "duration")
		}
		f.SetInt(int64(d))
		return nil
	}
	if f.CanAddr() && f.Addr().Type().Implements(textUnmarshalerType) {
		if err := f.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(last)); err != nil {
			return fmt.Errorf("%q is not a valid %s (%w)", last, f.Type(), err)
		}
		return nil
	}

	switch f.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(last)
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

// unsupportedType is the loud refusal for a field type coerce has no rule for:
// silence here would zero the field and hide a spec/codegen mistake. The fix is
// the documented contract — give the type an UnmarshalText.
func unsupportedType(t reflect.Type) error {
	return fmt.Errorf("cannot parse into %s — the type must implement encoding.TextUnmarshaler", t)
}

// coerceSlice fills a slice field from the raw values (one per repeated flag
// occurrence, or the trailing positionals for a variadic argument), coercing
// each element into the slice's element type — []string verbatim, []int parsed,
// []time.Duration / TextUnmarshaler elements through their own parsers.
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

// notValid is the standard "value isn't a <type>" coercion error.
func notValid(value, typeName string) error {
	return fmt.Errorf("%q is not a valid %s", value, typeName)
}

// coerceMap fills a string-keyed map field from raw "key=value" pairs (one per repeated
// flag occurrence), splitting on the first '='. The value is coerced into the map's
// element type (string/int/…); an `any` element stores the raw string. Later pairs win
// on a duplicate key. Malformed (no '=') pairs are skipped — validation rejects them. A
// value that doesn't parse into the element type is returned as an error.
// coerceMapDotted fills a map[string]any flag (spec dotted_keys) from raw
// "key=value" pairs whose keys are '.'-separated paths into nested maps:
// "image.tag=v2" → m["image"].(map[string]any)["tag"] = "v2". Each assignment
// overwrites whatever sits at its path (creating intermediate maps as needed),
// so later pairs win — including a pair that replaces a scalar with a subtree
// or vice versa. Malformed (no '=') pairs are skipped — validation rejects
// them; an empty path segment ("a..b", ".a", "a.") is an error.
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
		cur[segs[len(segs)-1]] = v
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
			ev.Set(reflect.ValueOf(v))
		} else if err := coerce(ev, []string{v}); err != nil {
			return fmt.Errorf("value for key %q: %w", k, err)
		}
		m.SetMapIndex(reflect.ValueOf(k).Convert(kt), ev)
	}
	f.Set(m)
	return nil
}
