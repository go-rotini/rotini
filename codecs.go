package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/recon"
	"github.com/go-rotini/yaml"
)

// JSON wires the JSON document format, for configuration files, stdin documents and records,
// and object flags.
func JSON() Feature {
	return Feature{name: "codec:json", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{
			name:   "json",
			codec:  func() recon.Codec { return recon.JSON },
			locate: locateJSONC,
			decodeAny: func(data []byte) (any, error) {
				if err := invalidUTF8("json", data); err != nil {
					return nil, err
				}
				if !json.Valid(data) {
					var probe any
					err := fmt.Errorf("decode json: %w", json.Unmarshal(data, &probe))
					return nil, jsonPositionError(err, data)
				}
				// A key given twice is an error, as in the other formats, not a silent last-wins.
				if e := jsonDuplicateKey(data); e != nil {
					return nil, duplicateKeyError(fmt.Errorf("decode json: %w", e), e.Key, e.Pos.Line, e.Pos.Column)
				}
				return json.RawMessage(bytes.TrimSpace(data)), nil
			},
		})
	}}
}

// YAML wires the YAML document format, for configuration files, stdin documents and object
// flags.
func YAML() Feature {
	return Feature{name: "codec:yaml", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{
			name:   "yaml",
			codec:  func() recon.Codec { return recon.YAML },
			locate: locateYAML,
			decodeAny: func(data []byte) (any, error) {
				if err := invalidUTF8("yaml", data); err != nil {
					return nil, err
				}
				var v any
				if err := yaml.UnmarshalWithOptions(data, &v, yaml.WithDisallowDuplicateKey()); err != nil {
					wrapped := fmt.Errorf("decode yaml: %w", err)
					if e, ok := asError[*yaml.DuplicateKeyError](err); ok {
						return nil, duplicateKeyError(wrapped, e.Key, e.Pos.Line, e.Pos.Column)
					}
					if e, ok := asError[*yaml.SyntaxError](err); ok {
						return nil, &docError{err: wrapped, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
					}
					return nil, wrapped
				}
				return jsonCompatible(v), nil
			},
		})
	}}
}

// TOML wires the TOML document format, for configuration files and stdin documents.
func TOML() Feature {
	return Feature{name: "codec:toml", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{name: "toml", codec: func() recon.Codec { return recon.TOML }, locate: locateTOML})
	}}
}

// JSONC wires the JSON-with-comments document format, for configuration files and stdin
// documents.
func JSONC() Feature {
	return Feature{name: "codec:jsonc", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{
			name:   "jsonc",
			codec:  func() recon.Codec { return recon.JSONC },
			locate: locateJSONC,
			decodeAny: func(data []byte) (any, error) {
				if err := invalidUTF8("jsonc", data); err != nil {
					return nil, err
				}
				var v any
				if err := jsonc.Unmarshal(data, &v); err != nil {
					wrapped := fmt.Errorf("decode jsonc: %w", err)
					if e, ok := asError[*jsonc.DuplicateKeyError](err); ok {
						return nil, duplicateKeyError(wrapped, e.Key, e.Pos.Line, e.Pos.Column)
					}
					if e, ok := asError[*jsonc.SyntaxError](err); ok {
						return nil, &docError{err: wrapped, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
					}
					return nil, wrapped
				}
				return v, nil
			},
		})
	}}
}

// Dotenv wires the dotenv format, for .env files.
func Dotenv() Feature {
	return Feature{name: "codec:dotenv", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{name: "dotenv", codec: func() recon.Codec { return recon.Dotenv }})
	}}
}

// Codec wires a document format of the program's own, read-only, for configuration files and
// stdin documents. A config_files entry or a stdin declared with `format: <name>` reads with it,
// and so does a configuration file with no declared format whose extension [CodecExtensions]
// lists. Given a built-in format's name ("yaml"), it replaces that format's codec, object flags
// included. The spec names the format; the program adds the codec:
//
//	cmd.NewProgram().WithFeatures(rotini.Codec("ini", decodeINI, rotini.CodecExtensions(".ini")))
//
// decode turns a whole document into the generic tree the built-in formats produce: a
// map[string]any for each mapping, []any for each list, and strings, bools, numbers and nil for
// scalars. A configuration file's top level must be a mapping; a stdin document's may be any
// value its declared type takes. Rotini keeps precedence, profiles, schema checks, provenance and
// --explain-inputs as they are for the built-in formats, so decode only reads. To read as
// strictly as they do, decode must:
//
//   - fail on a key given twice in one mapping, rather than keep either value;
//   - fail on input holding more than one document;
//   - return nil for the format's null, which rotini treats as unset, never the text "null";
//   - keep nothing of data once it returns, since the caller may reuse it.
//
// CheckCodec in package rotinitest checks a decode against these rules. A decode error with
// Position() (line, col int) and Message() string methods is reported at that position.
//
// Name must match ^[a-z][a-z0-9-]*$, and decode must not be nil; Codec panics otherwise.
// Writing to a file in the format ([SetConfigValue], [UnsetConfigValue]) is refused with
// [InputErrorKindWriteRefused].
func Codec(name string, decode func(data []byte) (any, error), opts ...CodecOption) Feature {
	if !validCodecName(name) {
		panic(fmt.Sprintf("rotini: Codec: format name %q must match ^[a-z][a-z0-9-]*$", name))
	}
	if decode == nil {
		panic(fmt.Sprintf("rotini: Codec %q: nil decode", name))
	}
	c := &codecEntry{name: name, author: true}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	rc := &authorCodec{name: name, exts: c.exts, decode: decode}
	c.codec = func() recon.Codec { return rc }
	c.decodeAny = func(data []byte) (any, error) {
		v, err := decode(data)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return v, nil
	}
	return Feature{name: "codec:" + name, author: true, apply: func(s *featureSet) { s.replaceCodec(c) }}
}

