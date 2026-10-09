package shape

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-rotini/rotini"
)

// name is the name every template is parsed under; it begins each error's position, as in
// "template:1:8".
const name = "template"

// Template is a Go text/template that formats a command's output, declared as an input type:
//
//	schema: { type: shape.Template, import: github.com/go-rotini/rotini/shape, placeholder: TEMPLATE }
//
// Its zero value, also what an empty string parses to, is no template; check it with IsZero.
type Template struct {
	src string
	t   *template.Template
}

// UnmarshalText parses text as the template, so a flag of this type rejects a template that
// does not parse when it is read. An empty text gives the zero Template.
func (t *Template) UnmarshalText(text []byte) error {
	src := string(text)
	if src == "" {
		*t = Template{}
		return nil
	}
	parsed, err := template.New(name).Option("missingkey=error").Funcs(funcs).Parse(src)
	if err != nil {
		return trimmed(err)
	}
	*t = Template{src: src, t: parsed}
	return nil
}

// MarshalText returns the template's source.
func (t Template) MarshalText() ([]byte, error) { return []byte(t.src), nil }

// String returns the template's source.
func (t Template) String() string { return t.src }

// IsZero reports whether t holds no template.
func (t Template) IsZero() bool { return t.t == nil }

// Execute runs the template over v's JSON-shaped data (see the package doc) and writes the
// result to w, writing nothing when it fails. An error the template causes, such as naming a
// field v's type does not have, is a usage error ([rotini.ErrUsage]); a value that has no JSON
// form is an error of the program. The zero Template writes nothing.
func (t Template) Execute(w io.Writer, v any) error {
	if t.t == nil {
		return nil
	}
	data, err := toData(reflect.ValueOf(v), 0)
	if err != nil {
		return fmt.Errorf("shape: %w", err)
	}
	var buf bytes.Buffer
	if err := t.t.Execute(&buf, data); err != nil {
		if _, ok := errors.AsType[template.ExecError](err); ok {
			return rotini.UsageError(trimmed(err)) //nolint:wrapcheck // tags the error, keeping its message
		}
		return fmt.Errorf("shape: %w", err)
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// trimmedError drops text/template's "template: " prefix from err's message, keeping err in
// the chain.
type trimmedError struct{ err error }

func trimmed(err error) error { return trimmedError{err} }

func (e trimmedError) Error() string { return strings.TrimPrefix(e.err.Error(), "template: ") }

func (e trimmedError) Unwrap() error { return e.err }

// funcs is the functions a template may call besides text/template's builtins.
var funcs = template.FuncMap{
	"json": jsonFunc,
	"join": joinFunc,
}

// jsonFunc returns v's compact JSON.
func jsonFunc(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err //nolint:wrapcheck // text/template names the call
	}
	return string(b), nil
}

// joinFunc joins list's items with sep: a string as it is, a number or bool as JSON prints it,
// null as nothing, and anything else as its compact JSON.
func joinFunc(sep string, list any) (string, error) {
	rv := reflect.ValueOf(list)
	if !rv.IsValid() {
		return "", nil
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return "", fmt.Errorf("join: %s is not a list", rv.Type())
	}
	parts := make([]string, rv.Len())
	for i := range parts {
		s, err := itemText(rv.Index(i).Interface())
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return strings.Join(parts, sep), nil
}

func itemText(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case json.Number:
		return x.String(), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return jsonFunc(v)
}
