package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// Structured output: a command's spec may declare the shape of what it writes (`output:`); the
// handler decides how and in which format to write it. [Context.WriteOutput] is an optional
// helper: it writes json, yaml or toml itself, hands any other format to the handler's
// renderer, and checks the value is the declared type:
//
//	in, err := rtx.Inputs[TaskrListInputs]()
//	…
//	return rtx.WriteOutput(list, in.Taskr.Flags.Output, renderTaskTable)
//
//	func renderTaskTable(w io.Writer, format string, v TaskrListOutput) error { … }
//
// The format is whatever the handler passes, typically the value of a flag the author declared.
// Output goes to rtx.Stdout.

// OutputDef is what a command declares it writes to stdout when it succeeds: the spec's
// `output:`, recorded by `rotini generate`. A nil OutputDef means the command declares none.
type OutputDef struct {
	// Type is the generated <Prefix>Output type: the whole output, or one item when the command
	// writes a stream of them.
	Type reflect.Type
	// Schema is the shape as a self-contained JSON Schema, which [Program.WithOutputChecks],
	// [Context.CheckOutput] and [DecodeOutput] check values against. "" when there is none.
	Schema string
	// Stream is the spec's `output_stream`: the command writes a stream of Type items with
	// [Context.WriteOutputItem], rather than one value with [Context.WriteOutput].
	Stream bool
	// Tag is set for a stream whose items come in several kinds (the spec's `oneOf`): the
	// JSON name of the property that says which kind an item is, such as "kind". Type is then
	// the interface every kind's type satisfies, and Schema allows any kind.
	Tag string
	// Kinds lists each kind of item, in declared order, when Tag is set.
	Kinds []OutputKind
}

// OutputKind is one kind of item in a stream whose items come in several kinds: the value
// its tag property holds, its generated Go type and its shape.
type OutputKind struct {
	// Value is what the item's tag property holds for this kind, such as "progress".
	Value string
	// Type is the kind's generated struct type.
	Type reflect.Type
	// Schema is the kind's shape as a self-contained JSON Schema, which output checks and
	// [DecodeOutput] check an item of this kind against. "" when there is none.
	Schema string
}

// kindOfType returns the kind whose type is t, or a pointer to it; nil when there is none.
func (o *OutputDef) kindOfType(t reflect.Type) *OutputKind {
	if t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := range o.Kinds {
		if o.Kinds[i].Type == t {
			return &o.Kinds[i]
		}
	}
	return nil
}

// kindOfValue returns the kind whose tag value is v; nil when there is none.
func (o *OutputDef) kindOfValue(v string) *OutputKind {
	for i := range o.Kinds {
		if o.Kinds[i].Value == v {
			return &o.Kinds[i]
		}
	}
	return nil
}

// kindValues lists the kinds' tag values, quoted, for a message.
func (o *OutputDef) kindValues() string {
	vs := make([]string, len(o.Kinds))
	for i, k := range o.Kinds {
		vs[i] = strconv.Quote(k.Value)
	}
	return strings.Join(vs, ", ")
}

// tagItem checks that v is one of out's kinds and returns its kind and v with the tag
// property filled in when it was empty. A tag naming another kind is an internal error.
func tagItem(name string, out *OutputDef, v any) (*OutputKind, any, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return nil, nil, InternalError(fmt.Errorf("%s: the item written is nil; write one of its kinds (%s)", name, out.kindValues()))
	}
	kind := out.kindOfType(rv.Type())
	if kind == nil {
		return nil, nil, InternalError(fmt.Errorf("%s: the item written is a %s, which is not one of the kinds the command declares (%s)", name, rv.Type(), out.kindValues()))
	}
	st := reflect.Indirect(rv)
	field := tagFieldIndex(st.Type(), out.Tag)
	if field < 0 || st.Field(field).Kind() != reflect.String {
		return nil, nil, InternalError(fmt.Errorf("%s: %s has no string field for its tag %q", name, st.Type(), out.Tag))
	}
	switch tag := st.Field(field).String(); tag {
	case kind.Value:
		return kind, v, nil
	case "":
	default:
		return nil, nil, InternalError(fmt.Errorf("%s: the item is a %s, the %q kind, but its %s is %q", name, st.Type(), kind.Value, out.Tag, tag))
	}
	filled := reflect.New(st.Type())
	filled.Elem().Set(st)
	filled.Elem().Field(field).SetString(kind.Value)
	if rv.Kind() == reflect.Pointer {
		return kind, filled.Interface(), nil
	}
	return kind, filled.Elem().Interface(), nil
}

