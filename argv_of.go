package rotini

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-rotini/recon"
)

// ArgvError is a value [ArgvOf] can't write as a command line: a secret, a config or stdin
// input, or a value no argv spelling reads back, such as an explicit empty list on a flag
// without a separator. Field names the input.
type ArgvError struct {
	Field  FieldPath
	Reason string
}

func (e *ArgvError) Error() string {
	return fmt.Sprintf("rotini: %s: %s", e.Field, e.Reason)
}

// ArgvOption configures [ArgvOf].
type ArgvOption func(*argvConfig)

type argvConfig struct {
	secrets bool
	path    []string
	hasPath bool
}

// ArgvSecrets lets [ArgvOf] write secret flags and arguments into argv. Without it they are an
// [*ArgvError], since a command line is visible to other users of the machine (ps). Secret env
// inputs are always written, into env.
func ArgvSecrets() ArgvOption {
	return func(c *argvConfig) { c.secrets = true }
}

// ArgvPath names the command the inputs value belongs to, by its path below the root (no names
// for the root itself). [ArgvOf] otherwise finds it by the inputs type the generated Definition
// records, so ArgvPath is for a hand-built Definition.
func ArgvPath(names ...string) ArgvOption {
	return func(c *argvConfig) { c.path, c.hasPath = names, true }
}

// ArgvOf turns a typed inputs value back into the command line that supplies it: argv (without
// the program name, as [Program.Run] takes it) and env, the KEY=value variables for its env
// inputs, ready for [Program.WithEnviron]. It is the inverse of parsing, for tests and for
// tools that drive a CLI:
//
//	in := cmd.TaskrAddInputs{}
//	in.TaskrAdd.Flags.Priority = 2
//	in.TaskrAdd.Arguments.Title = "write docs"
//	argv, env, err := rotini.ArgvOf(p.Definition(), in, rotini.PresenceOf(in))
//	// argv: add --priority=2 -- "write docs"
//
// Only the fields set names are written, each even when it holds its default. [PresenceOf]
// marks the non-zero fields; build the [Presence] by hand to write a zero value, such as
// --count=0 or --color=false over a true default.
//
// The command comes from the generated Definition, which records each command's inputs type, or
// from [ArgvPath]. Each command's flags follow its name, so a flag two commands both declare
// lands on the right one. Values are written attached (--name=value). A list or map repeats the
// flag, and a separator-split one quotes items that hold the separator. An object is JSON, a
// count repeats the flag, and false on a negatable flag is its negated form (--no-name, or the
// declared one). A "--" always comes before the invoked command's positionals, so a positional
// that looks like a flag or a command stays a positional; it shows in [Context.DashIndex]. A
// passthrough command's words follow its name as they are, except that with response files on,
// a word before any "--" that starts with the prefix has it doubled. On a flag or argument
// reading `@file` values, a value starting with @ is written with the @ doubled.
//
// A value no command line can supply is an [*ArgvError], never silently dropped:
//   - a secret flag or argument, unless [ArgvSecrets] is passed;
//   - a secret flag or argument that refuses a literal value (its from: leaves out value), even
//     with [ArgvSecrets]: pass it through a file or stdin;
//   - a config or stdin input (write the file, or supply stdin, yourself);
//   - an argument of a command other than the invoked one, or one after an unset argument;
//   - "-" on a flag or argument that reads stdin from it;
//   - an explicit empty list or map on a flag without a separator, or a nil pointer in set;
//   - text a free-form map value would read back as another type ("true", "3", "null");
//   - a time its layout can't show exactly.
//
// Any other error is the program's mistake: T isn't an inputs type of def, or doesn't match the
// command it names.
func ArgvOf[T any](def Definition, v T, set Presence, opts ...ArgvOption) (argv, env []string, err error) {
	var cfg argvConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	t := reflect.TypeFor[T]()
	rv := reflect.ValueOf(v)
	if t.Kind() != reflect.Struct || rv.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("rotini: ArgvOf: %s is not a generated inputs type", displayTypeName(t))
	}
	path := cfg.path
	if !cfg.hasPath {
		var ok bool
		if path, ok = inputsPath(def, t); !ok {
			return nil, nil, fmt.Errorf("rotini: ArgvOf: no command of %q records %s as its inputs type; "+
				"regenerate, or name the command with rotini.ArgvPath", def.Name, displayTypeName(t))
		}
	}
	chain, err := argvChain(def, path)
	if err != nil {
		return nil, nil, err
	}
	anchor := len(chain) - rv.NumField()
	if anchor < 0 {
		return nil, nil, fmt.Errorf("rotini: ArgvOf: %s describes %d commands but %q is only %d deep",
			displayTypeName(t), rv.NumField(), pathOf(chain), len(chain))
	}
	if err := checkChainAlignment(rv, chain, anchor); err != nil {
		return nil, nil, err
	}
	w := argvWriter{cfg: cfg, set: set, used: map[FieldPath]bool{}}
	if def.ResponseFiles != nil {
		w.responsePrefix = def.ResponseFiles.Prefix
	}
	for i, frame := range chain {
		if i > 0 {
			w.argv = append(w.argv, frame.Name)
		}
		w.reached = chain[:i+1]
		if i < anchor {
			continue // a composed parent's frame: T carries none of its inputs
		}
		top := t.Field(i - anchor).Name
		ci := rv.Field(i - anchor)
		if ci.Kind() != reflect.Struct {
			continue
		}
		if err := w.frame(top, ci, frame, i == len(chain)-1); err != nil {
			return nil, nil, err
		}
	}
	for _, p := range sortedPaths(set) {
		if !w.used[p] {
			return nil, nil, fmt.Errorf("rotini: ArgvOf: %s names no input of %s", p, displayTypeName(t))
		}
	}
	return w.argv, w.env, nil
}

