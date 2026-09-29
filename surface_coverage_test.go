package rotini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
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

// ── handler defaults (handlers.go) ──────────────────────────────────────────

// TestDefaultEmbedsAreNoOps executes the four embeddable defaults. Every generated stub embeds
// them, so "does nothing, safely, with a nil context" is a real contract.
func TestDefaultEmbedsAreNoOps(t *testing.T) {
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

// TestReconBind_rootPathIsNamedByChannel pins how a failure about the payload AS A WHOLE is
// phrased. recon reports an empty Path for a document-level problem — a JSON stdin payload
// missing its own required property, say — and the message used to interpolate that empty
// string into the per-input noun:
//
//	Error: stdin field "": missing required property "name"
//
// A reader then hunts for a field called "", while the real subject sits in the detail behind
// an empty pair of quotes. An empty path now falls back to the channel's own name, and a
// NON-empty one is untouched, which is the half a naive fix would break.
func TestReconBind_rootPathIsNamedByChannel(t *testing.T) {
	root, field := recon.Path{}, recon.Path{"tags"}
	cases := []struct {
		name  string
		cause error
		want  string
	}{
		{"validation at the root", &recon.ValidationError{Path: root, Msg: `missing required property "name"`}, `stdin: missing required property "name"`},
		{"validation on a field", &recon.ValidationError{Path: field, Msg: "value is not of type array"}, `stdin field "tags": value is not of type array`},
		{"missing required at the root", &recon.MissingRequiredError{Path: root}, "stdin is required"},
		{"missing required on a field", &recon.MissingRequiredError{Path: field}, `stdin field "tags" is required`},
		{"coercion at the root", &recon.CoercionError{Path: root, Target: "int"}, "stdin: expected int"},
		{"empty value at the root", &recon.EmptyValueError{Path: root}, "stdin must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var be *BindError
			if !errors.As(reconBind(channelStdin, tc.cause), &be) {
				t.Fatalf("reconBind did not return a *BindError for %T", tc.cause)
			}
			if be.Msg != tc.want {
				t.Errorf("message = %q, want %q", be.Msg, tc.want)
			}
			if strings.Contains(be.Msg, `""`) {
				t.Errorf("message %q names an empty input", be.Msg)
			}
		})
	}
}

func TestTrimAcquiredPayload_oneRuleForBothPaths(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"one trailing newline goes", "hello\n", "hello"},
		{"a CRLF ending goes whole", "hello\r\n", "hello"},
		{"only ONE ending goes", "hello\n\n", "hello\n"},
		{"leading whitespace is content", "  hello", "  hello"},
		{"interior whitespace is content", "a  b", "a  b"},
		{"trailing spaces are content", "hello  ", "hello  "},
		{"spaces before the ending survive", "  hello  \n", "  hello  "},
		{"no ending, nothing to do", "hello", "hello"},
		{"empty stays empty", "", ""},
		{"a lone newline empties", "\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := trimAcquiredPayload(tc.in); got != tc.want {
				t.Errorf("trimAcquiredPayload(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// The property that actually matters: both entry points agree. resolveFlagValue reads
	// the sentinel side; bindRawStdin reads the channel side.
	const payload = "  hello  \n"
	dir := t.TempDir()
	file := filepath.Join(dir, "value.txt")
	if err := os.WriteFile(file, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	fd := FlagDef{Name: "input", Identifiers: []string{"-i"}, From: []string{"file", "stdin"}}

	fromFile, err := resolveFlagValue(fd, "@"+file, nil)
	if err != nil {
		t.Fatalf("resolveFlagValue(@file): %v", err)
	}
	fromStdin, err := resolveFlagValue(fd, "-", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("resolveFlagValue(-): %v", err)
	}
	var channel *string
	sf := reflect.ValueOf(&channel).Elem()
	if err := bindRawStdin(sf, "text", []byte(payload)); err != nil {
		t.Fatalf("bindRawStdin: %v", err)
	}

	if fromFile != fromStdin || fromFile != *channel {
		t.Errorf("the three acquisition paths disagree: @file=%q -=%q channel=%q", fromFile, fromStdin, *channel)
	}
	if *channel != "  hello  " {
		t.Errorf("payload = %q, want %q — leading and trailing spaces are content", *channel, "  hello  ")
	}
}
