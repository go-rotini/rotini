package rotinitest_test

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/go-rotini/rotini/rotinitest"
)

// decodeKV decodes `key = value` lines, a value of `null` as nil, and fails on a key given
// twice or a `---` line starting a second document.
func decodeKV(data []byte) (any, error) {
	out := map[string]any{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == "---" {
			return nil, errors.New("a second document")
		}
		k, v, _ := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if _, dup := out[k]; dup {
			return nil, errors.New("duplicate key " + k)
		}
		if v == "null" {
			out[k] = nil
		} else {
			out[k] = v
		}
	}
	return out, nil
}

var kvDocs = rotinitest.CodecDocuments{
	Valid:        [][]byte{[]byte("a = 1\nb = two\n")},
	DuplicateKey: []byte("a = 1\na = 2\n"),
	TwoDocuments: []byte("a = 1\n---\nb = 2\n"),
	Null:         []byte("key = null\n"),
}

func TestCheckCodec_passes(t *testing.T) {
	rotinitest.CheckCodec(t, decodeKV, kvDocs)
	rotinitest.CheckCodec(t, decodeKV, rotinitest.CodecDocuments{}) // nothing to check
}

func TestCheckCodec_failures(t *testing.T) {
	tests := []struct {
		name   string
		decode func([]byte) (any, error)
		want   string
	}{
		{"keeps the last of a duplicate", func(data []byte) (any, error) {
			if bytes.Contains(data, []byte("a = 2")) {
				return map[string]any{"a": "2"}, nil
			}
			return decodeKV(data)
		}, "DuplicateKey: decoded to"},
		{"reads only the first document", func(data []byte) (any, error) {
			first, _, _ := bytes.Cut(data, []byte("---"))
			return decodeKV(first)
		}, "TwoDocuments: decoded to"},
		{"null as text", func(data []byte) (any, error) {
			v, err := decodeKV(bytes.ReplaceAll(data, []byte("null"), []byte("'null'")))
			return v, err
		}, `Null: "key" decoded to "'null'" (string); null must decode as nil`},
		{"typed list", func(data []byte) (any, error) {
			v, err := decodeKV(data)
			if m, ok := v.(map[string]any); ok && m["b"] != nil {
				m["b"] = []string{"two"}
			}
			return v, err
		}, "Valid[0]: b is a []string; decode must return map[string]any, []any, strings, bools, numbers, times and nil"},
		{"a list at the top", func([]byte) (any, error) { return []any{}, nil }, "Valid[0]: decoded to a []interface {}; a document's top level must be a map[string]any"},
		{"fails", func([]byte) (any, error) { return nil, errors.New("boom") }, "Null: decode failed: boom"},
		{"keeps its input", func(data []byte) (any, error) {
			return map[string]any{"a": unsafe.String(&data[0], len(data))}, nil
		}, "Valid[0]: the decoded tree changed when its input was overwritten; decode must copy what it keeps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := kvDocs
			if strings.HasPrefix(tt.want, "Valid") {
				docs = rotinitest.CodecDocuments{Valid: kvDocs.Valid} // the fake keeps the last failure only
			}
			got := failure(t, func(tb testing.TB) { rotinitest.CheckCodec(tb, tt.decode, docs) })
			if !strings.Contains(got, tt.want) {
				t.Errorf("failure = %q\nwant it to contain %q", got, tt.want)
			}
		})
	}
	if got := failure(t, func(tb testing.TB) { rotinitest.CheckCodec(tb, nil, kvDocs) }); got != "rotinitest.CheckCodec: nil decode" {
		t.Errorf("nil decode: %q", got)
	}
}