// inputsPath finds the command whose recorded inputs type is t, as its path below the root.
func inputsPath(def Definition, t reflect.Type) ([]string, bool) {
	if def.Inputs == t {
		return []string{}, true
	}
	var find func(cmds []CommandDef, prefix []string) ([]string, bool)
	find = func(cmds []CommandDef, prefix []string) ([]string, bool) {
		for _, c := range cmds {
			p := append(slices.Clone(prefix), c.Name)
			if c.Inputs == t {
				return p, true
			}
			if got, ok := find(c.Commands, p); ok {
				return got, true
			}
		}
		return nil, false
	}
	return find(def.Commands, nil)
}

// argvChain builds the command chain a path names, as the resolver would for it.
func argvChain(def Definition, path []string) ([]Command, error) {
	chain := []Command{rootFrame(def)}
	for _, name := range path {
		cur := chain[len(chain)-1]
		if cur.Passthrough {
			return nil, fmt.Errorf("rotini: ArgvOf: %q is a passthrough command, so it has no sub-command %q", pathOf(chain), name)
		}
		c, ok := findChild(cur, name)
		if !ok {
			return nil, fmt.Errorf("rotini: ArgvOf: %q has no sub-command %q", pathOf(chain), name)
		}
		frame := cmdFrame(c)
		frame.Matched = name
		chain = append(chain, frame)
	}
	return chain, nil
}

// argvWriter accumulates ArgvOf's argv and env.
type argvWriter struct {
	cfg       argvConfig
	set       Presence
	used      map[FieldPath]bool
	reached   []Command // the commands named so far, the one being written last
	argv, env []string

	responsePrefix string // the response-file prefix; "" when response files are off
}

// given reports whether set names p, and marks p as consumed.
func (w *argvWriter) given(p FieldPath) bool {
	if _, ok := w.set[p]; !ok {
		return false
	}
	w.used[p] = true
	return true
}