// tagSelection is tagItem for a Selection of one of out's kinds: a selected tag that is empty is
// filled in, and one naming another kind is an internal error. A tag left out of the selection
// stays out.
func tagSelection(name, tag string, kind *OutputKind, sel Selection) (Selection, error) {
	m, ok := sel.data.(map[string]any)
	if !ok {
		return sel, nil
	}
	switch value, has := m[tag]; {
	case !has || value == kind.Value:
		return sel, nil
	case value != "":
		text, _ := json.Marshal(value) //nolint:errcheck,errchkjson // a JSON-shaped value always encodes
		return sel, InternalError(fmt.Errorf("%s: the item is a %s, the %q kind, but its %s is %s", name, sel.from, kind.Value, tag, text))
	}
	filled := maps.Clone(m)
	filled[tag] = kind.Value
	return Selection{from: sel.from, data: filled}, nil
}

// tagFieldIndex returns the index of t's field whose JSON name is tag, or -1.
func tagFieldIndex(t reflect.Type, tag string) int {
	if t.Kind() != reflect.Struct {
		return -1
	}
	for i := range t.NumField() {
		f := t.Field(i)
		jsonName, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if jsonName == tag || (jsonName == "" && f.Name == tag) {
			return i
		}
	}
	return -1
}

// machineFormats are the formats rotini serializes itself. Every other format is a human format,
// laid out by the renderer the handler passes.
var machineFormats = []string{"json", "yaml", "toml"}

// WriteOutput writes v, the invoked command's output, to rtx.Stdout in format: indented json,
// yaml or toml by rotini, any other format by render, which may be nil when the handler only
// ever passes those three. An empty format means json.
//
// It returns an internal error, and writes nothing, when the command declares a stream
// (output_stream), when v is not the type the command declares as its output, when no renderer
// is passed for a format rotini does not write, or, with [Program.WithOutputChecks], when v does
// not match the declared shape. A renderer's error is returned unwrapped. A command that declares
// no output may still use it; nothing is checked.
//
// v may be a [Selection] made from a value of the declared type by [SelectFields]: it is checked
// as that type, and against the declared shape with every `required` removed.
func (rtx *Context) WriteOutput[T any](v T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, rtx.Stdout, v, format, render, false)
}

// WriteOutputItem writes one item of a stream to rtx.Stdout, for a command that writes its output
// item by item as each is ready: compact json, one value per line; a yaml document starting
// "---"; any other format by render. toml cannot be streamed. Its checks are WriteOutput's, with
// the item checked against the declared shape, which for a command that declares output_stream
// is one item. On a command that doesn't, the item is checked as WriteOutput checks a value.
//
// When the items come in several kinds (the spec's `oneOf`; see [OutputDef.Tag]), item is a value
// of any kind's type. Its tag field is filled in from that type when it is empty; a tag naming
// another kind, or a type that is none of the kinds, is an internal error, and the item is
// checked against its own kind's shape. A [Selection] made from a kind's value is treated the
// same way when it selects the tag field.
func (rtx *Context) WriteOutputItem[T any](item T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, rtx.Stdout, item, format, render, true)
}

