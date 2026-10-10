package rotini

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"slices"
	"strings"
)

// stdinTag is a parsed `stdin:"<format>[,stream][,nul][,required][,unless=<argument>]"` struct
// tag. Options may come in any order.
type stdinTag struct {
	format   string
	stream   bool   // the field is an iterator, read as the handler ranges over it
	nul      bool   // lines are separated by NUL bytes, not newlines
	required bool   // an empty stdin is a usage error
	unless   string // the file argument whose value means stdin is not read
}

// parseStdinTag parses a Stdin field's stdin tag.
func parseStdinTag(tag string) stdinTag {
	format, opts, _ := strings.Cut(tag, ",")
	t := stdinTag{format: format}
	if opts == "" {
		return t
	}
	for opt := range strings.SplitSeq(opts, ",") {
		switch opt = strings.TrimSpace(opt); {
		case opt == "stream":
			t.stream = true
		case opt == "nul":
			t.nul = true
		case opt == "required":
			t.required = true
		case strings.HasPrefix(opt, "unless="):
			t.unless = strings.TrimPrefix(opt, "unless=")
		}
	}
	return t
}

// emptyMessage is the usage error for a required stdin with nothing piped.
func (t stdinTag) emptyMessage() string {
	what := "a " + t.format + " document"
	switch t.format {
	case "text", "bytes":
		what = "a " + t.format + " payload"
	case "lines":
		what = "lines"
		if t.nul {
			what = "NUL-separated lines"
		}
	case "jsonl":
		what = "JSON Lines"
	}
	msg := "required stdin payload is empty; pipe " + what
	if t.unless != "" {
		msg += " or give <" + t.unless + ">"
	}
	return msg
}

// stdinLines is the one line reader over a run's stdin stream that every streamed field
// shares, so a second range, or a second Inputs call, continues where the last stopped.
type stdinLines struct {
	r     *bufio.Reader
	line  int  // lines read so far, blank ones included; jsonl errors name the line
	items int  // items yielded so far; a required stream with none is an error
	bom   bool // the first line has been read, so its byte-order mark is gone
	err   error
}

// stdinLines returns the run's shared line reader, built over the stdin stream on first use.
func (rtx *Context) stdinLines() *stdinLines {
	state := rtx.stdinState()
	state.mu.Lock()
	l := state.lineReader
	state.mu.Unlock()
	if l != nil {
		return l
	}
	r := rtx.stdinStream()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lineReader == nil {
		state.lineReader = &stdinLines{r: bufio.NewReaderSize(r, stdinChunkSize)}
	}
	return state.lineReader
}

// piped reports whether the source can carry piped input: false for a nil reader and a
// terminal, which a declared stdin reads as nothing.
func (s *stdinState) piped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inspect()
	return !s.terminal
}

// next reads one item ending at sep (or the end of the stream). The final item needs no
// separator, and an empty final item is no item. A failed read is returned once and then
// again on every call.
func (l *stdinLines) next(sep byte) (string, bool, error) {
	if l.err != nil {
		return "", false, l.err
	}
	var long []byte // a line longer than the reader's buffer, accumulated
	for {
		chunk, err := l.r.ReadSlice(sep)
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, chunk...)
			continue
		}
		data := chunk
		if long != nil {
			long = append(long, chunk...)
			data = long
		}
		if err != nil {
			l.err = err
			if len(data) == 0 {
				return "", false, err
			}
		} else {
			data = data[:len(data)-1] // the separator
		}
		l.line++
		if !l.bom {
			l.bom = true
			data = bytes.TrimPrefix(data, []byte(utf8BOM))
		}
		return string(data), true, nil
	}
}

// streamLines is the iterator a streamed `lines` Stdin field holds: one line per step, split
// on "\n" with a trailing "\r" removed, or on NUL with the bytes kept as they are. A read error
// or a canceled run is yielded once, and the iterator then stops.
func (rtx *Context) streamLines(tag stdinTag) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		l := rtx.stdinLines()
		sep := byte('\n')
		if tag.nul {
			sep = 0
		}
		for {
			line, ok, err := l.next(sep)
			if !ok {
				if err := streamEnd(l, tag, err); err != nil {
					yield("", err)
				}
				return
			}
			if !tag.nul {
				line = strings.TrimSuffix(line, "\r")
			}
			l.items++
			if !yield(line, nil) {
				return
			}
		}
	}
}

