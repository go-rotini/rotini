package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/recon"
	"github.com/go-rotini/yaml"
)

// JSON wires the JSON document format, for configuration files, stdin documents and records,
// and object flags.
func JSON() Feature {
	return Feature{name: "codec:json", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{
			name:  "json",
			codec: func() recon.Codec { return recon.JSON },
			decodeAny: func(data []byte) (any, error) {
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
			name:  "yaml",
			codec: func() recon.Codec { return recon.YAML },
			decodeAny: func(data []byte) (any, error) {
				var v any
				if err := yaml.UnmarshalWithOptions(data, &v, yaml.WithDisallowDuplicateKey()); err != nil {
					wrapped := fmt.Errorf("decode yaml: %w", err)
					if e, ok := errors.AsType[*yaml.DuplicateKeyError](err); ok {
						return nil, duplicateKeyError(wrapped, e.Key, e.Pos.Line, e.Pos.Column)
					}
					if e, ok := errors.AsType[*yaml.SyntaxError](err); ok {
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
		s.addCodec(&codecEntry{name: "toml", codec: func() recon.Codec { return recon.TOML }})
	}}
}

// JSONC wires the JSON-with-comments document format, for configuration files and stdin
// documents.
func JSONC() Feature {
	return Feature{name: "codec:jsonc", apply: func(s *featureSet) {
		s.addCodec(&codecEntry{
			name:  "jsonc",
			codec: func() recon.Codec { return recon.JSONC },
			decodeAny: func(data []byte) (any, error) {
				var v any
				if err := jsonc.Unmarshal(data, &v); err != nil {
					wrapped := fmt.Errorf("decode jsonc: %w", err)
					if e, ok := errors.AsType[*jsonc.DuplicateKeyError](err); ok {
						return nil, duplicateKeyError(wrapped, e.Key, e.Pos.Line, e.Pos.Column)
					}
					if e, ok := errors.AsType[*jsonc.SyntaxError](err); ok {
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

// missingCodec is the error for a document in a known format whose codec is not wired;
// subject says what reads it.
func missingCodec(format, subject string) *WiringError {
	return missingFeature("codec:"+format, codecCtor(format), subject)
}

// codecFor is the wired codec a configuration file decodes with: its declared format, else
// the one its extension names. It is nil, with no error, for a format no codec is known by,
// which recon then reports as unsupported, and a [*WiringError] for a known format whose codec
// the program does not wire.
func (s *featureSet) codecFor(format, path string) (*codecEntry, error) {
	if format != "" {
		if c := s.codec(format); c != nil {
			return c, nil
		}
		if codecCtor(format) != "" {
			return nil, missingCodec(format, fmt.Sprintf("configuration file %s is %s", path, format))
		}
		return nil, nil //nolint:nilnil // an unknown format: recon reports it
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, c := range s.codecs {
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
	return recon.NewWithCodecs(fs.reconCodecs(), opts...) //nolint:wrapcheck // reported by the caller
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
	if pe, isParse := errors.AsType[*recon.ParseError](err); isParse && pe.Position.Line > 0 {
		return pe.Position.Line, pe.Position.Column, true
	}
	if e, isPos := errors.AsType[recon.PositionError](err); isPos {
		line, col = e.Position()
		return line, col, line > 0
	}
	return 0, 0, false
}

// parseMessage is a decoder's description of a failure without its position, read from a
// [*recon.ParseError] or a [recon.PositionError] in err's chain, else from more, else err's
// own text.
func parseMessage(err error, more func(error) (string, bool)) string {
	if e, ok := errors.AsType[*recon.ParseError](err); ok && e.Msg != "" {
		return e.Msg
	}
	if e, ok := errors.AsType[recon.PositionError](err); ok {
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

// duplicateKeyError reports a key given twice in a document, at its second occurrence.
func duplicateKeyError(err error, key string, line, col int) error {
	return &docError{err: err, line: line, col: col, msg: fmt.Sprintf("duplicate key %q", key)}
}

// jsonMessage reads an encoding/json error's own text, as [parseMessage]'s more.
func jsonMessage(err error) (string, bool) {
	if e, ok := errors.AsType[*json.SyntaxError](err); ok {
		return e.Error(), true
	}
	if e, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return e.Error(), true
	}
	return "", false
}