// WriteOutputTo is [Context.WriteOutput] to w instead of rtx.Stdout: an output file from
// [CreateOutput], a buffer, or several writers at once. Its checks and errors are WriteOutput's.
// To write the output to a file and to stdout (a tee):
//
//	out, err := rotini.CreateOutput(rtx, in.Flags.Output)
//	if err != nil {
//		return err
//	}
//	defer out.Abort()
//	if err := rtx.WriteOutputTo(io.MultiWriter(rtx.Stdout, out), report, format, render); err != nil {
//		return err
//	}
//	return out.Close()
//
// io.MultiWriter stops at the first writer that fails, so a closed stdout also ends the file.
func (rtx *Context) WriteOutputTo[T any](w io.Writer, v T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, w, v, format, render, false)
}

// WriteOutputItemTo is [Context.WriteOutputItem] to w instead of rtx.Stdout.
func (rtx *Context) WriteOutputItemTo[T any](w io.Writer, item T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, w, item, format, render, true)
}

// CheckOutput checks v against the invoked command's declared output schema, returning an
// internal error that names each field that does not match:
//
//	taskr list: output does not match its contract: output.tasks[2].status: value is not in enum
//
// It writes nothing, and is an internal error on a command that declares no output schema.
// [Program.WithOutputChecks] makes every WriteOutput call check. A [Selection] is checked as the
// type it was selected from, against the schema with every `required` removed. For a stream
// whose items come in several kinds, v is checked as one item, against its kind.
func (rtx *Context) CheckOutput(v any) error {
	cmd := rtx.invokedCommand()
	name := rtx.commandName()
	if cmd.Output == nil || cmd.Output.Schema == "" {
		return InternalError(fmt.Errorf("%s: declares no output to check against", name))
	}
	t := reflect.TypeOf(v)
	sel, selected := v.(Selection)
	if selected {
		if sel.from == nil {
			return InternalError(fmt.Errorf("%s: the output is a zero Selection; make one with SelectFields", name))
		}
		t = sel.from
	}
	schema := cmd.Output.Schema
	switch {
	case cmd.Output.Tag != "" && selected:
		kind := cmd.Output.kindOfType(t)
		if kind == nil {
			return InternalError(fmt.Errorf("%s: the output is a %s, which is not one of the kinds the command declares (%s)", name, t, cmd.Output.kindValues()))
		}
		tagged, err := tagSelection(name, cmd.Output.Tag, kind, sel)
		if err != nil {
			return err
		}
		v, schema = tagged, kind.Schema
	case cmd.Output.Tag != "":
		kind, tagged, err := tagItem(name, cmd.Output, v)
		if err != nil {
			return err
		}
		v, schema = tagged, kind.Schema
	default:
		if err := checkOutputType(name, cmd.Output, t); err != nil {
			return err
		}
	}
	if schema == "" {
		return nil
	}
	return checkOutputSchema(name, schema, v, selected)
}

// writeOutput is WriteOutput and WriteOutputItem, and their To forms, writing to w. It is
// instantiated once per output type, so all it does is hand v to writeOutputValue, which is not.
func writeOutput[T any](rtx *Context, w io.Writer, v T, format string, render func(io.Writer, string, T) error, item bool) error {
	var r func(io.Writer, string, any) error
	if render != nil {
		r = func(w io.Writer, format string, v any) error {
			tv, _ := v.(T) // always v's own type; a nil interface value stays the zero T
			return render(w, format, tv)
		}
	}
	return writeOutputValue(rtx, w, v, reflect.TypeFor[T](), format, r, item)
}

