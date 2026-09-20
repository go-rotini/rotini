package rotini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every exported symbol is frozen at v1, so every exported symbol has to have been executed
// at least once before the freeze. This file closes the members the rest of the suite reached
// past — option setters, the no-op hook defaults, the Error() methods whose text no test had
// ever seen, and the terminal-detection seam. They are small on purpose: the point is that
// none of them is shipped untried, not that each needs an elaborate scenario.

// ── detection (detect.go) ───────────────────────────────────────────────────
//
// All three read the environment, which is why they had no test: they are the seam a program
// uses to decide about color, and rotini never calls them itself.

func TestEnvNoColor(t *testing.T) {
	cases := []struct {
		name                 string
		noColor, clicolorFrc string
		set                  []string // which vars to set at all
		want                 bool
	}{
		{name: "neither set", want: false},
		{name: "NO_COLOR set", noColor: "1", set: []string{"NO_COLOR"}, want: true},
		{name: "NO_COLOR empty is not set", noColor: "", set: []string{"NO_COLOR"}, want: false},
		{name: "CLICOLOR_FORCE overrides NO_COLOR", noColor: "1", clicolorFrc: "1",
			set: []string{"NO_COLOR", "CLICOLOR_FORCE"}, want: false},
		{name: `CLICOLOR_FORCE="0" does not override`, noColor: "1", clicolorFrc: "0",
			set: []string{"NO_COLOR", "CLICOLOR_FORCE"}, want: true},
		{name: "CLICOLOR_FORCE alone", clicolorFrc: "1", set: []string{"CLICOLOR_FORCE"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			os.Unsetenv("NO_COLOR")
			os.Unsetenv("CLICOLOR_FORCE")
			for _, k := range tc.set {
				switch k {
				case "NO_COLOR":
					t.Setenv(k, tc.noColor)
				case "CLICOLOR_FORCE":
					t.Setenv(k, tc.clicolorFrc)
				}
			}
			if got := EnvNoColor(); got != tc.want {
				t.Errorf("EnvNoColor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectProfile(t *testing.T) {
	cases := []struct {
		name            string
		colorterm, term string
		noColor         string
		want            Profile
		wantNote        string
	}{
		{name: "COLORTERM=truecolor", colorterm: "truecolor", want: ProfileTrueColor},
		{name: "COLORTERM=24bit", colorterm: "24bit", want: ProfileTrueColor},
		{name: "TERM carries truecolor", term: "xterm-truecolor", want: ProfileTrueColor},
		{name: "TERM carries 256color", term: "xterm-256color", want: ProfileANSI256},
		{name: "TERM=dumb", term: "dumb", want: ProfileNoColor},
		{name: "TERM unset", term: "", want: ProfileNoColor},
		{name: "any other TERM", term: "xterm", want: ProfileANSI16},
		{name: "NO_COLOR wins over everything", colorterm: "truecolor", term: "xterm-256color",
			noColor: "1", want: ProfileNoColor,
			wantNote: "a consumer asking for no color is not overridden by capability"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COLORTERM", tc.colorterm)
			t.Setenv("TERM", tc.term)
			t.Setenv("CLICOLOR_FORCE", "")
			os.Unsetenv("CLICOLOR_FORCE")
			if tc.noColor != "" {
				t.Setenv("NO_COLOR", tc.noColor)
			} else {
				t.Setenv("NO_COLOR", "")
				os.Unsetenv("NO_COLOR")
			}
			if got := DetectProfile(); got != tc.want {
				t.Errorf("DetectProfile() = %v, want %v %s", got, tc.want, tc.wantNote)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(nil) {
		t.Error("IsTerminal(nil) = true, want false")
	}

	// A regular file is not a terminal — the case that matters, since it is what a
	// redirected run looks like.
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("a regular file reported as a terminal")
	}

	// A closed file cannot be stat'd; that is not a terminal either, and must not panic.
	closed, err := os.Open(filepath.Join(t.TempDir(), ".."))
	if err == nil {
		closed.Close()
		if IsTerminal(closed) {
			t.Error("a closed file reported as a terminal")
		}
	}

	// /dev/null IS a character device, so it reports true — worth pinning, because it is
	// the one "not really a terminal" that passes the check, and callers should know.
	if devNull, err := os.Open(os.DevNull); err == nil {
		defer devNull.Close()
		if !IsTerminal(devNull) {
			t.Errorf("%s is a character device; IsTerminal should report true", os.DevNull)
		}
	}
}

// ── text (text.go) ──────────────────────────────────────────────────────────

func TestHyperlink(t *testing.T) {
	got := Hyperlink("https://example.com", "docs")
	if !strings.Contains(got, "https://example.com") || !strings.Contains(got, "docs") {
		t.Errorf("Hyperlink lost the url or the text: %q", got)
	}
	// A terminal that does not support OSC 8 shows the text and swallows the sequence, so
	// the displayed width must be the text's width and nothing more.
	if w := Width(got); w != Width("docs") {
		t.Errorf("Hyperlink display width = %d, want %d — the escape must not be counted", w, Width("docs"))
	}
}

// ── handler defaults (handlers.go) ──────────────────────────────────────────

// TestDefaultHooksAreNoOps executes the four embeddable defaults. Every generated stub embeds
// them, so "does nothing, safely, with a nil context" is a real contract.
func TestDefaultHooksAreNoOps(t *testing.T) {
	var h struct {
		DefaultCascadingPreRun
		DefaultPreRun
		DefaultPostRun
		DefaultCascadingPostRun
	}
	ctx, rtx := context.Background(), newContext()
	h.CascadingPreRun(ctx, rtx)
	h.PreRun(ctx, rtx)
	h.PostRun(ctx, rtx)
	h.CascadingPostRun(ctx, rtx)

	// A no-op must not have recorded anything, or an "empty" handler would not settle
	// silently.
	if !(Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}).Empty() {
		t.Error("a default hook recorded something")
	}

	// They also have to be callable with a nil context — the defaults are reached through
	// an interface a user may drive directly in a test.
	h.PreRun(ctx, nil)
	h.PostRun(ctx, nil)
}

// ── error strings never before rendered ─────────────────────────────────────
//
// An Error() with no test is a message no one has read. For a framework whose pitch includes
// actionable, non-leaky messages, these are the wrong place to have a gap.

func TestErrorStrings(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		parts []string
	}{
		{
			name:  "Deprecation",
			err:   Deprecation{Kind: "flag", Name: "conf", Identifier: "--conf"},
			parts: []string{"deprecated", "flag", "--conf"},
		},
		{
			name:  "RPCError",
			err:   NewRPCError(CodeInvalidParams, "missing field"),
			parts: []string{"jsonrpc", "-32602", "missing field"},
		},
		{
			name:  "ExitCode cancellation cause",
			err:   ExitCode(3),
			parts: []string{"rotini", "3"},
		},
		{
			name:  "ServiceError",
			err:   &ServiceError{Key: "store"},
			parts: []string{"rotini", "store"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, p := range tc.parts {
				if !strings.Contains(msg, p) {
					t.Errorf("%s message %q does not name %q", tc.name, msg, p)
				}
			}
			if strings.TrimSpace(msg) == "" {
				t.Errorf("%s renders empty", tc.name)
			}
		})
	}
}

func TestRemoteErrorKind_String(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []RemoteErrorKind{RemoteBinaryNotFound, RemoteTimeout, RemoteSpawnFailed} {
		s := k.String()
		if s == "" {
			t.Errorf("kind %d renders empty", k)
		}
		if seen[s] {
			t.Errorf("kind %d renders %q, already used by another kind", k, s)
		}
		seen[s] = true
	}
}

// ── option setters ──────────────────────────────────────────────────────────

// TestProgram_WithStdin is the seam doc.go and Context's doc comment both tell testers to
// use to drive a handler, and nothing exercised it.
func TestProgram_WithStdin(t *testing.T) {
	var got string
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(rtx.Stdin)
		got = buf.String()
	}}
	p, _, _ := newTestProgram(h, []string{"run"})
	p.WithStdin(strings.NewReader("piped payload"))

	if _, err := p.Run(p.args); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "piped payload" {
		t.Errorf("handler read %q from rtx.Stdin, want the reader WithStdin supplied", got)
	}
}

func TestSubprocess_WithStdin(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("no /bin/cat on this platform")
	}
	out, err := NewSubprocess("/bin/cat").
		WithStdin(strings.NewReader("through the child")).
		Output(context.Background())
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if strings.TrimSpace(out) != "through the child" {
		t.Errorf("child echoed %q, want the stdin WithStdin supplied", out)
	}
}

func TestPrinter_WithIndent(t *testing.T) {
	type row struct {
		Name string `json:"name"`
	}
	value := row{Name: "x"}

	compact := new(bytes.Buffer)
	if err := NewPrinter(compact).WithFormat(FormatJSON).WithIndent("").Print(value); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact.String(), "\n  ") {
		t.Errorf("an empty indent must emit compact JSON, got:\n%s", compact)
	}

	wide := new(bytes.Buffer)
	if err := NewPrinter(wide).WithFormat(FormatJSON).WithIndent("\t").Print(value); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wide.String(), "\t\"name\"") {
		t.Errorf("indent \\t not applied, got:\n%s", wide)
	}
}

