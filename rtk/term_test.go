package rtk_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// NewTerm — TTY detection
// =============================================================================

func TestNewTerm_nilWriterIsNotTTY(t *testing.T) {
	t.Parallel()
	tm := rtk.NewTerm(nil)
	if tm.IsTTY() {
		t.Errorf("nil writer: IsTTY=true, want false")
	}
	if tm.ColorLevel() != rtk.ColorNone {
		t.Errorf("nil writer: ColorLevel=%d, want %d", tm.ColorLevel(), rtk.ColorNone)
	}
}

func TestNewTerm_bufferIsNotTTY(t *testing.T) {
	t.Parallel()
	tm := rtk.NewTerm(&bytes.Buffer{})
	if tm.IsTTY() {
		t.Errorf("bytes.Buffer: IsTTY=true, want false")
	}
	if tm.ColorLevel() != rtk.ColorNone {
		t.Errorf("bytes.Buffer: ColorLevel=%d, want %d", tm.ColorLevel(), rtk.ColorNone)
	}
}

func TestNewTerm_discardIsNotTTY(t *testing.T) {
	t.Parallel()
	tm := rtk.NewTerm(io.Discard)
	if tm.IsTTY() {
		t.Errorf("io.Discard: IsTTY=true, want false")
	}
}

// =============================================================================
// Style helpers — color emit / suppress
// =============================================================================

// fakeTerm is a Term implementation tests use to inject specific
// ColorLevel values without depending on the process environment.
type fakeTerm struct {
	tty   bool
	level rtk.ColorLevel
}

func (f fakeTerm) IsTTY() bool                { return f.tty }
func (f fakeTerm) ColorLevel() rtk.ColorLevel { return f.level }

func TestStyle_passThroughOnColorNone(t *testing.T) {
	t.Parallel()
	tm := fakeTerm{tty: false, level: rtk.ColorNone}
	for _, fn := range []func(rtk.Term, string) string{
		rtk.Red, rtk.Yellow, rtk.Green, rtk.Bold,
	} {
		got := fn(tm, "hello")
		if got != "hello" {
			t.Errorf("ColorNone helper output: got %q, want %q (plain pass-through)", got, "hello")
		}
	}
}

func TestStyle_emitsANSIWhenColorOn(t *testing.T) {
	t.Parallel()
	tm := fakeTerm{tty: true, level: rtk.Color16}
	tests := []struct {
		name string
		fn   func(rtk.Term, string) string
		want string
	}{
		{"red", rtk.Red, "\x1b[31mhello\x1b[0m"},
		{"yellow", rtk.Yellow, "\x1b[33mhello\x1b[0m"},
		{"green", rtk.Green, "\x1b[32mhello\x1b[0m"},
		{"bold", rtk.Bold, "\x1b[1mhello\x1b[0m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.fn(tm, "hello")
			if got != tc.want {
				t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestStyle_nilTermPassThrough(t *testing.T) {
	t.Parallel()
	got := rtk.Red(nil, "x")
	if got != "x" {
		t.Errorf("nil Term: got %q, want %q (no panic, plain pass-through)", got, "x")
	}
}

// =============================================================================
// ColorLevel detection rule table
// =============================================================================

// TestColorLevel_ruleTable exercises every branch of the unexported
// detectColorLevel function. We hit it directly (via export_test.go)
// because the positive-color cases require an isTTY=true input and
// fabricating a real *os.File TTY inside a test is hostile to CI.
func TestColorLevel_ruleTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		isTTY     bool
		noColor   string
		term      string
		colorterm string
		want      rtk.ColorLevel
	}{
		{"no-color set wins over color env", true, "1", "xterm-256color", "truecolor", rtk.ColorNone},
		{"no-color any value", true, "anything", "xterm-256color", "", rtk.ColorNone},
		{"non-tty short-circuits", false, "", "xterm-256color", "truecolor", rtk.ColorNone},
		{"empty term", true, "", "", "", rtk.ColorNone},
		{"dumb term", true, "", "dumb", "", rtk.ColorNone},
		{"truecolor via colorterm", true, "", "xterm-256color", "truecolor", rtk.Color16M},
		{"24bit via colorterm", true, "", "xterm-256color", "24bit", rtk.Color16M},
		{"colorterm case-insensitive", true, "", "xterm-256color", "TrueColor", rtk.Color16M},
		{"256color via term", true, "", "xterm-256color", "", rtk.Color256},
		{"basic 16-color", true, "", "xterm", "", rtk.Color16},
		{"basic 16-color screen", true, "", "screen", "", rtk.Color16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rtk.DetectColorLevelForTest(tc.isTTY, tc.noColor, tc.term, tc.colorterm)
			if got != tc.want {
				t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestColorLevel_nonTTYAlwaysNone collapses the env-rule table for
// the common case rotini hits at runtime: a non-TTY writer (handler
// piping stderr to a file, CI environment). Every env permutation
// must yield ColorNone.
func TestColorLevel_nonTTYAlwaysNone(t *testing.T) {
	envs := []map[string]string{
		{"TERM": "xterm-256color", "COLORTERM": "truecolor"},
		{"TERM": "xterm-256color"},
		{"TERM": "xterm"},
		{},
	}
	for _, env := range envs {
		t.Run(strings.Join(asKV(env), "_"), func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			tm := rtk.NewTerm(&bytes.Buffer{})
			if tm.ColorLevel() != rtk.ColorNone {
				t.Errorf("non-TTY writer should always force ColorNone; got %d", tm.ColorLevel())
			}
		})
	}
}

// asKV stringifies a map for sub-test naming.
func asKV(m map[string]string) []string {
	if len(m) == 0 {
		return []string{"empty"}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v == "" {
			out = append(out, k+"=∅")
		} else {
			out = append(out, k+"="+v)
		}
	}
	return out
}