// writeOutputValue is writeOutput for v of static type t.
func writeOutputValue(rtx *Context, w io.Writer, v any, t reflect.Type, format string, render func(io.Writer, string, any) error, item bool) error {
	cmd := rtx.invokedCommand()
	name := rtx.commandName()
	out := cmd.Output
	if out != nil && out.Stream && !item {
		return InternalError(fmt.Errorf("%s: declares a stream (output_stream); write each item with WriteOutputItem", name))
	}
	sel, selected := v.(Selection)
	if selected && sel.from == nil {
		return InternalError(fmt.Errorf("%s: the output written is a zero Selection; make one with SelectFields", name))
	}
	var schema string
	switch {
	case out == nil:
	case out.Tag != "" && selected:
		kind := out.kindOfType(sel.from)
		if kind == nil {
			return InternalError(fmt.Errorf("%s: the item written is a %s, which is not one of the kinds the command declares (%s)", name, sel.from, out.kindValues()))
		}
		tagged, err := tagSelection(name, out.Tag, kind, sel)
		if err != nil {
			return err
		}
		v, schema = tagged, kind.Schema
	case out.Tag != "":
		kind, tagged, err := tagItem(name, out, v)
		if err != nil {
			return err
		}
		v = tagged // always v's own type
		schema = kind.Schema
	default:
		if t.Kind() == reflect.Interface {
			t = reflect.TypeOf(v)
		}
		if selected {
			t = sel.from
		}
		if err := checkOutputType(name, out, t); err != nil {
			return err
		}
		schema = out.Schema
	}
	if format == "" {
		format = "json"
	}
	if schema != "" && rtx.outputChecks != nil {
		if err := rtx.outputChecks(name, schema, v, selected); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := encodeOutput(&buf, v, format, render, item); err != nil {
		if errors.Is(err, errNoRenderer) {
			return InternalError(fmt.Errorf("%s: no renderer was passed for the output format %q", name, format))
		}
		if rf, ok := asError[rendererError](err); ok {
			return rf.err // the handler's own error, as it returned it
		}
		return InternalError(fmt.Errorf("%s: write output as %s: %w", name, format, err))
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("%s: write output: %w", name, err)
	}
	return nil
}

var errNoRenderer error = constError("no renderer")

// rendererError carries a renderer's error out of encodeOutput untouched.
type rendererError struct{ err error }

func (r rendererError) Error() string { return r.err.Error() }

// encodeOutput serializes v in format into w.
func encodeOutput(w *bytes.Buffer, v any, format string, render func(io.Writer, string, any) error, item bool) error {
	switch format {
	case "json":
		var b []byte
		var err error
		if item {
			b, err = json.Marshal(v)
		} else {
			b, err = json.MarshalIndent(v, "", "  ")
		}
		if err != nil {
			return err //nolint:wrapcheck // wrapped by the caller, which names the command and format
		}
		w.Write(b)
		w.WriteByte('\n')
	case "yaml":
		b, err := yaml.Marshal(v)
		if err != nil {
			return err //nolint:wrapcheck // wrapped by the caller
		}
		if item {
			w.WriteString("---\n")
		}
		w.Write(b)
	case "toml":
		if item {
			return errors.New("toml cannot be written as a stream")
		}
		doc := v
		if s, ok := doc.(Selection); ok {
			doc = nativeNumbers(s.data) // the toml encoder doesn't ask the top-level value
		}
		b, err := toml.Marshal(doc)
		if err != nil {
			return err //nolint:wrapcheck // wrapped by the caller
		}
		w.Write(b)
	default:
		if render == nil {
			return errNoRenderer
		}
		if err := render(w, format, v); err != nil {
			return rendererError{err}
		}
	}
	return nil
}

// outputCheck checks a value against a command's output schema. A program holds one only when
// output checks are on, so a program that never turns them on doesn't link the schema validator.
type outputCheck func(name, schema string, v any, partial bool) error

func outputCheckFor(enabled bool) outputCheck {
	if enabled {
		return checkOutputSchema
	}
	return nil
}

// checkOutputType reports an internal error when t is not the declared output type (or a
// pointer to it).
func checkOutputType(name string, out *OutputDef, t reflect.Type) error {
	if out.Type == nil || t == nil {
		return nil
	}
	if t == out.Type || (t.Kind() == reflect.Pointer && t.Elem() == out.Type) {
		return nil
	}
	return InternalError(fmt.Errorf("%s: the output written is a %s, but the command declares %s", name, t, out.Type))
}

// outputValidators caches each output schema, compiled, by its text.
var outputValidators sync.Map // string → *jsonschema.Schema

// checkOutputValue checks v against schema, reporting every field that does not match.
func checkOutputValue(name, schema string, v any) error {
	return checkOutputSchema(name, schema, v, false)
}

// checkOutputSchema is checkOutputValue, against the schema with every `required` removed when
// partial, as for a [Selection].
func checkOutputSchema(name, schema string, v any, partial bool) error {
	key := schema
	if partial {
		key = "partial:" + schema
	}
	cached, ok := outputValidators.Load(key)
	if !ok {
		text := []byte(schema)
		if partial {
			var err error
			if text, err = partialSchema(text); err != nil {
				return InternalError(fmt.Errorf("%s: the output schema does not compile: %w", name, err))
			}
		}
		s, err := jsonschema.Compile(text)
		if err != nil {
			return InternalError(fmt.Errorf("%s: the output schema does not compile: %w", name, err))
		}
		cached, _ = outputValidators.LoadOrStore(key, s)
	}
	compiled, ok := cached.(*jsonschema.Schema)
	if !ok {
		return InternalError(errors.New("rotini: output schema cache holds a foreign value"))
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return InternalError(fmt.Errorf("%s: encode the output to check it: %w", name, err))
	}
	if msgs := outputProblems(compiled, raw); len(msgs) > 0 {
		return InternalError(fmt.Errorf("%s: output does not match its contract: %s", name, strings.Join(msgs, "; ")))
	}
	return nil
}

// outputProblems validates raw against schema and lists each failure as "path: message".
func outputProblems(schema *jsonschema.Schema, raw []byte) []string {
	result, err := schema.Validate(raw)
	if err != nil {
		return []string{err.Error()}
	}
	if result.Valid {
		return nil
	}
	var msgs []string
	var walk func(errs []jsonschema.ValidationError)
	walk = func(errs []jsonschema.ValidationError) {
		for _, e := range errs {
			if len(e.Causes) > 0 {
				walk(e.Causes)
				continue
			}
			msg := outputPath(e.InstanceLocation) + ": " + e.Message
			if !slices.Contains(msgs, msg) {
				msgs = append(msgs, msg)
			}
		}
	}
	walk(result.Errors)
	return msgs
}

// outputPath renders a JSON Pointer into the output as a path a reader follows:
// "/tasks/2/status" → output.tasks[2].status.
func outputPath(pointer string) string {
	var b strings.Builder
	b.WriteString("output")
	for seg := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		if seg == "" {
			continue
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		if _, err := strconv.Atoi(seg); err == nil {
			b.WriteByte('[')
			b.WriteString(seg)
			b.WriteByte(']')
			continue
		}
		b.WriteByte('.')
		b.WriteString(seg)
	}
	return b.String()
}

// DecodeOutput decodes a command's output, as captured from its stdout, into T, checking it
// against the command's declared schema. T is the generated <Prefix>Output type, which names
// the command; for output written item by item with [Context.WriteOutputItem], T is a slice of
// it and every item is decoded. format is json, yaml or toml. It is for tests:
//
//	var stdout bytes.Buffer
//	p := cmd.NewProgram().WithStdout(&stdout)
//	p.Run([]string{"list", "-o", "json"})
//	list, err := rotini.DecodeOutput[cmd.TaskrListOutput](p, stdout.Bytes(), "json")
//
// Output written from a [Selection] leaves fields out, so it fails the schema's `required`
// here; DecodeOutput is for whole outputs.
//
// For a stream whose items come in several kinds, T is a slice of the <Prefix>Output interface:
// each item is decoded into the type of the kind its tag names, and checked against that kind.
// An item whose tag names no kind is an error naming the tag.
func DecodeOutput[T any](p *Program, data []byte, format string) (T, error) {
	var zero T
	if !slices.Contains(machineFormats, format) {
		return zero, fmt.Errorf("DecodeOutput reads json, yaml or toml, not %q", format)
	}
	t := reflect.TypeFor[T]()
	out, name, stream := findOutput(p, t)
	if out == nil {
		elem := t
		if t.Kind() == reflect.Slice {
			elem = t.Elem()
		}
		if s, n, _ := findAnyOutput(p, t); s != nil && s.Tag != "" && s.Type != elem {
			return zero, fmt.Errorf("%s writes a stream of %s, of which %s is one kind; decode it into []%s", n, s.Type, elem, s.Type)
		} else if s != nil && s.Stream {
			return zero, fmt.Errorf("%s writes a stream of %s (output_stream); decode it into []%s", n, t, t)
		} else if s != nil {
			return zero, fmt.Errorf("%s writes one %s, not a stream; decode it into %s", n, s.Type, s.Type)
		}
		return zero, fmt.Errorf("no command declares %s as its output", t)
	}
	var docs []any
	var err error
	if stream {
		docs, err = decodeStream(data, format)
	} else {
		var doc any
		doc, err = decodeOutputDocument(data, format)
		docs = []any{doc}
	}
	if err != nil {
		return zero, fmt.Errorf("%s: decode the output as %s: %w", name, format, err)
	}
	if stream && out.Tag != "" {
		return decodeKinds[T](name, out, t, docs)
	}
	for i, doc := range docs {
		if out.Schema == "" {
			break
		}
		if err := checkOutputValue(name, out.Schema, doc); err != nil {
			if stream {
				return zero, fmt.Errorf("item %d: %w", i, err)
			}
			return zero, err
		}
	}
	var whole any = docs
	if !stream {
		whole = docs[0]
	}
	raw, err := json.Marshal(whole)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", name, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, fmt.Errorf("%s: decode the output into %s: %w", name, t, err)
	}
	return v, nil
}