// CodecOption configures a format wired with [Codec].
type CodecOption func(*codecEntry)

// CodecExtensions lists the file extensions that a configuration file with no declared format
// is read with the codec for. An extension may be given with or without its dot, in any case:
// "ini", ".ini" and ".INI" all mean .ini. A later codec claiming an extension wins over an
// earlier one, and over a built-in format.
func CodecExtensions(exts ...string) CodecOption {
	return func(c *codecEntry) {
		for _, e := range exts {
			e = strings.TrimPrefix(strings.ToLower(e), ".")
			if e == "" {
				continue
			}
			if e = "." + e; !slices.Contains(c.exts, e) {
				c.exts = append(c.exts, e)
			}
		}
	}
}

// CodecLocate gives the codec a way to find where a value sits in a document, so an error about
// one of a configuration file's values names its line and column (app.ini:3:1), as it does for
// the built-in formats. locate is given the document and the value's path, its mapping keys and
// list indexes outermost first, and returns the 1-indexed line and column, or ok false when it
// can't tell.
func CodecLocate(locate func(data []byte, path []string) (line, col int, ok bool)) CodecOption {
	return func(c *codecEntry) { c.locate = locate }
}

// validCodecName reports whether name matches ^[a-z][a-z0-9-]*$.
func validCodecName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// authorCodec is a [Codec] format as recon reads it.
type authorCodec struct {
	name   string
	exts   []string
	decode func([]byte) (any, error)
}

func (c *authorCodec) Name() string         { return c.name }
func (c *authorCodec) Extensions() []string { return c.exts }

// Decode decodes a document whose top level must be a mapping; an empty or null document is
// the empty mapping.
func (c *authorCodec) Decode(data []byte) (map[string]any, error) {
	v, err := c.decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", c.name, err)
	}
	switch m := v.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return m, nil
	}
	return nil, fmt.Errorf("%w: %s root must be a mapping, got %T", recon.ErrUnsupportedFormat, c.name, v)
}

// Encode refuses: a [Codec] format is read-only.
func (c *authorCodec) Encode(map[string]any) ([]byte, error) {
	return nil, fmt.Errorf("rotini: the %s format is read-only", c.name)
}

// codecCtor is the constructor that wires the codec of a known format, "" for any other.
func codecCtor(format string) string {
	switch format {
	case "json":
		return "JSON()"
	case "yaml":
		return "YAML()"
	case "toml":
		return "TOML()"
	case "jsonc":
		return "JSONC()"
	case "dotenv":
		return "Dotenv()"
	}
	return ""
}

// extensionFormat is the known format a file extension (lowercase, with its dot) names.
func extensionFormat(ext string) string {
	switch ext {
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".json":
		return "json"
	case ".jsonc", ".json5":
		return "jsonc"
	case ".env":
		return "dotenv"
	}
	return ""
}

// missingCodec is the error for a document in format whose codec is not wired; subject says
// what reads it. A format rotini doesn't ship is the program's to add with [Codec].
func missingCodec(format, subject string) *WiringError {
	if ctor := codecCtor(format); ctor != "" {
		return missingFeature("codec:"+format, ctor, subject)
	}
	return &WiringError{
		Feature: "codec:" + format,
		Msg: fmt.Sprintf("%s, but the program does not wire codec:%s; %s is not a format rotini ships, "+
			"so add rotini.Codec(%q, …) to the program's features (Program.WithFeatures)", subject, format, format, format),
	}
}