func TestTable_WithPaddingAndHeaderStyle(t *testing.T) {
	build := func(pad int) string {
		return NewTable("a", "bb").
			WithPadding(pad).
			Row("1", "2").
			Render()
	}
	narrow, wide := build(1), build(6)
	if Width(strings.Split(wide, "\n")[0]) <= Width(strings.Split(narrow, "\n")[0]) {
		t.Errorf("WithPadding(6) did not widen the header line:\n%q\nvs\n%q", wide, narrow)
	}

	// A negative padding is ignored rather than corrupting the layout.
	if build(-1) != build(2) {
		t.Error("a negative padding should be ignored, leaving the default")
	}

	// WithHeaderStyle names the Styler key the header renders through.
	styler := NewStyler()
	styler.Define("banner").Bold()
	styled := NewTable("a").
		WithStyler(styler).
		WithHeaderStyle("banner").
		Row("1").
		Render()
	if !strings.Contains(styled, "\x1b[1m") {
		t.Errorf("header did not render through the named style:\n%q", styled)
	}
}

// ── Styler / Style convenience wrappers ─────────────────────────────────────
//
// Thin wrappers over Render/Sprint, but each is a separate exported symbol, and a
// copy-paste slip in one of six near-identical bodies is exactly what nothing would catch.

func TestStyler_printFamily(t *testing.T) {
	s := NewStyler()
	s.Define("k").Bold()

	if got := s.Renderf("k", "n=%d", 7); !strings.Contains(got, "n=7") || !strings.Contains(got, "\x1b[1m") {
		t.Errorf("Renderf = %q, want the formatted text, styled", got)
	}

	for _, tc := range []struct {
		name    string
		call    func(w *bytes.Buffer)
		want    string
		newline bool
	}{
		{"Fprint", func(w *bytes.Buffer) { s.Fprint(w, "k", "a", "b") }, "ab", false},
		{"Fprintln", func(w *bytes.Buffer) { s.Fprintln(w, "k", "a") }, "a", true},
		{"Fprintf", func(w *bytes.Buffer) { s.Fprintf(w, "k", "%s-%d", "a", 1) }, "a-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := new(bytes.Buffer)
			tc.call(w)
			if !strings.Contains(w.String(), tc.want) {
				t.Errorf("%s wrote %q, want it to contain %q", tc.name, w, tc.want)
			}
			if !strings.Contains(w.String(), "\x1b[1m") {
				t.Errorf("%s wrote %q unstyled", tc.name, w)
			}
			if got := strings.HasSuffix(w.String(), "\n"); got != tc.newline {
				t.Errorf("%s newline = %v, want %v", tc.name, got, tc.newline)
			}
		})
	}
}