// decodeKinds decodes the items of a stream whose items come in several kinds into t, a slice
// of the kinds' interface: each item's tag names its kind, whose schema it is checked against
// and whose type it is decoded into.
func decodeKinds[T any](name string, out *OutputDef, t reflect.Type, docs []any) (T, error) {
	var zero T
	items := reflect.MakeSlice(t, len(docs), len(docs))
	for i, doc := range docs {
		obj, _ := doc.(map[string]any)
		value, ok := obj[out.Tag].(string)
		if !ok {
			return zero, fmt.Errorf("%s: item %d has no %q saying which kind it is", name, i, out.Tag)
		}
		kind := out.kindOfValue(value)
		if kind == nil {
			return zero, fmt.Errorf("%s: item %d is of an unknown kind: %s %q is none of %s", name, i, out.Tag, value, out.kindValues())
		}
		if kind.Schema != "" {
			if err := checkOutputValue(name, kind.Schema, doc); err != nil {
				return zero, fmt.Errorf("item %d: %w", i, err)
			}
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return zero, fmt.Errorf("%s: %w", name, err)
		}
		if !kind.Type.AssignableTo(t.Elem()) {
			return zero, fmt.Errorf("%s: kind %q's type %s is not a %s", name, kind.Value, kind.Type, t.Elem())
		}
		item := reflect.New(kind.Type)
		if err := json.Unmarshal(raw, item.Interface()); err != nil {
			return zero, fmt.Errorf("%s: decode item %d into %s: %w", name, i, kind.Type, err)
		}
		items.Index(i).Set(item.Elem())
	}
	v, ok := reflect.TypeAssert[T](items)
	if !ok {
		return zero, fmt.Errorf("%s: the items do not decode into %s", name, t)
	}
	return v, nil
}

