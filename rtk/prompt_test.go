package rtk

import (
	"bytes"
	"fmt"
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