// streamEnd turns the end of a stream into the error its iterator yields: nil at a clean end,
// the read failure (already an [*InputError], see stdinStream), or the required-stdin error when
// nothing was read.
func streamEnd(l *stdinLines, tag stdinTag, err error) error {
	if err != nil && !errors.Is(err, io.EOF) {
		return err // the stream reported it (see stdinStream)
	}
	if tag.required && l.items == 0 {
		return usageBind(channelStdin, "", tag.emptyMessage(), nil)
	}
	return nil
}

// errorType is the reflect type of the error interface.
var errorType = reflect.TypeFor[error]()

// streamJSONL builds the iterator a streamed `jsonl` Stdin field holds, an
// iter.Seq2[<Prefix>Stdin, error] of fieldType: one record per non-blank line, checked against
// schema (when given) and decoded into the record type. A record that fails, a read error or a
// canceled run is yielded once, and the iterator then stops.
func (rtx *Context) streamJSONL(fieldType reflect.Type, tag stdinTag, schema string) (reflect.Value, bool) {
	if fieldType.Kind() != reflect.Func || fieldType.NumIn() != 1 {
		return reflect.Value{}, false
	}
	yieldType := fieldType.In(0)
	if yieldType.Kind() != reflect.Func || yieldType.NumIn() != 2 || yieldType.In(1) != errorType {
		return reflect.Value{}, false
	}
	recType := yieldType.In(0)
	noErr := reflect.Zero(errorType)
	fn := reflect.MakeFunc(fieldType, func(args []reflect.Value) []reflect.Value {
		yield := args[0]
		l := rtx.stdinLines()
		fail := func(err error) { yield.Call([]reflect.Value{reflect.Zero(recType), reflect.ValueOf(err)}) }
		for {
			line, ok, err := l.next('\n')
			if !ok {
				if err := streamEnd(l, tag, err); err != nil {
					fail(err)
				}
				return nil
			}
			raw := bytes.TrimSpace([]byte(line))
			if len(raw) == 0 {
				continue
			}
			rec := reflect.New(recType)
			if err := decodeJSONLRecord(raw, l.line, schema, rec.Interface()); err != nil {
				fail(err)
				return nil
			}
			l.items++
			if !yield.Call([]reflect.Value{rec.Elem(), noErr})[0].Bool() {
				return nil
			}
		}
	})
	return fn, true
}

// decodeJSONLRecord checks one JSON Lines record against schema (when given) and decodes it
// into out. Errors name the line.
func decodeJSONLRecord(raw []byte, line int, schema string, out any) error {
	lineErr := func(ie *InputError) error {
		return &InputError{Channel: channelStdin, Input: ie.Input, Msg: fmt.Sprintf("stdin line %d: %s", line, ie.Msg), Cause: ie.Cause, usage: ie.usage}
	}
	if schema != "" {
		compiled, err := documentSchema(schema)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil || dec.InputOffset() != int64(len(raw)) {
			return notJSONRecord(raw, line)
		}
		result, err := compiled.ValidateValue(v)
		if err != nil {
			return usageBind(channelStdin, "", fmt.Sprintf("stdin line %d: could not check the record against its schema", line), err)
		}
		if err := documentResult(schema, result); err != nil {
			if ie, ok := errors.AsType[*InputError](err); ok {
				return lineErr(ie)
			}
			return err
		}
	} else if !json.Valid(raw) {
		return notJSONRecord(raw, line)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return usageBind(channelStdin, "", fmt.Sprintf("stdin line %d: could not bind the record: %s", line, decodeMessage(err)), err)
	}
	return nil
}

// notJSONRecord reports a JSON Lines line that is not one JSON value.
func notJSONRecord(raw []byte, line int) error {
	var probe any
	err := json.Unmarshal(raw, &probe)
	if err == nil {
		err = errors.New("more than one JSON value on the line")
	}
	return usageBind(channelStdin, "", fmt.Sprintf("stdin line %d: not a JSON record: %s", line, decodeMessage(err)), err)
}

