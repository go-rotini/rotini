// Package rtk is rotini's opt-in toolkit: the rotini-flavored functionality a CLI
// can choose to use on top of the core runtime, but does not have to. Core rotini
// (the github.com/go-rotini/rotini package) guarantees only two things — codegen
// of the handler files, and command resolution + lifecycle dispatch. Everything
// else is opt-in and lives here, each piece a service a CLI binds to the context
// and a handler retrieves with rtx.Get:
//
//   - [Parser] — parse the resolved command's arguments into a typed inputs
//     struct ([Parser.Parse]); render standard help with [Usage].
//   - [IO] — injectable, testable stdin/stdout/stderr.
//   - [CompletionScript] — generate bash/zsh/fish shell completion scripts.
//
// A handler reaches for rtk when it wants rotini's conventions; a handler that
// disagrees ignores rtk entirely — reading the raw argument vector via
// rotini.Context.Args and writing to os.Stdout however it likes.
package rtk

import (
	"encoding"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-rotini/rotini"
)

// parsedInputs is one invocation's parsed argv, indexed by position in the
// resolved command chain (root = 0 … leaf). [Parse] builds it via [parseInto] and
// reads it back through the reflective binder. Keying by chain position — not by
// command name — means two commands on a single path can never collide, and a
// statically-composed child still reads its inputs through its own generated types
// unchanged: those describe its own root→leaf tail of the chain, bound leaf-first.
type parsedInputs struct {
	scopes []scopeInputs // one entry per resolved chain frame, root → leaf
}

// scopeInputs holds one chain frame's parsed values: flag values by logical name
// (a repeatable flag keeps every value) and the positional arguments.
type scopeInputs struct {
	flags map[string][]string
	args  []string
}

// usageError is a parse-time failure caused by bad input; handlers conventionally
// map it to exit code 2 (the usual CLI usage-error code).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// Parser is rotini's argument parser, and it is a *service*: a CLI binds it to the
// context registry under the key "parser" so that (1) parsing is opt-in — a CLI
// that wants raw argv binds nothing and reads rotini.Context.Args itself — and
// (2) the registry is a dependency-injection seam (the same handler code runs
// whether you bound the production parser or a double). A handler retrieves it and
// calls [Parser.Parse]:
//
//	// main.go
//	rth.Program.Bind("parser", rtk.NewParser()).Execute()
//
//	// a handler
//	parser := rotini.MustGet[*rtk.Parser](rtx, "parser")
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
// back to rotini.Context.Args.
func (p *Parser) Parse(rtx *rotini.Context, out any) error {
	store, chain, err := p.parseBind(rtx, out)
	if err != nil {
		return err
	}
	return validate(chain, store)
}

// parseBind parses argv into a store and binds it into out, but performs no
// required/enum/constraint validation — that is [validate]'s job. It is the shared
// front half of [Parser.Parse] (which then validates the argv-only store) and of the
// [Binder] (which first reconciles env/config fallbacks into the store, then
// validates last — so a required input is satisfiable from any source, not just
// argv). It returns the store and the resolved chain for that deferred validation.
func (p *Parser) parseBind(rtx *rotini.Context, out any) (*parsedInputs, []rotini.ResolvedCommand, error) {
	if p == nil {
		return nil, nil, &usageError{msg: "rotini: nil parser"}
	}
	if rtx == nil {
		return nil, nil, &usageError{msg: "rotini: parse on nil context"}
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, nil, &usageError{msg: "rotini: Parse out argument must be a non-nil pointer to an inputs struct"}
	}
	chain := rtx.Chain()
	if len(chain) == 0 {
		return nil, nil, &usageError{msg: "rotini: no command resolved for this context"}
	}
	store, err := parseInto(chain, rtx.Args())
	if err != nil {
		return nil, nil, err
	}
	bindInputs(rv.Elem(), store)
	return store, chain, nil
}