func TestStyle_printFamily(t *testing.T) {
	st := NewStyle().Bold()
	for _, tc := range []struct {
		name    string
		call    func(w *bytes.Buffer)
		want    string
		newline bool
	}{
		{"Fprint", func(w *bytes.Buffer) { st.Fprint(w, "a", "b") }, "ab", false},
		{"Fprintln", func(w *bytes.Buffer) { st.Fprintln(w, "a") }, "a", true},
		{"Fprintf", func(w *bytes.Buffer) { st.Fprintf(w, "%s-%d", "a", 1) }, "a-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := new(bytes.Buffer)
			tc.call(w)
			if !strings.Contains(w.String(), tc.want) {
				t.Errorf("%s wrote %q, want it to contain %q", tc.name, w, tc.want)
			}
			if got := strings.HasSuffix(w.String(), "\n"); got != tc.newline {
				t.Errorf("%s newline = %v, want %v", tc.name, got, tc.newline)
			}
		})
	}
}

// TestStyle_rareAttributes covers the two SGR attributes nothing else reaches. They are
// separate codes (21 and 53), so a transposition would be invisible without this.
func TestStyle_rareAttributes(t *testing.T) {
	if got := NewStyle().DoubleUnderline().Sprint("x"); !strings.Contains(got, "\x1b[21m") {
		t.Errorf("DoubleUnderline = %q, want SGR 21", got)
	}
	if got := NewStyle().Overline().Sprint("x"); !strings.Contains(got, "\x1b[53m") {
		t.Errorf("Overline = %q, want SGR 53", got)
	}
}

