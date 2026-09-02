package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func ask[T any](t *testing.T, input string, fn func(in *strings.Reader, out *bytes.Buffer) (T, error)) (T, string, error) {
	t.Helper()
	in, out := strings.NewReader(input), &bytes.Buffer{}
	v, err := fn(in, out)
	return v, out.String(), err
}

func TestPrompt_readsAnswer(t *testing.T) {
	got, out, err := ask(t, "alpha\n", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithLabel("name").Ask(context.Background())
	})
	if err != nil || got != "alpha" {
		t.Fatalf("Ask = (%q, %v), want (\"alpha\", nil)", got, err)
	}
	if !strings.Contains(out, "name") {
		t.Errorf("label not written: %q", out)
	}
}

func TestPrompt_emptyAnswerTakesDefault(t *testing.T) {
	got, out, err := ask(t, "\n", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithLabel("name").WithDefault("fallback").Ask(context.Background())
	})
	if err != nil || got != "fallback" {
		t.Fatalf("Ask = (%q, %v), want the default", got, err)
	}
	if !strings.Contains(out, "[fallback]") {
		t.Errorf("default not shown in the label: %q", out)
	}
}

// THE contract that makes an interactive battery safe in CI: input that ends
// without an answer fails immediately instead of blocking on a stdin nobody is
// typing into.
func TestPrompt_notInteractiveRatherThanHanging(t *testing.T) {
	_, _, err := ask(t, "", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithLabel("name").Ask(context.Background())
	})
	if !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("Ask on empty input = %v, want ErrNotInteractive", err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("ErrNotInteractive must categorize as a usage error — the environment is wrong, not the program")
	}
}

// A default IS the non-interactive answer, so a prompt that has one never fails
// for lack of a human.
func TestPrompt_defaultSurvivesNonInteractive(t *testing.T) {
	got, _, err := ask(t, "", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithDefault("d").Ask(context.Background())
	})
	if err != nil || got != "d" {
		t.Fatalf("Ask = (%q, %v), want the default with no error", got, err)
	}
}

// A piped answer is a legitimate scripted use, not a degraded one.
func TestPrompt_worksFromAPipe(t *testing.T) {
	got, _, err := ask(t, "piped\n", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).Ask(context.Background())
	})
	if err != nil || got != "piped" {
		t.Fatalf("Ask = (%q, %v), want the piped value", got, err)
	}
}

func TestPrompt_validationRetriesThenFails(t *testing.T) {
	reject := func(s string) error {
		if s == "good" {
			return nil
		}
		return fmt.Errorf("%q is not good", s)
	}
	got, out, err := ask(t, "bad\ngood\n", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithValidate(reject).WithRetries(1).Ask(context.Background())
	})
	if err != nil || got != "good" {
		t.Fatalf("Ask = (%q, %v), want the retried answer", got, err)
	}
	if !strings.Contains(out, "is not good") {
		t.Errorf("validation message not shown: %q", out)
	}

	_, _, err = ask(t, "bad\nbad\n", func(in *strings.Reader, out *bytes.Buffer) (string, error) {
		return NewPrompt(in, out).WithValidate(reject).WithRetries(0).Ask(context.Background())
	})
	if !errors.Is(err, ErrPromptInvalid) {
		t.Fatalf("exhausted retries = %v, want ErrPromptInvalid", err)
	}
	if !strings.Contains(err.Error(), "is not good") {
		t.Errorf("the validation cause is not carried through the error: %v", err)
	}
}

// A canceled context returns promptly rather than waiting for a keystroke.
func TestPrompt_respectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewPrompt(blockingReader{}, &bytes.Buffer{}).Ask(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Ask on a canceled context = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return on a canceled context")
	}
}

// blockingReader never returns, standing in for a terminal nobody types into.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestConfirm(t *testing.T) {
	cases := []struct {
		input string
		def   *bool
		want  bool
	}{
		{input: "y\n", want: true},
		{input: "YES\n", want: true},
		{input: "n\n", want: false},
		{input: "\n", def: Ptr(true), want: true},
		{input: "", def: Ptr(false), want: false}, // non-interactive falls to the default
	}
	for _, tc := range cases {
		got, _, err := ask(t, tc.input, func(in *strings.Reader, out *bytes.Buffer) (bool, error) {
			c := NewConfirm(in, out).WithLabel("proceed?")
			if tc.def != nil {
				c.WithDefault(*tc.def)
			}
			return c.Ask(context.Background())
		})
		if err != nil || got != tc.want {
			t.Errorf("Confirm(%q) = (%v, %v), want %v", tc.input, got, err, tc.want)
		}
	}
}

