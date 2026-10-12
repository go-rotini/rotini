package rotini

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// fillStdin binds piped stdin to the leaf command's Stdin field, per its
// `stdin:"<format>[,stream][,nul][,required][,unless=<argument>]"` tag (see stdinTag),
// validating a decoded document against the command's stdin schema before binding. With
// nothing piped the field stays nil, or a required payload is a usage error.
//
// Stdin is not read, and the field stays nil, when a short-circuit flag is set (waived), or
// when the tag's unless argument has a value other than "-". argValues returns the leaf's
// values of a named argument as the command line gave them; it is called only for such a tag.
//
// Stdin is read once per run (see stdinState), so a later call binds the same payload. A
// streamed field is an iterator over the run's one shared stream, read as the handler ranges
// over it. A read of piped stdin ends when the run is canceled, or when the context the hook
// holds ([Context.Context]) ends, with an [*InputError] whose cause is the cancellation's.
func (b *InputReader) fillStdin(rtx *Context, v reflect.Value, waived bool, argValues func(name string) ([]string, error)) error {
	if v.Kind() != reflect.Struct || v.NumField() == 0 {
		return nil
	}
	leaf := v.Field(v.NumField() - 1) // the running command's inputs
	if leaf.Kind() != reflect.Struct {
		return nil
	}
	sf := leaf.FieldByName("Stdin")
	field, ok := leaf.Type().FieldByName("Stdin")
	if !sf.IsValid() || !ok || (sf.Kind() != reflect.Pointer && sf.Kind() != reflect.Func) {
		return nil
	}
	raw := field.Tag.Get("stdin")
	if raw == "" || waived {
		return nil // a short-circuited run never waits on stdin
	}
	tag := parseStdinTag(raw)
	fs := rtx.features()
	if fs.stdin == nil || (slices.Contains(stdinFormats, tag.format) && !fs.hasStdinFormat(tag.format)) {
		path := pathOf(rtx.chainRef())
		we := missingFeature("stdin:"+tag.format, fmt.Sprintf("Stdin(%q)", tag.format), fmt.Sprintf("command %q reads stdin as %s", path, tag.format))
		we.Command = path
		return we
	}
	return fs.stdin.fill(b, rtx, sf, tag, argValues)
}

// stdinSubject names the running command's stdin in a wiring error.
func stdinSubject(rtx *Context) string {
	return fmt.Sprintf("stdin of command %q", pathOf(rtx.chainRef()))
}

// bindStdin reads stdin into the leaf's Stdin field sf per its tag: everything fillStdin does
// once it knows the field reads stdin.
func (b *InputReader) bindStdin(rtx *Context, sf reflect.Value, tag stdinTag, argValues func(name string) ([]string, error)) error {
	if skip, err := tag.fileGiven(argValues); skip || err != nil {
		return err // the file argument is the input; stdin is not read
	}
	fs := rtx.features()
	if tag.format == "jsonl" && fs.codec("json") == nil {
		return missingCodec("json", stdinSubject(rtx)+" is jsonl")
	}

	if tag.stream {
		return b.bindStdinStream(rtx, sf, tag)
	}

	text, err := rtx.slurpStdin()
	if err != nil {
		return err
	}
	if text == "" {
		if tag.required {
			return usageBind(channelStdin, "", tag.emptyMessage(), nil)
		}
		return nil // nothing piped → leave Stdin nil
	}
	if tag.format == "bytes" {
		return bindRawStdin(sf, tag, text)
	}
	if strings.HasPrefix(text, "\xff\xfe") || strings.HasPrefix(text, "\xfe\xff") {
		return usageBind(channelStdin, "", "stdin is UTF-16 encoded; pipe UTF-8 instead", nil)
	}

	// A raw format binds the payload itself rather than decoding a document.
	if isRawStdinFormat(tag.format) {
		return bindRawStdin(sf, tag, text)
	}
	if tag.format == "jsonl" {
		return bindJSONL(fs, sf, text, b.stdinSchemas, tag)
	}
	return b.bindStdinDocument(rtx, sf, tag.format, text)
}

// bindStdinDocument decodes a stdin document into the Stdin field, checking it against the
// command's stdin schema first.
func (b *InputReader) bindStdinDocument(rtx *Context, sf reflect.Value, format, text string) error {
	fs := rtx.features()
	data := stripBOM([]byte(text))
	js := b.stdinSchemas[sf.Type().Elem().Name()]
	c := fs.codec(format)
	if c == nil && codecCtor(format) != "" {
		return missingCodec(format, stdinSubject(rtx)+" is "+format)
	}
	if js != "" && fs.schemas == nil {
		return missingFeature("schemas", "Schemas()", stdinSubject(rtx)+" has a schema")
	}
	if sf.Type().Elem().Kind() != reflect.Struct {
		return bindStdinValue(fs, sf, format, c, data, js)
	}

	if c == nil {
		return internalBind(channelStdin, "", fmt.Sprintf("unsupported stdin format %q", format), nil)
	}
	m, err := c.codec().Decode(data)
	if err != nil {
		return stdinDecodeError(format, err)
	}

	if js != "" {
		validator, err := fs.schemas.validator(js)
		if err != nil {
			return internalBind(channelStdin, "", "invalid stdin schema", err)
		}
		if err := validator.Validate(m); err != nil {
			fs.schemas.patternMessages(js, err)
			return reconBind(channelStdin, err)
		}
	}

	reg, err := newRegistry(fs, recon.WithoutWatch(), recon.WithSource(recon.NewMapSource("stdin", m)))
	if err != nil {
		return internalBind(channelStdin, "", "could not build the stdin registry", err)
	}
	defer reg.Close()

	ptr := reflect.New(sf.Type().Elem()) // *<Prefix>Stdin
	if err := bindReconWaived(reg, ptr.Interface(), false); err != nil {
		return reconBind(channelStdin, err)
	}
	sf.Set(ptr)
	return nil
}

