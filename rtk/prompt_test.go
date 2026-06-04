package rtk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func testPrompter(in string) (*Prompter, *bytes.Buffer) {
	var out bytes.Buffer
	return NewPrompter().WithInput(strings.NewReader(in)).WithOutput(&out), &out
}

func TestPrompter_lineValid(t *testing.T) {
	p, out := testPrompter("bad\ngood\n")
	got, err := p.LineValid("Name", func(s string) error {
		if s != "good" {
			return fmt.Errorf("must be good")
		}
		return nil
	})
	if err != nil || got != "good" {
		t.Fatalf("LineValid = %q, %v; want good, nil", got, err)
	}
	if !strings.Contains(out.String(), "must be good") {
		t.Errorf("re-prompt did not show the validator reason: %q", out.String())
	}
	if n := strings.Count(out.String(), "Name:"); n != 2 {
		t.Errorf("asked %d times, want 2 (re-prompt after invalid)", n)
	}
}

func TestPrompter_secretConfirm(t *testing.T) {
	// First pair mismatches (abc/xyz), second pair matches (secret/secret).
	p, out := testPrompter("abc\nxyz\nsecret\nsecret\n")
	got, err := p.SecretConfirm("Password")
	if err != nil || got != "secret" {
		t.Fatalf("SecretConfirm = %q, %v; want secret, nil", got, err)
	}
	if !strings.Contains(out.String(), "do not match") {
		t.Errorf("mismatch was not reported: %q", out.String())
	}
}

func TestPrompter_line(t *testing.T) {
	p, out := testPrompter("alice\n")
	got, err := p.Line("Name")
	if err != nil || got != "alice" {
		t.Fatalf("Line = %q, %v; want alice, nil", got, err)
	}
	if !strings.Contains(out.String(), "Name:") {
		t.Errorf("prompt text = %q, want it to contain %q", out.String(), "Name:")
	}
}

func TestPrompter_lineEOFNoInputErrors(t *testing.T) {
	p, _ := testPrompter("") // immediate EOF, nothing typed
	if _, err := p.Line("Name"); err == nil {
		t.Error("Line on empty input should error (EOF, no answer)")
	}
}

func TestPrompter_lineDefault(t *testing.T) {
	p, _ := testPrompter("\n") // empty line → default
	if got, err := p.LineDefault("Name", "demo"); err != nil || got != "demo" {
		t.Errorf("LineDefault(empty) = %q, %v; want demo, nil", got, err)
	}
	p2, _ := testPrompter("bob\n")
	if got, err := p2.LineDefault("Name", "demo"); err != nil || got != "bob" {
		t.Errorf("LineDefault(bob) = %q, %v; want bob, nil", got, err)
	}
	p3, _ := testPrompter("") // EOF → default
	if got, err := p3.LineDefault("Name", "demo"); err != nil || got != "demo" {
		t.Errorf("LineDefault(EOF) = %q, %v; want demo, nil", got, err)
	}
}

func TestPrompter_confirm(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"y\n", false, true}, {"yes\n", false, true},
		{"n\n", true, false}, {"no\n", true, false},
		{"\n", true, true}, {"\n", false, false},
		{"YES\n", false, true},
	}
	for _, c := range cases {
		p, _ := testPrompter(c.in)
		if got, err := p.Confirm("OK?", c.def); err != nil || got != c.want {
			t.Errorf("Confirm(%q, def=%v) = %v, %v; want %v", c.in, c.def, got, err, c.want)
		}
	}
}

func TestPrompter_confirmReprompts(t *testing.T) {
	p, _ := testPrompter("maybe\ny\n") // invalid then valid
	if got, err := p.Confirm("OK?", false); err != nil || got != true {
		t.Errorf("Confirm(reprompt) = %v, %v; want true (after re-ask)", got, err)
	}
}

func TestPrompter_select(t *testing.T) {
	opts := []string{"yaml", "jsonc", "json"}

	p, _ := testPrompter("2\n")
	if idx, err := p.Select("Format", opts); err != nil || idx != 1 {
		t.Errorf("Select(by number) = %d, %v; want 1", idx, err)
	}

	p2, _ := testPrompter("json\n") // by verbatim text (case-insensitive)
	if idx, err := p2.Select("Format", opts); err != nil || idx != 2 {
		t.Errorf("Select(by text) = %d, %v; want 2", idx, err)
	}

	p3, _ := testPrompter("9\n1\n") // out of range, then valid → re-prompts
	if idx, err := p3.Select("Format", []string{"a", "b"}); err != nil || idx != 0 {
		t.Errorf("Select(reprompt) = %d, %v; want 0", idx, err)
	}
}