// frame writes one command's set inputs: its flags (after its name), its env inputs and, on the
// invoked command, its positionals.
func (w *argvWriter) frame(top string, ci reflect.Value, frame Command, leaf bool) error {
	var err error
	eachTaggedField(ci, "Flags", func(fieldName, logical string, _ reflect.StructTag, f reflect.Value) {
		p := fieldPath(top, "Flags", fieldName)
		if err != nil || !w.given(p) {
			return
		}
		fd, ok := findFlagDef(frame.Flags, logical)
		if !ok {
			err = fmt.Errorf("rotini: ArgvOf: %s: %q declares no flag %q", p, frame.Name, logical)
			return
		}
		err = w.flag(p, fd, f)
	})
	if err != nil {
		return err
	}
	eachTaggedField(ci, "Env", func(fieldName, _ string, tag reflect.StructTag, f reflect.Value) {
		p := fieldPath(top, "Env", fieldName)
		if err != nil || !w.given(p) {
			return
		}
		err = w.envInput(p, tag, f)
	})
	if err != nil {
		return err
	}
	eachTaggedField(ci, "Config", func(fieldName, _ string, _ reflect.StructTag, _ reflect.Value) {
		p := fieldPath(top, "Config", fieldName)
		if err == nil && w.given(p) {
			err = &ArgvError{Field: p, Reason: "a config input comes only from a configuration file; write one"}
		}
	})
	if err != nil {
		return err
	}
	if p := fieldPath(top, "Stdin"); w.given(p) {
		return &ArgvError{Field: p, Reason: "the stdin payload isn't part of the command line; supply it on the program's stdin"}
	}
	return w.arguments(top, ci, frame, leaf)
}

// identifier picks the spelling ArgvOf writes a flag with: its first long identifier that isn't
// deprecated, else its first short one that isn't, else its first.
func argvIdentifier(fd FlagDef) string {
	for _, long := range []bool{true, false} {
		for _, id := range fd.Identifiers {
			if strings.HasPrefix(id, "--") == long && !slices.Contains(fd.DeprecatedIdentifiers, id) {
				return id
			}
		}
	}
	if len(fd.Identifiers) > 0 {
		return fd.Identifiers[0]
	}
	return "--" + fd.Name
}

// flag writes one set flag.
func (w *argvWriter) flag(p FieldPath, fd FlagDef, f reflect.Value) error {
	if literalFree(fd.Secret, fd.From) {
		return &ArgvError{Field: p, Reason: secretTakesNoLiteral}
	}
	if fd.Secret && !w.cfg.secrets {
		return &ArgvError{Field: p, Reason: "a secret is visible in the process list as argv; pass rotini.ArgvSecrets() to write it anyway"}
	}
	id := argvIdentifier(fd)
	if f.Kind() == reflect.Pointer && valueParsers[f.Type()] == nil {
		if f.IsNil() {
			return &ArgvError{Field: p, Reason: "a nil value can't be written; leave the field out of set"}
		}
		f = f.Elem()
	}
	switch {
	case fd.Type == "count":
		return w.countFlag(p, fd, id, f.Int())
	case fd.DottedKeys:
		pairs, err := dottedPairs(f)
		if err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
		return w.occurrences(p, fd, id, pairs)
	case isObjectFlag(fd):
		return w.objectFlag(p, fd, id, f)
	case f.Kind() == reflect.Bool:
		w.boolFlag(fd, id, f.Bool())
		return nil
	case f.Kind() == reflect.Map:
		pairs, err := mapPairs(f, fd.Layout)
		if err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
		return w.occurrences(p, fd, id, pairs)
	case f.Kind() == reflect.Slice && !isScalarSlice(f.Type()):
		items := make([]string, f.Len())
		for i := range items {
			s, err := argvText(f.Index(i), fd.Layout)
			if err != nil {
				return &ArgvError{Field: p, Reason: err.Error()}
			}
			items[i] = s
		}
		return w.occurrences(p, fd, id, items)
	}
	s, err := argvText(f, fd.Layout)
	if err != nil {
		return &ArgvError{Field: p, Reason: err.Error()}
	}
	return w.occurrence(p, fd, id, s)
}

