package rotini

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// errHooks is an ErrorHandler that logs each hook and returns the error configured for it.
type errHooks struct {
	name string
	log  *[]string
	errs map[string]error
	do   map[string]func(rtx *Context)
}

func (h *errHooks) hook(hook string, rtx *Context) error {
	*h.log = append(*h.log, h.name+"."+hook)
	if fn := h.do[hook]; fn != nil {
		fn(rtx)
	}
	return h.errs[hook]
}

func (h *errHooks) CascadingPreRun(_ context.Context, rtx *Context) error {
	return h.hook("CascadingPreRun", rtx)
}
func (h *errHooks) PreRun(_ context.Context, rtx *Context) error { return h.hook("PreRun", rtx) }
func (h *errHooks) Run(_ context.Context, rtx *Context) error    { return h.hook("Run", rtx) }
func (h *errHooks) PostRun(_ context.Context, rtx *Context) error {
	return h.hook("PostRun", rtx)
}
func (h *errHooks) CascadingPostRun(_ context.Context, rtx *Context) error {
	return h.hook("CascadingPostRun", rtx)
}

var _ ErrorHandler = (*errHooks)(nil)

// runErrHooks runs `app sub` with the root's and sub's handlers, returning the exit code, the
// run's error and stderr.
func runErrHooks(t *testing.T, root, sub Handler) (code int, err error, stderr string) {
	t.Helper()
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "sub", Handler: "Sub"}}}
	p := NewProgramFunc(def, func(name string) (Handler, bool) {
		switch name {
		case "App":
			return root, true
		case "Sub":
			return sub, true
		}
		return nil, false
	})
	var o, e bytes.Buffer
	p.stdout, p.stderr = &o, &e
	code, err = p.Run([]string{"sub"})
	return code, err, e.String()
}

func TestErrorHandler_returnedErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	full := []string{
		"app.CascadingPreRun", "sub.CascadingPreRun", "sub.PreRun", "sub.Run",
		"sub.PostRun", "sub.CascadingPostRun", "app.CascadingPostRun",
	}
	tests := []struct {
		name    string
		at      string // the sub hook that returns boom
		wantLog []string
	}{
		{"CascadingPreRun halts forward progress", "CascadingPreRun",
			[]string{"app.CascadingPreRun", "sub.CascadingPreRun", "sub.CascadingPostRun", "app.CascadingPostRun"}},
		{"PreRun halts before Run", "PreRun",
			[]string{"app.CascadingPreRun", "sub.CascadingPreRun", "sub.PreRun", "sub.PostRun", "sub.CascadingPostRun", "app.CascadingPostRun"}},
		{"Run records", "Run", full},
		{"PostRun records and teardown goes on", "PostRun", full},
		{"CascadingPostRun records and teardown goes on", "CascadingPostRun", full},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var log []string
			root := FromErrorHandler(&errHooks{name: "app", log: &log})
			sub := FromErrorHandler(&errHooks{name: "sub", log: &log, errs: map[string]error{tt.at: boom}})
			code, err, stderr := runErrHooks(t, root, sub)
			if !slices.Equal(log, tt.wantLog) {
				t.Errorf("hooks ran\n  %v\nwant\n  %v", log, tt.wantLog)
			}
			if code != 1 || !errors.Is(err, boom) {
				t.Errorf("code, err = %d, %v; want 1 and boom", code, err)
			}
			if stderr != "Error: boom\n" {
				t.Errorf("stderr = %q, want one Error: line", stderr)
			}
		})
	}
}

func TestErrorHandler_nilReturnsRunEverything(t *testing.T) {
	t.Parallel()
	var log []string
	code, err, stderr := runErrHooks(t,
		FromErrorHandler(&errHooks{name: "app", log: &log}),
		FromErrorHandler(&errHooks{name: "sub", log: &log}))
	if code != 0 || err != nil || stderr != "" || len(log) != 7 {
		t.Errorf("code=%d err=%v stderr=%q hooks=%v; want a clean run of all seven hooks", code, err, stderr, log)
	}
}