func TestPrompter_selectEmptyOptionsErrors(t *testing.T) {
	p, _ := testPrompter("1\n")
	if _, err := p.Select("x", nil); err == nil {
		t.Error("Select with no options should error")
	}
}

func TestPrompter_secretNonTerminalReadsLine(t *testing.T) {
	// A strings.Reader is not a terminal, so Secret falls back to a normal line read.
	p, _ := testPrompter("s3cret\n")
	if got, err := p.Secret("Password"); err != nil || got != "s3cret" {
		t.Errorf("Secret = %q, %v; want s3cret, nil", got, err)
	}
}

// keyReader delivers one pre-split keystroke per Read, mimicking a raw terminal (where
// each keypress arrives as its own read), then EOF.
type keyReader struct {
	chunks [][]byte
	i      int
}

func (k *keyReader) Read(p []byte) (int, error) {
	if k.i >= len(k.chunks) {
		return 0, io.EOF
	}
	n := copy(p, k.chunks[k.i])
	k.i++
	return n, nil
}

func keys(cs ...[]byte) *keyReader { return &keyReader{chunks: cs} }

var (
	kUp    = []byte{0x1b, '[', 'A'}
	kDown  = []byte{0x1b, '[', 'B'}
	kEnter = []byte{'\r'}
	kEsc   = []byte{0x1b}
)

func TestDecodeSelectKey(t *testing.T) {
	cases := []struct {
		in   []byte
		want selectKey
	}{
		{kUp, keyUp}, {kDown, keyDown},
		{[]byte("k"), keyUp}, {[]byte("j"), keyDown},
		{[]byte("\r"), keyEnter}, {[]byte("\n"), keyEnter},
		{[]byte{0x03}, keyCancel}, {[]byte("q"), keyCancel}, {kEsc, keyCancel},
		{[]byte("x"), keyNone}, {nil, keyNone},
		{[]byte{0x1b, '[', 'C'}, keyNone}, // right arrow → ignored
	}
	for _, c := range cases {
		if got := decodeSelectKey(c.in); got != c.want {
			t.Errorf("decodeSelectKey(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPrompter_selectLoop(t *testing.T) {
	opts := []string{"yaml", "jsonc", "json"}
	cases := []struct {
		name string
		in   *keyReader
		want int
		err  error
	}{
		{"enter at first", keys(kEnter), 0, nil},
		{"down then enter", keys(kDown, kEnter), 1, nil},
		{"down twice", keys(kDown, kDown, kEnter), 2, nil},
		{"down wraps to first", keys(kDown, kDown, kDown, kEnter), 0, nil},
		{"up wraps to last", keys(kUp, kEnter), 2, nil},
		{"vim j/k", keys([]byte("j"), []byte("j"), []byte("k"), kEnter), 1, nil},
		{"esc cancels", keys(kEsc), 0, ErrCanceled},
		{"ctrl-c cancels", keys([]byte{0x03}), 0, ErrCanceled},
		{"eof cancels", keys(), 0, ErrCanceled},
		{"unknown key ignored then enter", keys([]byte("x"), kEnter), 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := testPrompter("")
			got, err := p.selectLoop(c.in, "Format", opts)
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("selectLoop = %d, %v; want %d, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestPrompter_selectLoop_rendersAndRestoresCursor(t *testing.T) {
	p, out := testPrompter("")
	if _, err := p.selectLoop(keys(kDown, kEnter), "Pick", []string{"a", "b"}); err != nil {
		t.Fatalf("selectLoop: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, HideCursor) || !strings.Contains(s, ShowCursor) {
		t.Errorf("cursor should be hidden then restored: %q", s)
	}
	if !strings.Contains(s, "Pick") {
		t.Errorf("question not rendered: %q", s)
	}
	if !strings.Contains(s, "> b") { // after one Down, row "b" is marked
		t.Errorf("selection marker not on the highlighted row: %q", s)
	}
}

// SelectArrow degrades to the line-based Select when input is not a terminal (a
// strings.Reader here), so scripted/piped runs behave exactly like Select.
func TestPrompter_SelectArrow_fallback(t *testing.T) {
	p, out := testPrompter("2\n")
	got, err := p.SelectArrow("Format", []string{"yaml", "jsonc", "json"})
	if err != nil || got != 1 {
		t.Fatalf("SelectArrow fallback = %d, %v; want 1, nil", got, err)
	}
	if !strings.Contains(out.String(), "1) yaml") {
		t.Errorf("fallback did not render the numbered list: %q", out.String())
	}
}

func TestPrompter_SelectArrow_emptyErrors(t *testing.T) {
	p, _ := testPrompter("")
	if _, err := p.SelectArrow("x", nil); err == nil {
		t.Error("SelectArrow with no options should error")
	}
}