// countFlag writes a count flag as n bare occurrences. 0 writes nothing, which reads back as 0
// only when the flag has no default.
func (w *argvWriter) countFlag(p FieldPath, fd FlagDef, id string, n int64) error {
	switch {
	case n < 0:
		return &ArgvError{Field: p, Reason: "a count can't be negative"}
	case n == 0 && len(flagDefaults(fd)) > 0:
		return &ArgvError{Field: p, Reason: "an explicit count of 0 can't be written over a default"}
	}
	for range n {
		w.argv = append(w.argv, id)
	}
	return nil
}

// boolFlag writes true as the bare flag, and false as its negated form, unless another flag
// declares that spelling as its own, else as --name=false.
func (w *argvWriter) boolFlag(fd FlagDef, id string, v bool) {
	if v {
		w.argv = append(w.argv, id)
		return
	}
	for _, neg := range negatedIdentifiers(fd) {
		if _, idx, negated, ok := findFlagMatch(w.reached, neg); ok && negated && idx == len(w.reached)-1 {
			w.argv = append(w.argv, neg)
			return
		}
	}
	w.argv = append(w.argv, id+"=false")
}

// occurrences writes a list or map flag: one occurrence per item, each quoted on its own when
// the flag splits on a separator. An empty one is "--name=" with a separator, and has no
// spelling without one.
func (w *argvWriter) occurrences(p FieldPath, fd FlagDef, id string, items []string) error {
	if len(items) == 0 {
		if fd.Separator == "" {
			return &ArgvError{Field: p, Reason: "an explicit empty list can't be written on a flag without a separator"}
		}
		w.argv = append(w.argv, id+"=")
		return nil
	}
	for _, item := range items {
		if fd.Separator != "" {
			q, err := csvItem(item, fd.Separator)
			if err != nil {
				return &ArgvError{Field: p, Reason: err.Error()}
			}
			item = q
		}
		if err := w.occurrence(p, fd, id, item); err != nil {
			return err
		}
	}
	return nil
}

// occurrence writes one attached occurrence of a flag, escaping a value the flag's `from:`
// modes would read as a file or as stdin.
func (w *argvWriter) occurrence(p FieldPath, fd FlagDef, id, value string) error {
	if strings.HasPrefix(value, "@") && slices.Contains(fd.From, "file") {
		value = "@" + value
	}
	if value == "-" && slices.Contains(fd.From, "stdin") {
		return &ArgvError{Field: p, Reason: `"-" on this flag reads stdin, and has no other spelling`}
	}
	w.argv = append(w.argv, id+"="+value)
	return nil
}

// objectFlag writes an object flag as JSON, one occurrence per element of a list of objects.
func (w *argvWriter) objectFlag(p FieldPath, fd FlagDef, id string, f reflect.Value) error {
	values := []reflect.Value{f}
	if f.Kind() == reflect.Slice {
		values = values[:0]
		for i := range f.Len() {
			values = append(values, f.Index(i))
		}
	}
	items := make([]string, 0, len(values))
	for _, v := range values {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return &ArgvError{Field: p, Reason: "a nil object can't be written"}
		}
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return &ArgvError{Field: p, Reason: "can't write the object as JSON: " + err.Error()}
		}
		// JSON replaces invalid UTF-8, so check the text reads back to the same value.
		back := reflect.New(v.Type())
		if err := json.Unmarshal(b, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), v.Interface()) {
			return &ArgvError{Field: p, Reason: "the object doesn't read back from JSON as it is (invalid UTF-8?)"}
		}
		items = append(items, string(b))
	}
	return w.occurrences(p, fd, id, items)
}