// parseInto binds argv to an already-resolved chain, strictly: an unrecognized
// flag, or a flag missing its value, is a usageError. Command tokens already in
// the chain are consumed; everything after the leaf command (and after "--") is a
// positional argument of the leaf. Declared defaults are applied. parseInto does
// not check required inputs or enums — that is [validate]'s job — so a handler can
// inspect what was supplied before deciding how strict to be.
func parseInto(chain []rotini.ResolvedCommand, argv []string) (*parsedInputs, error) {
	store := &parsedInputs{scopes: make([]scopeInputs, len(chain))}
	leaf := len(chain) - 1 // chain index of the leaf command
	depth := 1             // index of the next chain frame we might descend into
	startedArgs := false

	addFlag := func(idx int, name, value string) {
		if store.scopes[idx].flags == nil {
			store.scopes[idx].flags = map[string][]string{}
		}
		store.scopes[idx].flags[name] = append(store.scopes[idx].flags[name], value)
	}
	addArg := func(value string) {
		store.scopes[leaf].args = append(store.scopes[leaf].args, value)
	}

	for i := 0; i < len(argv); i++ {
		tok := argv[i]

		if tok == "--" { // explicit end of flags; the rest are positional
			startedArgs = true
			continue
		}

		if isFlag(tok) {
			name, inline, hasInline := splitFlag(tok)
			fdef, idx, ok := findFlag(chain, name)
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
				return nil, &usageError{msg: fmt.Sprintf("unknown flag %q", name)}
			}
			var value string
			switch {
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
					return nil, &usageError{msg: fmt.Sprintf("flag %q needs a value", name)}
				}
				value = argv[i]
			}
			addFlag(idx, fdef.Name, value)
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

	applyDefaults(chain, store)
	return store, nil
}

// validate enforces the declarative constraints on the resolved chain against a
// parsed store: a stray positional on a branch-only command is a mistyped
// sub-command; required flags/arguments must be present (or defaulted); and any
// value for a flag or argument that declares an Enum must be one of its members.
// It is the strict half of [Parse]; a handler that wants laxer behavior can bind
// inputs without it.
func validate(chain []rotini.ResolvedCommand, store *parsedInputs) error {
	leaf := chain[len(chain)-1]
	si := store.scopes[len(chain)-1]

	// A stray positional on a command that branches but takes no arguments is a
	// mistyped sub-command, not an argument.
	if len(leaf.Commands) > 0 && len(leaf.Arguments) == 0 && len(si.args) > 0 {
		return &usageError{msg: unknownCommandMsg(leaf, si.args[0])}
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
					return &usageError{msg: fmt.Sprintf("invalid value %q for %s (one of: %s)", redactValue(v, fd.Secret), flagLabel(fd), strings.Join(fd.Enum, ", "))}
				}
			}
			if err := checkConstraints(flagLabel(fd), fd.Type, fd.Constraints, vals, fd.Secret); err != nil {
				return err
			}
		}
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
				return &usageError{msg: fmt.Sprintf("invalid value %q for <%s> (one of: %s)", redactValue(v, ad.Secret), ad.Name, strings.Join(ad.Enum, ", "))}
			}
		}
		if err := checkConstraints("<"+ad.Name+">", ad.Type, ad.Constraints, vals, ad.Secret); err != nil {
			return err
		}
	}
	return nil
}

// checkConstraints enforces an input's declared numeric/string/array bounds against
// the value(s) supplied for it (one element for a scalar; possibly many for a
// repeatable flag or variadic argument). label is the human-facing identifier; typ is
// the resolved Go type. A zero bound (or empty pattern) is unset and skipped; numeric
// bounds apply to int/float types, length/pattern to strings, and item counts to
// arrays — a constraint declared on an incompatible type is silently ignored. When
// secret is true the offending value (and its length) is redacted in the error.
func checkConstraints(label, typ string, c rotini.Constraints, values []string, secret bool) error {
	if isArrayType(typ) {
		switch n := len(values); {
		case c.MinItems > 0 && n < c.MinItems:
			return &usageError{msg: fmt.Sprintf("%s needs at least %d %s (got %d)", label, c.MinItems, plural("value", c.MinItems), n)}
		case c.MaxItems > 0 && n > c.MaxItems:
			return &usageError{msg: fmt.Sprintf("%s accepts at most %d %s (got %d)", label, c.MaxItems, plural("value", c.MaxItems), n)}
		}
	}
	for _, v := range values {
		switch {
		case isNumericType(typ):
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				continue // not range-checkable; coerce already tolerates malformed input
			}
			if c.Minimum != 0 && n < c.Minimum {
				return &usageError{msg: fmt.Sprintf("%s must be >= %s (got %s)", label, formatNum(c.Minimum), redactValue(v, secret))}
			}
			if c.Maximum != 0 && n > c.Maximum {
				return &usageError{msg: fmt.Sprintf("%s must be <= %s (got %s)", label, formatNum(c.Maximum), redactValue(v, secret))}
			}
		case typ == "string":
			ln := utf8.RuneCountInString(v)
			gotLen := strconv.Itoa(ln)
			if secret {
				gotLen = "[redacted]"
			}
			if c.MinLength > 0 && ln < c.MinLength {
				return &usageError{msg: fmt.Sprintf("%s must be at least %d %s long (got %s)", label, c.MinLength, plural("character", c.MinLength), gotLen)}
			} else if c.MaxLength > 0 && ln > c.MaxLength {
				return &usageError{msg: fmt.Sprintf("%s must be at most %d %s long (got %s)", label, c.MaxLength, plural("character", c.MaxLength), gotLen)}
			}
			if c.Pattern != "" {
				if ok, err := regexp.MatchString(c.Pattern, v); err == nil && !ok {
					return &usageError{msg: fmt.Sprintf("%s must match %s (got %q)", label, c.Pattern, redactValue(v, secret))}
				}
			}
		}
	}
	return nil
}