// findOutput finds the command whose declared output T is: the declared type for a command
// that writes one value, or a slice of it for one that declares a stream (output_stream);
// stream reports which. It returns the declaration and the command's name as typed.
func findOutput(p *Program, t reflect.Type) (out *OutputDef, name string, stream bool) {
	find := func(matches func(*OutputDef) bool) (*OutputDef, string) {
		if matches(p.def.Output) {
			return p.def.Output, p.def.Name
		}
		var walk func(cmds []CommandDef, path string) (*OutputDef, string)
		walk = func(cmds []CommandDef, path string) (*OutputDef, string) {
			for i := range cmds {
				name := path + " " + cmds[i].Name
				if matches(cmds[i].Output) {
					return cmds[i].Output, name
				}
				if out, n := walk(cmds[i].Commands, name); out != nil {
					return out, n
				}
			}
			return nil, ""
		}
		return walk(p.def.Commands, p.def.Name)
	}
	if out, name := find(func(o *OutputDef) bool { return o != nil && !o.Stream && o.Type == t }); out != nil {
		return out, name, false
	}
	if t.Kind() == reflect.Slice {
		if out, name := find(func(o *OutputDef) bool { return o != nil && o.Stream && o.Type == t.Elem() }); out != nil {
			return out, name, true
		}
	}
	return nil, "", false
}