// arguments writes the invoked command's set positionals after "--" (a passthrough command's
// raw, with no "--"), and rejects positionals set on any other command.
func (w *argvWriter) arguments(top string, ci reflect.Value, frame Command, leaf bool) error {
	s := ci.FieldByName("Arguments")
	if !s.IsValid() || s.Kind() != reflect.Struct {
		return nil
	}
	st := s.Type()
	defs := fieldArgDefs(s, frame.Arguments)
	var words []string
	counts := make([]int, s.NumField())
	given := make([]bool, s.NumField())
	for j := range s.NumField() {
		p := fieldPath(top, "Arguments", st.Field(j).Name)
		if !w.given(p) {
			continue
		}
		given[j] = true
		if !leaf {
			return &ArgvError{Field: p, Reason: fmt.Sprintf("only the invoked command takes arguments; %q is an ancestor", frame.Name)}
		}
		if literalFree(defs[j].Secret, defs[j].From) {
			return &ArgvError{Field: p, Reason: secretTakesNoLiteral}
		}
		if defs[j].Secret && !w.cfg.secrets {
			return &ArgvError{Field: p, Reason: "a secret is visible in the process list as argv; pass rotini.ArgvSecrets() to write it anyway"}
		}
		vals, err := argWords(s.Field(j), defs, j, frame)
		if err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
		if len(vals) == 0 {
			return &ArgvError{Field: p, Reason: "an explicit empty list of arguments can't be written"}
		}
		counts[j] = len(vals)
		words = append(words, vals...)
	}
	if len(words) == 0 {
		return nil
	}
	// Words bind in order around a variadic; each set argument must get exactly its own.
	spans := argSpans(defs, len(words))
	for j, sp := range spans {
		if sp[1]-sp[0] != counts[j] {
			p := fieldPath(top, "Arguments", st.Field(j).Name)
			for k := j; k < len(given); k++ {
				if given[k] {
					p = fieldPath(top, "Arguments", st.Field(k).Name)
					break
				}
			}
			return &ArgvError{Field: p, Reason: "an argument can't be written while one before it is unset"}
		}
	}
	if !frame.Passthrough {
		w.argv = append(w.argv, "--")
	} else {
		words = escapeResponseWords(words, w.responsePrefix)
	}
	w.argv = append(w.argv, words...)
	return nil
}

// escapeResponseWords doubles the response-file prefix on the raw words before the first "--",
// which a run would otherwise expand as response files.
func escapeResponseWords(words []string, prefix string) []string {
	if prefix == "" {
		return words
	}
	out := slices.Clone(words)
	for i, w := range out {
		if w == "--" {
			break
		}
		if len(w) > len(prefix) && strings.HasPrefix(w, prefix) {
			out[i] = prefix + w
		}
	}
	return out
}

// argWords spells one argument's value as words. A variadic with a separator quotes each item
// on its own; a passthrough argument's words are written as they are.
func argWords(f reflect.Value, defs []ArgDef, j int, frame Command) ([]string, error) {
	def := defs[j]
	if f.Kind() == reflect.Pointer && valueParsers[f.Type()] == nil {
		if f.IsNil() {
			return nil, errors.New("a nil value can't be written; leave the field out of set")
		}
		f = f.Elem()
	}
	if f.Kind() != reflect.Slice || isScalarSlice(f.Type()) {
		s, err := argvText(f, def.Layout)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(s, "@") && slices.Contains(def.From, "file") {
			s = "@" + s
		}
		if s == "-" && slices.Contains(def.From, "stdin") {
			return nil, errors.New(`"-" on this argument reads stdin, and has no other spelling`)
		}
		return []string{s}, nil
	}
	raw := def.Passthrough || frame.Passthrough
	split := def.Separator != "" && !raw && !hasArgTail(defs)
	out := make([]string, f.Len())
	for i := range out {
		s, err := argvText(f.Index(i), def.Layout)
		if err != nil {
			return nil, err
		}
		if split {
			if s, err = csvItem(s, def.Separator); err != nil {
				return nil, err
			}
		}
		out[i] = s
	}
	return out, nil
}