func TestConfirm_showsDefaultInChoices(t *testing.T) {
	_, out, _ := ask(t, "\n", func(in *strings.Reader, out *bytes.Buffer) (bool, error) {
		return NewConfirm(in, out).WithLabel("go?").WithDefault(true).Ask(context.Background())
	})
	if !strings.Contains(out, "[Y/n]") {
		t.Errorf("default not capitalized in the choices: %q", out)
	}
}

func TestConfirm_customTokens(t *testing.T) {
	got, _, err := ask(t, "oui\n", func(in *strings.Reader, out *bytes.Buffer) (bool, error) {
		return NewConfirm(in, out).WithAffirmative("oui").WithNegative("non").Ask(context.Background())
	})
	if err != nil || !got {
		t.Errorf("custom affirmative = (%v, %v), want true", got, err)
	}
}

func TestConfirm_unrecognizedExhaustsRetries(t *testing.T) {
	_, _, err := ask(t, "maybe\nperhaps\nwho knows\n", func(in *strings.Reader, out *bytes.Buffer) (bool, error) {
		return NewConfirm(in, out).WithRetries(1).Ask(context.Background())
	})
	if !errors.Is(err, ErrPromptInvalid) {
		t.Errorf("unrecognized answers = %v, want ErrPromptInvalid", err)
	}
}

func TestSelect(t *testing.T) {
	opts := []string{"alpha", "beta", "gamma"}
	cases := []struct {
		name, input string
		wantIdx     int
	}{
		{"by number", "2\n", 1},
		{"by exact text", "gamma\n", 2},
		{"case-insensitive text", "ALPHA\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, out := strings.NewReader(tc.input), &bytes.Buffer{}
			idx, text, err := NewSelect(in, out, opts...).WithLabel("pick").Ask(context.Background())
			if err != nil || idx != tc.wantIdx || text != opts[tc.wantIdx] {
				t.Errorf("Ask = (%d, %q, %v), want index %d", idx, text, err, tc.wantIdx)
			}
			if !strings.Contains(out.String(), "1) alpha") {
				t.Errorf("menu not numbered: %q", out.String())
			}
		})
	}
}

// The Suggestor is what makes a typo'd option recoverable — and it stays opt-in,
// so without one a near-miss is simply rejected.
func TestSelect_fuzzyMatchIsOptIn(t *testing.T) {
	opts := []string{"alpha", "beta"}
	in, out := strings.NewReader("alpah\n"), &bytes.Buffer{}
	idx, _, err := NewSelect(in, out, opts...).WithSuggestor(NewSuggestor()).WithRetries(0).Ask(context.Background())
	if err != nil || idx != 0 {
		t.Errorf("fuzzy Ask = (%d, %v), want index 0", idx, err)
	}

	in2, out2 := strings.NewReader("alpah\n"), &bytes.Buffer{}
	if _, _, err := NewSelect(in2, out2, opts...).WithRetries(0).Ask(context.Background()); !errors.Is(err, ErrPromptInvalid) {
		t.Errorf("without a Suggestor a near-miss must be rejected, got %v", err)
	}
}

// A number outside the menu is a mistake, not a label to fuzzy-match.
func TestSelect_numberOutsideMenuIsRejected(t *testing.T) {
	in, out := strings.NewReader("99\n"), &bytes.Buffer{}
	if _, _, err := NewSelect(in, out, "a", "b").WithRetries(0).Ask(context.Background()); !errors.Is(err, ErrPromptInvalid) {
		t.Errorf("out-of-range number = %v, want ErrPromptInvalid", err)
	}
}

func TestSelect_defaultAndNonInteractive(t *testing.T) {
	in, out := strings.NewReader(""), &bytes.Buffer{}
	idx, text, err := NewSelect(in, out, "a", "b").WithDefault(1).Ask(context.Background())
	if err != nil || idx != 1 || text != "b" {
		t.Errorf("non-interactive with a default = (%d, %q, %v), want (1, \"b\", nil)", idx, text, err)
	}

	in2, out2 := strings.NewReader(""), &bytes.Buffer{}
	if _, _, err := NewSelect(in2, out2, "a", "b").Ask(context.Background()); !errors.Is(err, ErrNotInteractive) {
		t.Errorf("non-interactive without a default = %v, want ErrNotInteractive", err)
	}
}

func TestSelect_noOptionsIsUsageError(t *testing.T) {
	if _, _, err := NewSelect(strings.NewReader(""), &bytes.Buffer{}).Ask(context.Background()); !errors.Is(err, ErrUsage) {
		t.Errorf("empty Select = %v, want a usage error", err)
	}
}