// findAnyOutput finds a command whose declared type is T or T's element, whether or not it
// streams, to say why DecodeOutput can't read T.
func findAnyOutput(p *Program, t reflect.Type) (*OutputDef, string, bool) {
	match := func(o *OutputDef) bool {
		return o != nil && (o.Type == t || (t.Kind() == reflect.Slice && o.Type == t.Elem()) || o.kindOfType(t) != nil || (t.Kind() == reflect.Slice && o.kindOfType(t.Elem()) != nil))
	}
	if match(p.def.Output) {
		return p.def.Output, p.def.Name, true
	}
	var walk func(cmds []CommandDef, path string) (*OutputDef, string, bool)
	walk = func(cmds []CommandDef, path string) (*OutputDef, string, bool) {
		for i := range cmds {
			name := path + " " + cmds[i].Name
			if match(cmds[i].Output) {
				return cmds[i].Output, name, true
			}
			if o, n, ok := walk(cmds[i].Commands, name); ok {
				return o, n, true
			}
		}
		return nil, "", false
	}
	return walk(p.def.Commands, p.def.Name)
}

// decodeOutputDocument decodes one json, yaml or toml document into its JSON-shaped value.
func decodeOutputDocument(data []byte, format string) (any, error) {
	var doc any
	var err error
	switch format {
	case "json":
		doc, err = unmarshalExact(data)
	case "yaml":
		err = yaml.Unmarshal(data, &doc)
	default:
		var m map[string]any
		err = toml.Unmarshal(data, &m)
		doc = m
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by DecodeOutput, which names the command and format
	}
	return normalizeJSON(doc)
}

// decodeStream decodes a stream: one json value per line, or one yaml document per item.
func decodeStream(data []byte, format string) ([]any, error) {
	var docs []any
	switch format {
	case "json":
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		for {
			var doc any
			if err := dec.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					return docs, nil
				}
				return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
			}
			docs = append(docs, doc)
		}
	case "yaml":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var doc any
			if err := dec.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					return docs, nil
				}
				return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
			}
			n, err := normalizeJSON(doc)
			if err != nil {
				return nil, err
			}
			docs = append(docs, n)
		}
	default:
		return nil, errors.New("toml cannot hold a stream")
	}
}

// normalizeJSON round-trips a decoded value through JSON, so a yaml or toml document takes the
// same shape a json one does (map[string]any, json.Number) before it is checked.
func normalizeJSON(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
	}
	return unmarshalExact(raw)
}

// unmarshalExact is json.Unmarshal into an any that keeps numbers as [json.Number], so an
// integer past 2^53 isn't rounded through float64.
func unmarshalExact(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid character after top-level value")
	}
	return out, nil
}

// WithOutputChecks makes every [Context.WriteOutput] and [Context.WriteOutputItem] call, and
// their To forms, check the value against the command's declared output schema before writing
// it. A value that does not match is an internal error naming each field at fault, and nothing
// is written. It is off by default; enable it in tests or debug builds:
//
//	p := cmd.NewProgram().WithOutputChecks(true)
//
// Only output written through those methods is checked. To check captured stdout, use
// [DecodeOutput].
func (p *Program) WithOutputChecks(enabled bool) *Program {
	p.outputChecks = outputCheckFor(enabled)
	return p
}