// envInput writes one set env input: KEY=value under its first variable name, or one
// variable per leaf of a nested env input.
func (w *argvWriter) envInput(p FieldPath, tag reflect.StructTag, f reflect.Value) error {
	if nest := tag.Get("envnest"); nest != "" {
		return w.envNested(p, nest, f)
	}
	names := tag.Get("env")
	if names == "" {
		return &ArgvError{Field: p, Reason: "the input's struct tag names no environment variable"}
	}
	name, _, _ := strings.Cut(names, ",")
	if f.Kind() == reflect.Pointer && valueParsers[f.Type()] == nil {
		if f.IsNil() {
			return &ArgvError{Field: p, Reason: "a nil value can't be written; leave the field out of set"}
		}
		f = f.Elem()
	}
	layout := tag.Get("layout")
	sep := recon.ParseTag(tag.Get("recon")).Separator
	if sep == "" {
		sep = ","
	}
	var text string
	switch {
	case f.Kind() == reflect.Map:
		pairs, err := mapPairs(f, layout)
		if err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
		if text, err = envList(pairs, sep); err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
	case f.Kind() == reflect.Slice && !isScalarSlice(f.Type()):
		items := make([]string, f.Len())
		for i := range items {
			s, err := argvText(f.Index(i), layout)
			if err != nil {
				return &ArgvError{Field: p, Reason: err.Error()}
			}
			items[i] = s
		}
		var err error
		if text, err = envList(items, sep); err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
	default:
		var err error
		if text, err = argvText(f, layout); err != nil {
			return &ArgvError{Field: p, Reason: err.Error()}
		}
	}
	w.env = append(w.env, name+"="+text)
	return nil
}

// envList joins list items with the env input's separator, as its list is read.
func envList(items []string, sep string) (string, error) {
	for _, it := range items {
		if strings.Contains(it, sep) {
			return "", fmt.Errorf("the item %q holds the separator %q, which an environment variable's list can't", it, sep)
		}
		if strings.TrimSpace(it) != it {
			return "", fmt.Errorf("the item %q has surrounding space, which an environment variable's list drops", it)
		}
	}
	return strings.Join(items, sep), nil
}

// envNested writes a nested env input (envnest:"BASE,SEP") as one variable per string leaf.
func (w *argvWriter) envNested(p FieldPath, nest string, f reflect.Value) error {
	base, rest, _ := strings.Cut(nest, ",")
	sep, _, _ := strings.Cut(rest, ",")
	m, ok := reflect.TypeAssert[map[string]any](f)
	if !ok || sep == "" {
		return &ArgvError{Field: p, Reason: "a nested env input must be a map[string]any with a separator"}
	}
	var out []string
	var walk func(prefix string, m map[string]any) error
	walk = func(prefix string, m map[string]any) error {
		for k, v := range m {
			if k == "" || k != strings.ToLower(k) || strings.Contains(k, strings.ToLower(sep)) || strings.Contains(k, ".") {
				return fmt.Errorf("the key %q can't be written: keys must be lowercase and hold neither %q nor \".\"", k, sep)
			}
			name := prefix + sep + strings.ToUpper(k)
			switch v := v.(type) {
			case map[string]any:
				if err := walk(name, v); err != nil {
					return err
				}
			case string:
				out = append(out, name+"="+v)
			default:
				return fmt.Errorf("the value of %q is a %T; a nested env input holds only strings", k, v)
			}
		}
		return nil
	}
	if err := walk(base, m); err != nil {
		return &ArgvError{Field: p, Reason: err.Error()}
	}
	slices.Sort(out)
	w.env = append(w.env, out...)
	return nil
}