// bindStdinStream sets a streamed Stdin field to its iterator. Nothing is read here: with
// nothing that can be piped (a terminal, or no reader) the field stays nil, or a required
// stdin is a usage error.
func (b *InputReader) bindStdinStream(rtx *Context, sf reflect.Value, tag stdinTag) error {
	if sf.Kind() != reflect.Func {
		return internalBind(channelStdin, "", "a streamed stdin needs an iterator payload type", nil)
	}
	if !rtx.stdinState().piped() {
		if tag.required {
			return usageBind(channelStdin, "", tag.emptyMessage(), nil)
		}
		return nil
	}
	switch tag.format {
	case "lines":
		seq := reflect.ValueOf(rtx.streamLines(tag))
		if !seq.Type().AssignableTo(sf.Type()) {
			return internalBind(channelStdin, "", "stdin format lines with stream needs an iter.Seq2[string, error] payload type", nil)
		}
		sf.Set(seq)
		return nil
	case "jsonl":
		yieldType := sf.Type()
		name := ""
		if yieldType.NumIn() == 1 && yieldType.In(0).Kind() == reflect.Func && yieldType.In(0).NumIn() == 2 {
			name = yieldType.In(0).In(0).Name()
		}
		fn, ok := rtx.streamJSONL(sf.Type(), tag, b.stdinSchemas[name])
		if !ok {
			return internalBind(channelStdin, "", "stdin format jsonl with stream needs an iter.Seq2[<record>, error] payload type", nil)
		}
		sf.Set(fn)
		return nil
	}
	return internalBind(channelStdin, "", fmt.Sprintf("stdin format %q can't be streamed", tag.format), nil)
}

// bindStdinValue binds a stdin document whose type is not an object, such as a list: decoded
// to JSON, checked against schema (when given), then unmarshaled into the field.
func bindStdinValue(fs *featureSet, sf reflect.Value, format string, c *codecEntry, data []byte, schema string) error {
	raw, err := decodeDocumentJSON(format, c, data)
	if err != nil {
		return stdinDecodeError(format, err)
	}
	if schema != "" {
		if err := fs.schemas.document(schema, raw); err != nil {
			return err
		}
	}
	ptr := reflect.New(sf.Type().Elem())
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return usageBind(channelStdin, "", fmt.Sprintf("could not bind stdin: %s", jsonDecodeMessage(err)), err)
	}
	sf.Set(ptr)
	return nil
}

// stdinDecodeError reports a stdin document that did not decode, with the decoder's position
// and reason.
func stdinDecodeError(format string, err error) error {
	msg := "could not parse stdin as " + format
	if line, col, ok := parsePosition(err); ok {
		msg += fmt.Sprintf(" at line %d, column %d", line, col)
	}
	return usageBind(channelStdin, "", msg+": "+decodeMessage(err), err)
}

// isRawStdinFormat reports whether format binds stdin directly instead of decoding it.
func isRawStdinFormat(format string) bool {
	return format == "text" || format == "lines" || format == "bytes"
}

// bindRawStdin sets a raw stdin field from the piped text: the whole payload as one string
// for "text", its lines for "lines" (see splitStdinLines), or its bytes exactly as piped for
// "bytes". For "text" only a leading byte-order mark and the trailing line ending are trimmed
// (see trimAcquiredPayload).
func bindRawStdin(sf reflect.Value, tag stdinTag, text string) error {
	switch tag.format {
	case "text":
		if sf.Type().Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format text needs a string payload type", nil)
		}
		p := reflect.New(sf.Type().Elem())
		p.Elem().SetString(trimAcquiredPayload(text))
		sf.Set(p)
		return nil
	case "lines":
		elem := sf.Type().Elem()
		if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.String {
			return internalBind(channelStdin, "", "stdin format lines needs a []string payload type", nil)
		}
		p := reflect.New(elem)
		p.Elem().Set(reflect.ValueOf(splitStdinLines(text, tag.nul)).Convert(elem))
		sf.Set(p)
		return nil
	case "bytes":
		elem := sf.Type().Elem()
		if elem.Kind() != reflect.Slice || elem.Elem().Kind() != reflect.Uint8 {
			return internalBind(channelStdin, "", "stdin format bytes needs a []byte payload type", nil)
		}
		p := reflect.New(elem)
		p.Elem().SetBytes([]byte(text))
		sf.Set(p)
		return nil
	}
	return internalBind(channelStdin, "", fmt.Sprintf("unsupported raw stdin format %q", tag.format), nil)
}

// readStdin returns the bytes available on r. When r is the real os.Stdin attached to a
// terminal it returns nil rather than blocking: the stdin channel is for piped input, not
// interactive typing. Any other reader is read to EOF, and a nil reader yields no data.
func readStdin(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if f, ok := r.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return nil, err //nolint:wrapcheck // reported by the caller
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return nil, nil // an interactive terminal, not a pipe/redirect
		}
	}
	return io.ReadAll(r) //nolint:wrapcheck // reported by the caller
}