// redactValue returns "[redacted]" for a secret input, else the value unchanged — so a
// secret flag/argument's value never appears in usage or validation errors.
func redactValue(v string, secret bool) string {
	if secret {
		return "[redacted]"
	}
	return v
}

func isNumericType(typ string) bool { return typ == "int" || typ == "float64" }

func isArrayType(typ string) bool { return strings.HasPrefix(typ, "[]") }

// formatNum renders a numeric bound without a trailing ".000…".
func formatNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// applyDefaults fills in declared flag and trailing-argument defaults for inputs
// the user did not provide, so handlers and required-checks see them.
func applyDefaults(chain []rotini.ResolvedCommand, store *parsedInputs) {
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
func requiredErrors(chain []rotini.ResolvedCommand, store *parsedInputs) error {
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
		return &usageError{msg: "missing required " + plural("input", len(missing)) + ": " + strings.Join(missing, ", ")}
	}
	return nil
}

func flagLabel(f rotini.FlagDef) string {
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

// isFlag reports whether tok is a flag token (e.g. "-h", "--watch", "--config=x").
// Bare "-" and "--" are not flags, and a token that parses as a number (e.g. "-5",
// "-0.5") is a negative-number argument rather than a flag. It mirrors the core
// runtime's resolver so binding agrees with the command the runtime dispatched.
func isFlag(tok string) bool {
	if len(tok) <= 1 || tok[0] != '-' || tok == "--" {
		return false
	}
	if _, err := strconv.ParseFloat(tok, 64); err == nil {
		return false
	}
	return true
}

// splitFlag splits a flag token into its identifier and an inline "=value".
func splitFlag(tok string) (name, value string, hasValue bool) {
	if eq := strings.IndexByte(tok, '='); eq >= 0 {
		return tok[:eq], tok[eq+1:], true
	}
	return tok, "", false
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
func parseCluster(chain []rotini.ResolvedCommand, body, inline string, hasInline bool, argv []string, i int, addFlag func(idx int, name, value string)) (int, error) {
	for k := 0; k < len(body); k++ {
		short := "-" + body[k:k+1]
		fdef, idx, ok := findFlag(chain, short)
		if !ok {
			return 0, &usageError{msg: fmt.Sprintf("unknown flag %q", short)}
		}
		if fdef.Type == "bool" {
			addFlag(idx, fdef.Name, "true")
			continue
		}
		// A value-taking flag ends the cluster: its value is whatever follows.
		switch rest := body[k+1:]; {
		case rest != "":
			addFlag(idx, fdef.Name, rest)
			return 0, nil
		case hasInline:
			addFlag(idx, fdef.Name, inline)
			return 0, nil
		default:
			if i+1 >= len(argv) {
				return 0, &usageError{msg: fmt.Sprintf("flag %q needs a value", short)}
			}
			addFlag(idx, fdef.Name, argv[i+1])
			return 1, nil
		}
	}
	// Every flag in the cluster was boolean; a trailing "=value" has nothing to bind.
	if hasInline {
		return 0, &usageError{msg: fmt.Sprintf("flag %q does not take a value", "-"+body)}
	}
	return 0, nil
}

// findFlag searches the resolved chain leaf→root for a flag whose identifiers
// include name, returning its definition and the chain index of the command that
// owns it.
func findFlag(chain []rotini.ResolvedCommand, name string) (rotini.FlagDef, int, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		for _, f := range chain[i].Flags {
			for _, id := range f.Identifiers {
				if id == name {
					return f, i, true
				}
			}
		}
	}
	return rotini.FlagDef{}, -1, false
}

// findChild returns the sub-command of f matching tok by name or alias.
func findChild(f rotini.ResolvedCommand, tok string) (rotini.CommandDef, bool) {
	for _, c := range f.Commands {
		if c.Name == tok {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == tok {
				return c, true
			}
		}
	}
	return rotini.CommandDef{}, false
}

// unknownCommandMsg builds the error for a mistyped sub-command, appending a
// "did you mean" suggestion when a close match exists.
func unknownCommandMsg(cur rotini.ResolvedCommand, tok string) string {
	msg := fmt.Sprintf("unknown command %q for %q", tok, cur.Name)
	if s := suggest(cur.Commands, tok); s != "" {
		msg += fmt.Sprintf("\n\nDid you mean %q?", s)
	}
	return msg
}

// suggest returns the closest command name to tok within edit distance 2.
func suggest(candidates []rotini.CommandDef, tok string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := levenshtein(tok, c.Name); d < bestDist {
			best, bestDist = c.Name, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// bindInputs fills a <Cmd>Inputs struct: one field per command on the resolved
// path, in root→leaf order. The fields bind to the tail of the chain aligned at
// the leaf, so each command's inputs come from the right frame no matter how deep
// it was reached — including a statically-composed subtree reached under extra
// parent frames, which simply go unbound.
func bindInputs(v reflect.Value, p *parsedInputs) {
	if v.Kind() != reflect.Struct {
		return
	}
	offset := len(p.scopes) - v.NumField()
	if offset < 0 {
		return // the struct names more commands than the chain has frames
	}
	for i := range v.NumField() {
		bindCommandInputs(v.Field(i), p.scopes[offset+i])
	}
}

// bindCommandInputs fills a <Cmd>CommandInputs struct's Flags and Arguments.
func bindCommandInputs(v reflect.Value, si scopeInputs) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range v.NumField() {
		switch t.Field(i).Name {
		case "Flags":
			bindFlags(v.Field(i), si.flags)
		case "Arguments":
			bindArgs(v.Field(i), si.args)
		}
	}
}

// bindFlags fills a <Cmd>Flags struct by matching each field's `rotini:"<name>"`
// tag against the parsed flag values.
func bindFlags(v reflect.Value, flags map[string][]string) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range v.NumField() {
		name := t.Field(i).Tag.Get("rotini")
		if name == "" {
			continue
		}
		if raw, ok := flags[name]; ok {
			coerce(v.Field(i), raw)
		}
	}
}

// bindArgs fills a <Cmd>Arguments struct positionally; a trailing []string field
// is variadic and absorbs all remaining positionals.
func bindArgs(v reflect.Value, args []string) {
	if v.Kind() != reflect.Struct {
		return
	}
	idx := 0
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String {
			coerce(f, args[min(idx, len(args)):])
			idx = len(args)
			continue
		}
		if idx < len(args) {
			coerce(f, args[idx:idx+1])
			idx++
		}
	}
}

var (
	durationType        = reflect.TypeOf(time.Duration(0))
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// coerce sets f from the raw string value(s). It is best-effort: malformed values
// (which validation should have rejected) leave the field at its zero value rather
// than panicking.
func coerce(f reflect.Value, raw []string) {
	if len(raw) == 0 {
		return
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		coerce(f.Elem(), raw)
		return
	}
	last := raw[len(raw)-1]

	if f.Type() == durationType {
		if d, err := time.ParseDuration(last); err == nil {
			f.SetInt(int64(d))
		}
		return
	}
	if f.CanAddr() && f.Addr().Type().Implements(textUnmarshalerType) {
		_ = f.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(last))
		return
	}

	switch f.Kind() {
	case reflect.Bool:
		if b, err := strconv.ParseBool(last); err == nil {
			f.SetBool(b)
		}
	case reflect.String:
		f.SetString(last)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(last, 10, 64); err == nil {
			f.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(last, 10, 64); err == nil {
			f.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if x, err := strconv.ParseFloat(last, 64); err == nil {
			f.SetFloat(x)
		}
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.String {
			f.Set(reflect.ValueOf(append([]string{}, raw...)))
		}
	}
}