// isScalarSlice reports whether a slice type is one value, not a list: bytes and the
// byte-slice value types.
func isScalarSlice(t reflect.Type) bool {
	if valueParsers[t] != nil {
		return true
	}
	if t.Elem().Kind() == reflect.Uint8 {
		return true
	}
	return reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// argvText spells one scalar value as the parser reads it back.
func argvText(f reflect.Value, layout string) (string, error) {
	if f.Kind() == reflect.Pointer && valueParsers[f.Type()] == nil {
		if f.IsNil() {
			return "", errors.New("a nil value can't be written")
		}
		f = f.Elem()
	}
	switch v := f.Interface().(type) {
	case *url.URL, *time.Location, *regexp.Regexp:
		if f.IsNil() {
			return "", fmt.Errorf("a nil %s can't be written", valueTypeLabels[f.Type()])
		}
		return parsedText(f, fmt.Sprint(v))
	case mail.Address:
		return parsedText(f, v.String())
	case net.HardwareAddr:
		return parsedText(f, v.String())
	case time.Time:
		return timeText(v, layout)
	case time.Duration:
		return formatDuration(v), nil
	}
	if s, ok, err := marshaledText(f); ok {
		return s, err
	}
	switch f.Kind() {
	case reflect.String:
		return f.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(f.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(f.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(f.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(f.Float(), 'g', -1, f.Type().Bits()), nil
	case reflect.Interface:
		if s, ok := reflect.TypeAssert[string](f); ok {
			return s, nil
		}
		return "", fmt.Errorf("a %T in an untyped input can't be written; it reads text only", f.Interface())
	}
	return "", fmt.Errorf("a %s can't be written as text", f.Type())
}

// parsedText checks that s, the spelling of a value type rotini parses itself, reads back to
// the value f holds.
func parsedText(f reflect.Value, s string) (string, error) {
	back, err := valueParsers[f.Type()](s)
	if err != nil || fmt.Sprint(back.Interface()) != fmt.Sprint(f.Interface()) {
		return "", fmt.Errorf("the %s %q doesn't read back as it is", valueTypeLabels[f.Type()], s)
	}
	return s, nil
}

// marshaledText spells a value whose type parses itself with UnmarshalText: by its MarshalText,
// else by its String method when that parses back to the same value. ok is false for a type
// that doesn't parse itself.
func marshaledText(f reflect.Value) (string, bool, error) {
	pt := reflect.PointerTo(f.Type())
	if !pt.Implements(textUnmarshalerType) {
		return "", false, nil
	}
	addr := reflect.New(f.Type())
	addr.Elem().Set(f)
	if m, ok := reflect.TypeAssert[encoding.TextMarshaler](addr); ok {
		b, err := m.MarshalText()
		if err != nil {
			return "", true, fmt.Errorf("can't write the %s: %w", f.Type(), err)
		}
		return string(b), true, nil
	}
	if sr, ok := reflect.TypeAssert[fmt.Stringer](addr); ok {
		s := sr.String()
		back := reflect.New(f.Type())
		if u, ok := reflect.TypeAssert[encoding.TextUnmarshaler](back); ok && u.UnmarshalText([]byte(s)) == nil &&
			reflect.DeepEqual(back.Elem().Interface(), f.Interface()) {
			return s, true, nil
		}
	}
	return "", true, fmt.Errorf("a %s has no MarshalText method (and its String doesn't read back), so it can't be written", f.Type())
}

// timeText spells a time under an input's layout and checks that the layout reads it back to
// the same instant.
func timeText(t time.Time, layout string) (string, error) {
	var s string
	switch layout {
	case "":
		s = t.Format(time.RFC3339Nano)
		back, err := time.Parse(time.RFC3339, s)
		if err != nil || !back.Equal(t) {
			return "", fmt.Errorf("the time %s can't be written as RFC 3339", t)
		}
		return s, nil
	case layoutUnix:
		s = unixText(t)
	case layoutUnixMilli:
		s = strconv.FormatInt(t.UnixMilli(), 10)
	default:
		s = t.Format(layout)
	}
	back, err := parseTimeLayout(s, layout)
	if err != nil || !back.Equal(t) {
		return "", fmt.Errorf("the layout %q can't show the time %s exactly", layout, t)
	}
	return s, nil
}

// unixText writes a time as Unix seconds with the fraction the layout `unix` reads.
func unixText(t time.Time) string {
	sec, ns := t.Unix(), int64(t.Nanosecond())
	if ns == 0 {
		return strconv.FormatInt(sec, 10)
	}
	sign := ""
	if sec < 0 {
		sign, sec, ns = "-", -(sec + 1), 1e9-ns
	}
	frac := strings.TrimRight(fmt.Sprintf("%09d", ns), "0")
	return sign + strconv.FormatInt(sec, 10) + "." + frac
}

// csvItem quotes one item so splitting on sep reads it back as it is.
func csvItem(item, sep string) (string, error) {
	if strings.Contains(item, "\r") {
		return "", fmt.Errorf("the item %q holds a carriage return, which a separated list can't", item)
	}
	if item == "" || strings.Contains(item, sep) || strings.ContainsAny(item, "\"\n") ||
		strings.TrimLeftFunc(item, unicode.IsSpace) != item {
		return `"` + strings.ReplaceAll(item, `"`, `""`) + `"`, nil
	}
	return item, nil
}

// mapPairs spells a map as key=value pairs, sorted by key. A free-form value is written as its
// JSON spelling, which the parser types back.
func mapPairs(f reflect.Value, layout string) ([]string, error) {
	if f.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("a %s can't be written; map keys must be strings", f.Type())
	}
	keys := make([]string, 0, f.Len())
	for _, k := range f.MapKeys() {
		keys = append(keys, k.String())
	}
	slices.Sort(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" || strings.Contains(k, "=") {
			return nil, fmt.Errorf("the key %q can't be written; keys must be non-empty and hold no \"=\"", k)
		}
		v := f.MapIndex(reflect.ValueOf(k).Convert(f.Type().Key()))
		var s string
		var err error
		if f.Type().Elem().Kind() == reflect.Interface {
			s, err = anyText(k, v.Interface())
		} else {
			s, err = argvText(v, layout)
		}
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, k+"="+s)
	}
	return pairs, nil
}

// anyText spells a free-form map value so [inferScalar] reads back the same value.
func anyText(key string, v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case string:
		if back, ok := inferScalar(v).(string); !ok || back != v {
			return "", fmt.Errorf("the value %q of %q would read back as a %T, not text", v, key, inferScalar(v))
		}
		return v, nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		x := rv.Float()
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("the value of %q is %v, which has no JSON spelling", key, x)
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	}
	return "", fmt.Errorf("the value of %q is a %T; only text, numbers, booleans and null can be written", key, v)
}

// dottedPairs flattens a dotted-keys map into sorted a.b=value pairs.
func dottedPairs(f reflect.Value) ([]string, error) {
	m, ok := reflect.TypeAssert[map[string]any](f)
	if !ok {
		return nil, fmt.Errorf("a %s can't be written as dotted keys", f.Type())
	}
	var pairs []string
	var walk func(prefix string, m map[string]any) error
	walk = func(prefix string, m map[string]any) error {
		for k, v := range m {
			if k == "" || strings.ContainsAny(k, ".=") {
				return fmt.Errorf("the key %q can't be written; dotted keys hold no \".\" or \"=\" and aren't empty", k)
			}
			if sub, ok := v.(map[string]any); ok {
				if len(sub) == 0 {
					return fmt.Errorf("the empty map under %q can't be written", prefix+k)
				}
				if err := walk(prefix+k+".", sub); err != nil {
					return err
				}
				continue
			}
			s, err := anyText(prefix+k, v)
			if err != nil {
				return err
			}
			pairs = append(pairs, prefix+k+"="+s)
		}
		return nil
	}
	if err := walk("", m); err != nil {
		return nil, err
	}
	slices.Sort(pairs)
	return pairs, nil
}
