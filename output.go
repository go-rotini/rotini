package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

// machineFormats are the formats rotini serializes itself. Every other format is a human format,
// laid out by the renderer the handler passes.
var machineFormats = []string{"json", "yaml", "toml"}

// WriteOutput writes v, the invoked command's output, to rtx.Stdout in format: indented json,
// yaml or toml by rotini, any other format by render, which may be nil when the handler only
// ever passes those three. An empty format means json.
//
// It returns an internal error when v is not the type the command declares as its output, when no renderer is passed for a format rotini does not
// write, or, with [Program.WithOutputChecks], when v does not match the declared shape. Nothing
// is written then. A renderer's error is returned unwrapped. A command that declares no output may still use it; nothing is checked.
func (rtx *Context) WriteOutput[T any](v T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, v, format, render, false)
}

// WriteOutputItem writes one item of a stream to rtx.Stdout, for a command that writes its output
// item by item as each is ready: compact json, one value per line; a yaml document starting
// "---"; any other format by render. toml cannot be streamed. Its checks are WriteOutput's, with
// the item checked against the declared shape, which for such a command is one item.
func (rtx *Context) WriteOutputItem[T any](item T, format string, render func(io.Writer, string, T) error) error {
	return writeOutput(rtx, item, format, render, true)
}

// CheckOutput checks v against the invoked command's declared output schema, returning an
// internal error that names each field that does not match:
//
//	taskr list: output does not match its contract: output.tasks[2].status: value is not in enum
//
// It writes nothing. [Program.WithOutputChecks] makes every WriteOutput call check.
func (rtx *Context) CheckOutput(v any) error {
	cmd := rtx.invokedCommand()
	name := rtx.commandName()
	if cmd.Output == nil || cmd.Output.Schema == "" {
		return InternalError(fmt.Errorf("%s: declares no output to check against", name))
	}
	if err := checkOutputType(name, cmd.Output, reflect.TypeOf(v)); err != nil {
		return err
	}
	return checkOutputValue(name, cmd.Output.Schema, v)
}

// writeOutput is WriteOutput and WriteOutputItem.
func writeOutput[T any](rtx *Context, v T, format string, render func(io.Writer, string, T) error, item bool) error {
	cmd := rtx.invokedCommand()
	name := rtx.commandName()
	out := cmd.Output
	if out != nil {
		t := reflect.TypeFor[T]()
		if t.Kind() == reflect.Interface {
			t = reflect.TypeOf(any(v))
		}
		if err := checkOutputType(name, out, t); err != nil {
			return err
		}
	}
	if format == "" {
		format = "json"
	}
	if out != nil && out.Schema != "" && rtx.outputChecks {
		if err := checkOutputValue(name, out.Schema, v); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := encodeOutput(&buf, v, format, render, item); err != nil {
		if errors.Is(err, errNoRenderer) {
			return InternalError(fmt.Errorf("%s: no renderer was passed for the output format %q", name, format))
		}
		if rf, ok := errors.AsType[rendererError](err); ok {
			return rf.err // the handler's own error, as it returned it
		}
		return InternalError(fmt.Errorf("%s: write output as %s: %w", name, format, err))
	}
	if _, err := rtx.Stdout.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("%s: write output: %w", name, err)
	}
	return nil
}

var errNoRenderer = errors.New("no renderer")

// rendererError carries a renderer's error out of encodeOutput untouched.
type rendererError struct{ err error }

func (r rendererError) Error() string { return r.err.Error() }

// encodeOutput serializes v in format into w.
func encodeOutput[T any](w *bytes.Buffer, v T, format string, render func(io.Writer, string, T) error, item bool) error {
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
		b, err := toml.Marshal(v)
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
	cached, ok := outputValidators.Load(schema)
	if !ok {
		s, err := jsonschema.Compile([]byte(schema))
		if err != nil {
			return InternalError(fmt.Errorf("%s: the output schema does not compile: %w", name, err))
		}
		cached, _ = outputValidators.LoadOrStore(schema, s)
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
//	p := cmd.NewProgram(cmd.Handlers()).WithStdout(&stdout)
//	p.Run([]string{"list", "-o", "json"})
//	list, err := rotini.DecodeOutput[cmd.TaskrListOutput](p, stdout.Bytes(), "json")
func DecodeOutput[T any](p *Program, data []byte, format string) (T, error) {
	var zero T
	if !slices.Contains(machineFormats, format) {
		return zero, fmt.Errorf("DecodeOutput reads json, yaml or toml, not %q", format)
	}
	t := reflect.TypeFor[T]()
	out, name, stream := findOutput(p, t)
	if out == nil {
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

// findOutput finds the command whose declared output T is. T is the declared type for one
// document, or a slice of it for a stream of items; stream reports which. An exact match is
// preferred, so a command whose declared type is itself a slice still decodes as one document.
// It returns the declaration and the command's name as typed.
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
	if out, name := find(func(o *OutputDef) bool { return o != nil && o.Type == t }); out != nil {
		return out, name, false
	}
	if t.Kind() == reflect.Slice {
		if out, name := find(func(o *OutputDef) bool { return o != nil && o.Type == t.Elem() }); out != nil {
			return out, name, true
		}
	}
	return nil, "", false
}

// decodeOutputDocument decodes one json, yaml or toml document into its JSON-shaped value.
func decodeOutputDocument(data []byte, format string) (any, error) {
	var doc any
	var err error
	switch format {
	case "json":
		err = json.Unmarshal(data, &doc)
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
// same shape a json one does (map[string]any, float64) before it is checked.
func normalizeJSON(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err //nolint:wrapcheck // wrapped by DecodeOutput
	}
	return out, nil
}

// WithOutputChecks makes every [Context.WriteOutput] and [Context.WriteOutputItem] call check
// its value against the command's declared output schema before writing it. A value that does
// not match is an internal error naming each field at fault, and nothing is written. It is off
// by default; enable it in tests or debug builds:
//
//	p := cmd.NewProgram(cmd.Handlers()).WithOutputChecks()
//
// Only output written through WriteOutput and WriteOutputItem is checked. To check captured
// stdout, use [DecodeOutput].
func (p *Program) WithOutputChecks() *Program {
	p.outputChecks = true
	return p
}
