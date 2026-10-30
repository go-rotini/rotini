package codegen

import "testing"

// TestRoffCode pins how man pages set markdown code spans: bold, without the backticks, with a
// fence longer than any backtick run inside, and a stray backtick kept literal.
func TestRoffCode(t *testing.T) {
	for in, want := range map[string]string{
		"matches `^[a-z]+$`":         `matches \fB^[a\-z]+$\fR`,
		"matches ``^[a-z`\"]+$``":    `matches \fB^[a\-z\(ga"]+$\fR`,
		"matches `` `x` ``":          `matches \fB\(gax\(ga\fR`,
		"a ` alone":                  `a \(ga alone`,
		"``unclosed` then `code`":    `\(ga\(gaunclosed\fBthen\fRcode\(ga`, // as CommonMark pairs them
		"`.x` first; then `-v`":      `\fB.x\fR first; then \fB\-v\fR`,
		".dot start":                 `\&.dot start`,
		"no spans, a \\ and a - too": `no spans, a \e and a \- too`,
	} {
		if got := roffCode(in); got != want {
			t.Errorf("roffCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStructuredDefault pins the JSON form man and markdown give a list or map default, and
// that a scalar, default_text or secret default has none.
func TestStructuredDefault(t *testing.T) {
	for _, tc := range []struct {
		schema *InputSchema
		want   string
	}{
		{&InputSchema{Default: []any{"a,b", `c"d`}}, `["a,b", "c\"d"]`},
		{&InputSchema{Default: []any{"eu", "us"}}, `["eu", "us"]`},
		{&InputSchema{Default: []any{"<x>", "a b"}}, `["<x>", "a b"]`},
		{&InputSchema{Default: map[string]any{"b": "2", "a": "1"}}, `{"a": "1", "b": "2"}`},
		{&InputSchema{Default: "a,b"}, ""},
		{&InputSchema{Default: []any{"x"}, DefaultText: "the usual"}, ""},
		{&InputSchema{Default: []any{"x"}, Secret: true}, ""},
		{nil, ""},
	} {
		if got := structuredDefault(tc.schema); got != tc.want {
			t.Errorf("structuredDefault(%+v) = %q, want %q", tc.schema, got, tc.want)
		}
	}
}

// TestCompletionHint_pathTypes pins the completion kind a path type implies when `complete:`
// declares none, and that a declared kind, or fixed values, win.
func TestCompletionHint_pathTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema *InputSchema
		kind   string
		msg    string
	}{
		{"existingfile", &InputSchema{Type: "existingfile"}, "file", ""},
		{"inputfile", &InputSchema{Type: "inputfile"}, "file", ""},
		{"outputfile", &InputSchema{Type: "outputfile"}, "file", ""},
		{"existingdir", &InputSchema{Type: "existingdir"}, "directory", ""},
		{"list", &InputSchema{Type: "[]existingfile"}, "file", ""},
		{"message kept", &InputSchema{Type: "existingdir", Complete: &CompletionHint{Message: "a dir"}}, "directory", "a dir"},
		{"declared wins", &InputSchema{Type: "existingfile", Complete: &CompletionHint{Kind: "none"}}, "none", ""},
		{"enum wins", &InputSchema{Type: "existingfile", Enum: []any{"a.txt"}}, "", ""},
		{"string", &InputSchema{Type: "string"}, "", ""},
	} {
		h := completionHint(tc.schema)
		kind, msg := "", ""
		if h != nil {
			kind, msg = h.Kind, h.Message
		}
		if kind != tc.kind || msg != tc.msg {
			t.Errorf("%s: hint = %q %q, want %q %q", tc.name, kind, msg, tc.kind, tc.msg)
		}
	}
	if s := (&InputSchema{Type: "existingfile"}); completionHint(s) == nil || s.Complete != nil {
		t.Error("the implied hint changed the spec's schema")
	}
}