// splitStdinLines splits a slurped lines payload: on NUL bytes with every byte kept, or on
// newlines with a trailing "\r" removed from each line. One leading byte-order mark is removed,
// and a final separator adds no empty line.
func splitStdinLines(text string, nul bool) []string {
	text = strings.TrimPrefix(text, utf8BOM)
	if nul {
		items := strings.Split(text, "\x00")
		if items[len(items)-1] == "" {
			items = items[:len(items)-1]
		}
		return items
	}
	lines := strings.Split(trimAcquiredPayload(text), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r") // CRLF input stays usable
	}
	return lines
}

// bindJSONL decodes every record of a slurped JSON Lines payload into the field, a
// *[]<Prefix>Stdin, checking each against its record schema in schemas (when given) first.
func bindJSONL(sf reflect.Value, text string, schemas map[string]string, tag stdinTag) error {
	elem := sf.Type().Elem()
	if elem.Kind() != reflect.Slice {
		return internalBind(channelStdin, "", "stdin format jsonl needs a []<record> payload type", nil)
	}
	recType := elem.Elem()
	schema := schemas[recType.Name()]
	out := reflect.MakeSlice(elem, 0, 0)
	text = strings.TrimPrefix(text, utf8BOM)
	n := 0
	for line := range strings.SplitSeq(text, "\n") {
		n++
		raw := bytes.TrimSpace([]byte(line))
		if len(raw) == 0 {
			continue
		}
		rec := reflect.New(recType)
		if err := decodeJSONLRecord(raw, n, schema, rec.Interface()); err != nil {
			return err
		}
		out = reflect.Append(out, rec.Elem())
	}
	if out.Len() == 0 && tag.required {
		return usageBind(channelStdin, "", tag.emptyMessage(), nil)
	}
	p := reflect.New(elem)
	p.Elem().Set(out)
	sf.Set(p)
	return nil
}

// stdinStream returns the run's stdin as the one shared, cancellable stream, byte for byte:
// no byte-order mark removed and nothing trimmed. When stdin is a terminal or nil, so nothing
// can be piped, it reads as empty. Reads continue where any other stream consumer stopped, and
// replay the payload when it was already read whole. A read that a trapped signal interrupts
// returns an [*InputError] whose cause is the run's cancellation cause, and stops the run with
// the signal's exit code; any other failure is an internal [*InputError]. The reader must be
// read from one goroutine.
func (rtx *Context) stdinStream() io.Reader {
	state := rtx.stdinState()
	if !state.piped() {
		return strings.NewReader("")
	}
	return &stdinStreamReader{rtx: rtx, r: state.reader()}
}

// stdinStreamReader reports a failed stdin read the way the declared stdin does.
type stdinStreamReader struct {
	rtx *Context
	r   io.Reader
}

func (r *stdinStreamReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = stdinReadError(r.rtx.currentRun(), err)
		r.rtx.haltOnSignal(err)
	}
	return n, err
}

// leafArgValues returns the values the leaf command's argument name received from the command
// line, nil when it got none or the leaf declares no such argument.
func leafArgValues(chain []Command, store *parsedInputs, name string) []string {
	if len(chain) == 0 || store == nil {
		return nil
	}
	leaf := len(chain) - 1
	defs := chain[leaf].Arguments
	i := slices.IndexFunc(defs, func(a ArgDef) bool { return a.Name == name })
	if i < 0 || leaf >= len(store.scopes) {
		return nil
	}
	args := store.scopes[leaf].args
	span := argSpans(defs, len(args))[i]
	if span[1] <= span[0] {
		return nil
	}
	return args[span[0]:span[1]]
}

// fileGiven reports whether the tag's unless argument got a file other than "-", so stdin is not
// read. argValues returns the argument's values from the command line.
func (t stdinTag) fileGiven(argValues func(name string) ([]string, error)) (bool, error) {
	if t.unless == "" {
		return false, nil
	}
	vals, err := argValues(t.unless)
	if err != nil {
		return false, err
	}
	return len(vals) > 0 && !slices.Contains(vals, "-"), nil
}