// codecFor is the wired codec a configuration file decodes with: its declared format, else
// the one its extension names, the codec wired last winning. It is nil, with no error, for an
// extension no codec is known by, which recon then reports as unsupported, and a
// [*WiringError] for a declared format, or a built-in format's extension, whose codec the
// program does not wire.
func (s *featureSet) codecFor(format, path string) (*codecEntry, error) {
	if format != "" {
		if c := s.codec(format); c != nil {
			return c, nil
		}
		return nil, missingCodec(format, fmt.Sprintf("configuration file %s is %s", path, format))
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, c := range slices.Backward(s.codecs) {
		if slices.Contains(c.codec().Extensions(), ext) {
			return c, nil
		}
	}
	if known := extensionFormat(ext); known != "" {
		return nil, missingCodec(known, fmt.Sprintf("configuration file %s is %s", path, known))
	}
	return nil, nil //nolint:nilnil // an unknown extension: recon reports it
}

// newRegistry builds a recon registry whose codec set is the program's wired codecs; every
// registry rotini builds goes through it.
func newRegistry(fs *featureSet, opts ...recon.Option) (*recon.Registry, error) {
	return recon.NewWithCodecsWithoutWatch(fs.reconCodecs(), opts...) //nolint:wrapcheck // reported by the caller
}

// newFileSource builds a recon file source for path that decodes with c, or, when c is nil,
// with the wired codec its options or extension name.
func newFileSource(fs *featureSet, path string, c *codecEntry, opts ...recon.FileOption) (recon.Source, error) {
	if c != nil {
		opts = append(opts, recon.WithFileCodec(c.codec()))
	}
	return recon.NewFileSourceWithCodecs(fs.reconCodecs(), path, opts...) //nolint:wrapcheck // reported by the caller
}

// parsePosition is the 1-indexed line and column at which a document failed to decode, read
// from a [*recon.ParseError] or a [recon.PositionError] in err's chain.
func parsePosition(err error) (line, col int, ok bool) {
	if pe, isParse := asError[*recon.ParseError](err); isParse && pe.Position.Line > 0 {
		return pe.Position.Line, pe.Position.Column, true
	}
	if e, isPos := asError[recon.PositionError](err); isPos {
		line, col = e.Position()
		return line, col, line > 0
	}
	return 0, 0, false
}

// parseMessage is a decoder's description of a failure without its position, read from a
// [*recon.ParseError] or a [recon.PositionError] in err's chain, else from more, else err's
// own text.
func parseMessage(err error, more func(error) (string, bool)) string {
	if e, ok := asError[*recon.ParseError](err); ok && e.Msg != "" {
		return e.Msg
	}
	if e, ok := asError[recon.PositionError](err); ok {
		return e.Message()
	}
	if more != nil {
		if msg, ok := more(err); ok {
			return msg
		}
	}
	return err.Error()
}

// docError is a document decode failure rotini's own decoding found, with where it is. It
// reads as the error it wraps.
type docError struct {
	err       error
	line, col int
	msg       string
}

func (e *docError) Error() string             { return e.err.Error() }
func (e *docError) Unwrap() error             { return e.err }
func (e *docError) Position() (line, col int) { return e.line, e.col }
func (e *docError) Message() string           { return e.msg }

// jsonPositionError gives an encoding/json syntax or type error in err the line and column of
// its byte offset into data; any other error is returned as it is.
func jsonPositionError(err error, data []byte) error {
	msg, ok := jsonMessage(err)
	if !ok {
		return err
	}
	line, col, _ := recon.ParsePosition(err, data)
	return &docError{err: err, line: line, col: col, msg: msg}
}

// errInvalidUTF8 is the reason a document that is not valid UTF-8 does not decode.
var errInvalidUTF8 = errors.New("invalid UTF-8 in input")

// invalidUTF8 reports the first byte of a format document that is not valid UTF-8, with its
// line and column, or returns nil when data is valid UTF-8.
func invalidUTF8(format string, data []byte) error {
	if utf8.Valid(data) {
		return nil
	}
	off := 0
	for off < len(data) {
		r, size := utf8.DecodeRune(data[off:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		off += size
	}
	head := data[:off]
	line := bytes.Count(head, []byte("\n")) + 1
	col := off - bytes.LastIndexByte(head, '\n')
	err := fmt.Errorf("decode %s: line %d, column %d: %w", format, line, col, errInvalidUTF8)
	return &docError{err: err, line: line, col: col, msg: errInvalidUTF8.Error()}
}

// duplicateKeyError reports a key given twice in a document, at its second occurrence.
func duplicateKeyError(err error, key string, line, col int) error {
	return &docError{err: err, line: line, col: col, msg: fmt.Sprintf("duplicate key %q", key)}
}

// jsonMessage reads an encoding/json error's own text, as [parseMessage]'s more.
func jsonMessage(err error) (string, bool) {
	if e, ok := asError[*json.SyntaxError](err); ok {
		return e.Error(), true
	}
	if e, ok := asError[*json.UnmarshalTypeError](err); ok {
		return e.Error(), true
	}
	return "", false
}