// ── indicator messages ──────────────────────────────────────────────────────

// TestIndicator_Message covers Spinner.Message and Progress.Message against a non-terminal
// writer — the redirected case, where both must stay silent rather than smear the log.
func TestIndicator_Message(t *testing.T) {
	sw := new(bytes.Buffer)
	sp := NewSpinner(sw)
	sp.Message("still working") // before Start: must not panic
	sp.Start(context.Background())
	sp.Message("halfway")
	sp.Stop()
	if sw.Len() != 0 {
		t.Errorf("spinner wrote %q to a non-terminal writer, want silence", sw)
	}

	pw := new(bytes.Buffer)
	pr := NewProgress(pw, 10)
	pr.Message("uploading")
	pr.Set(5)
	pr.Message("finishing")
	pr.Done()
	if pw.Len() != 0 {
		t.Errorf("progress wrote %q to a non-terminal writer, want silence", pw)
	}
}

// ── the last two uncovered helpers ──────────────────────────────────────────

// TestReconBind_unrecognizedCause covers the fallback arm of reconBind: a recon failure that
// is none of the four typed ones still has to produce a categorized, non-leaky *BindError
// naming the channel in plain words ("environment", not "env").
func TestReconBind_unrecognizedCause(t *testing.T) {
	cause := errors.New("some unrecognized recon failure")
	cases := []struct {
		channel string
		want    string
	}{
		{channelEnv, "environment"},
		{channelConfig, "configuration"},
		{channelStdin, "stdin"},
		{channelFlag, "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.channel, func(t *testing.T) {
			err := reconBind(tc.channel, cause)

			var be *BindError
			if !errors.As(err, &be) {
				t.Fatalf("reconBind returned %T, want a *BindError", err)
			}
			if !strings.Contains(be.Msg, tc.want) {
				t.Errorf("message %q does not name the channel as %q", be.Msg, tc.want)
			}
			if !errors.Is(err, cause) {
				t.Error("the original cause is not reachable with errors.Is")
			}
			if !errors.Is(err, ErrUsage) {
				t.Error("an unrecognized channel failure should still be usage-class")
			}
			// Non-leaky: the raw recon text must not reach the user's message.
			if strings.Contains(be.Msg, cause.Error()) {
				t.Errorf("message %q leaks the raw cause", be.Msg)
			}
		})
	}

	if err := reconBind(channelEnv, nil); err != nil {
		t.Errorf("reconBind(nil) = %v, want nil", err)
	}
}

// TestPrinter_textMapIsSorted covers mapPairs, the text-format path for a map. A map has no
// order, so unsorted output would make `--output text` undiffable between runs — the kind of
// bug that only shows up in someone's CI.
func TestPrinter_textMapIsSorted(t *testing.T) {
	value := map[string]any{"zulu": 1, "alpha": 2, "mike": 3}

	render := func() string {
		w := new(bytes.Buffer)
		if err := NewPrinter(w).WithFormat(FormatText).Print(value); err != nil {
			t.Fatal(err)
		}
		return w.String()
	}

	got := render()
	for _, key := range []string{"alpha", "mike", "zulu"} {
		if !strings.Contains(got, key) {
			t.Errorf("rendered text is missing key %q:\n%s", key, got)
		}
	}
	if a, m, z := strings.Index(got, "alpha"), strings.Index(got, "mike"), strings.Index(got, "zulu"); !(a < m && m < z) {
		t.Errorf("map keys are not in sorted order:\n%s", got)
	}

	// Stable across renders — the property that makes the output diffable.
	for range 5 {
		if again := render(); again != got {
			t.Fatalf("text map rendering is not stable:\n%s\nvs\n%s", got, again)
		}
	}
}