// TestErrorHandler_noDoubleRecord pins that a hook returning the error it just recorded
// reports it once, and that a different error is recorded as well.
func TestErrorHandler_noDoubleRecord(t *testing.T) {
	t.Parallel()
	first, second := errors.New("first"), errors.New("second")
	tests := []struct {
		name   string
		record func(rtx *Context)
		ret    error
		want   string
	}{
		{"HaltWith then return it", func(rtx *Context) { rtx.HaltWith(first) }, first, "Error: first\n"},
		{"RecordError then return it", func(rtx *Context) { rtx.RecordError(first) }, first, "Error: first\n"},
		{"record one, return another", func(rtx *Context) { rtx.RecordError(first) }, second, "Error: first\nError: second\n"},
		{"a parse error keeps its path once", func(rtx *Context) {
			rtx.HaltWith(&ParseError{Kind: ParseKindUnknownFlag, Msg: `unknown flag "--x"`})
		}, nil, "Error: app sub: unknown flag \"--x\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var log []string
			sub := &errHooks{name: "sub", log: &log,
				errs: map[string]error{"Run": tt.ret}, do: map[string]func(*Context){"Run": tt.record}}
			_, _, stderr := runErrHooks(t, FromErrorHandler(&errHooks{name: "app", log: &log}), FromErrorHandler(sub))
			if stderr != tt.want {
				t.Errorf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

// uncomparableErr is an error whose dynamic type == can't compare.
type uncomparableErr struct{ parts []string }

func (e uncomparableErr) Error() string { return strings.Join(e.parts, ",") }

func TestErrorHandler_uncomparableErrors(t *testing.T) {
	t.Parallel()
	var log []string
	e := uncomparableErr{parts: []string{"a", "b"}}
	sub := &errHooks{name: "sub", log: &log,
		errs: map[string]error{"Run": e}, do: map[string]func(*Context){"Run": func(rtx *Context) { rtx.RecordError(e) }}}
	code, _, stderr := runErrHooks(t, FromErrorHandler(&errHooks{name: "app", log: &log}), FromErrorHandler(sub))
	// An uncomparable value can't be told apart from a different one, so both are kept.
	if code != 1 || stderr != "Error: a,b\nError: a,b\n" {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestErrorHandler_codeWithoutError(t *testing.T) {
	t.Parallel()
	var log []string
	root := &errHooks{name: "app", log: &log,
		do: map[string]func(*Context){"CascadingPreRun": func(rtx *Context) { rtx.HaltWithCode(0) }}}
	code, err, _ := runErrHooks(t, FromErrorHandler(root), FromErrorHandler(&errHooks{name: "sub", log: &log}))
	if code != 0 || err != nil || slices.Contains(log, "sub.Run") {
		t.Errorf("code=%d err=%v hooks=%v; want a clean stop before Run", code, err, log)
	}
}

// TestErrorHandler_mixedShapes runs a void root over an error-returning sub-command, as a
// parent composing children of either shape does.
func TestErrorHandler_mixedShapes(t *testing.T) {
	t.Parallel()
	var log []string
	root := &actHandler{name: "app", log: &log}
	sub := FromErrorHandler(&errHooks{name: "sub", log: &log, errs: map[string]error{"PreRun": errors.New("no")}})
	code, _, stderr := runErrHooks(t, root, sub)
	if code != 1 || stderr != "Error: no\n" || slices.Contains(log, "sub.Run") || !slices.Contains(log, "app.CascadingPostRun") {
		t.Errorf("code=%d stderr=%q hooks=%v", code, stderr, log)
	}
}

func TestErrorHandler_embedsAndNil(t *testing.T) {
	t.Parallel()
	var h NoHooksE
	ctx, rtx := context.Background(), NewContextFor(Definition{Name: "app"}, nil)
	for _, err := range []error{
		h.CascadingPreRun(ctx, rtx), h.PreRun(ctx, rtx), h.PostRun(ctx, rtx), h.CascadingPostRun(ctx, rtx),
	} {
		if err != nil {
			t.Errorf("a no-op hook returned %v", err)
		}
	}
	if FromErrorHandler(nil) != nil {
		t.Error("FromErrorHandler(nil) is not nil")
	}
}

// errSet is a handler set for NewProgram whose method returns an ErrorHandler.
type errSet struct{ log *[]string }

func (s errSet) App() ErrorHandler {
	return &errHooks{name: "app", log: s.log, errs: map[string]error{"Run": errors.New("from reflection")}}
}

func TestNewProgram_acceptsErrorHandler(t *testing.T) {
	t.Parallel()
	var log []string
	p := NewProgram(Definition{Name: "app", Handler: "App"}, errSet{log: &log})
	var o, e bytes.Buffer
	p.stdout, p.stderr = &o, &e
	code, _ := p.Run(nil)
	if code != 1 || e.String() != "Error: from reflection\n" {
		t.Errorf("code=%d stderr=%q", code, e.String())
	}
}

// errCompleter is an ErrorHandler that also completes its argument.
type errCompleter struct{ NoHooksE }

func (errCompleter) Run(context.Context, *Context) error { return nil }

func (errCompleter) CompleteArgValue(*Context, string, string) []string { return []string{"alpha"} }

// TestFromErrorHandler_keepsCompleters pins that wrapping a handler doesn't hide the completer
// methods completion looks for.
func TestFromErrorHandler_keepsCompleters(t *testing.T) {
	t.Parallel()
	lookup := func(string) (Handler, bool) { return FromErrorHandler(errCompleter{}), true }
	c, ok := resolveHandler[ArgValueCompleter](lookup, "App")
	if !ok || !slices.Equal(c.CompleteArgValue(nil, "", ""), []string{"alpha"}) {
		t.Errorf("the wrapped handler's ArgValueCompleter was not found")
	}
	if _, ok := resolveHandler[FlagValueCompleter](lookup, "App"); ok {
		t.Error("found a FlagValueCompleter the handler doesn't implement")
	}
}
