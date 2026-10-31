package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// mwHandler is an ErrorHandler that logs "<command>.<hook>" for every hook it runs and returns
// the error set for that hook, if any.
type mwHandler struct {
	name string
	log  *[]string
	errs map[string]error
	do   map[string]func(ctx context.Context, rtx *Context)
}

func (h *mwHandler) hook(ctx context.Context, rtx *Context, kind string) error {
	*h.log = append(*h.log, h.name+"."+kind)
	if fn := h.do[kind]; fn != nil {
		fn(ctx, rtx)
	}
	return h.errs[kind]
}

func (h *mwHandler) CascadingPreRun(ctx context.Context, rtx *Context) error {
	return h.hook(ctx, rtx, "CascadingPreRun")
}
func (h *mwHandler) PreRun(ctx context.Context, rtx *Context) error {
	return h.hook(ctx, rtx, "PreRun")
}
func (h *mwHandler) Run(ctx context.Context, rtx *Context) error { return h.hook(ctx, rtx, "Run") }
func (h *mwHandler) PostRun(ctx context.Context, rtx *Context) error {
	return h.hook(ctx, rtx, "PostRun")
}
func (h *mwHandler) CascadingPostRun(ctx context.Context, rtx *Context) error {
	return h.hook(ctx, rtx, "CascadingPostRun")
}

// mwRun is one run of a two-command program (app, its sub-command sub) under middleware.
type mwRun struct {
	log     []string
	code    int
	outcome Outcome
}

// mwProgram builds the app/sub program whose handlers log to log; errs maps
// "<command>.<hook>" to the error that hook returns, do to an action it runs first.
func mwProgram(log *[]string, errs map[string]error, do map[string]func(context.Context, *Context)) *Program {
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "sub", Handler: "AppSub"}}}
	lookup := func(handler string) (Handler, bool) {
		name := map[string]string{"App": "app", "AppSub": "sub"}[handler]
		e, d := map[string]error{}, map[string]func(context.Context, *Context){}
		for key, err := range errs {
			if cmd, hook, _ := strings.Cut(key, "."); cmd == name {
				e[hook] = err
			}
		}
		for key, fn := range do {
			if cmd, hook, _ := strings.Cut(key, "."); cmd == name {
				d[hook] = fn
			}
		}
		return FromErrorHandler(&mwHandler{name: name, log: log, errs: e, do: d}), true
	}
	return NewProgramFunc(def, lookup).WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).WithoutSignalHandling()
}

// runMW runs `app sub` under mw, capturing the hook log, the exit code and the outcome.
func runMW(t *testing.T, errs map[string]error, do map[string]func(context.Context, *Context), mw ...Middleware) mwRun {
	t.Helper()
	var r mwRun
	p := mwProgram(&r.log, errs, do).WithMiddleware(mw...).WithReporter(func(ctx context.Context, rtx *Context, out Outcome) {
		r.outcome = out
		if out.Failed() {
			rtx.Exit(1)
		}
	})
	r.code, _ = p.Run([]string{"sub"})
	return r
}

// mwFullOrder is the hook log of an uneventful `app sub`.
var mwFullOrder = []string{
	"app.CascadingPreRun", "sub.CascadingPreRun", "sub.PreRun", "sub.Run", "sub.PostRun",
	"sub.CascadingPostRun", "app.CascadingPostRun",
}

// mwHooks are the hooks of an `app sub` run, as "<command>.<hook>", with their kinds.
var mwHooks = []struct {
	key  string
	kind HookKind
}{
	{"app.CascadingPreRun", HookCascadingPreRun},
	{"sub.CascadingPreRun", HookCascadingPreRun},
	{"sub.PreRun", HookPreRun},
	{"sub.Run", HookRun},
	{"sub.PostRun", HookPostRun},
	{"sub.CascadingPostRun", HookCascadingPostRun},
	{"app.CascadingPostRun", HookCascadingPostRun},
}

// onHook is middleware that applies act around one hook, named "<command>.<hook>", and passes
// every other hook through untouched.
func onHook(key string, act func(next HookFunc) HookFunc) Middleware {
	return func(h Hook, next HookFunc) HookFunc {
		if h.Command.Name+"."+h.Kind.String() != key {
			return next
		}
		return act(next)
	}
}

// expectedAfter is the hook log when the hook at index stop of mwFullOrder halts forward
// progress: the hooks up to it, then the teardowns of the begun steps. skipped drops that
// hook from the log.
func expectedAfter(stop int, skipped bool) []string {
	var want []string
	for i, k := range mwFullOrder[:stop+1] {
		if i == stop && skipped {
			continue
		}
		want = append(want, k)
	}
	switch stop {
	case 0:
		want = append(want, "app.CascadingPostRun")
	case 1:
		want = append(want, "sub.CascadingPostRun", "app.CascadingPostRun")
	default:
		want = append(want, mwFullOrder[4:]...)
	}
	return want
}

// The matrix: every hook kind, setup and teardown, under middleware that calls next and
// returns nil, returns an error, skips the hook, or panics.
func TestMiddleware_matrix(t *testing.T) {
	errMW := errors.New("from middleware")
	for i, hk := range mwHooks {
		teardown := hk.kind.teardown()
		t.Run(hk.key+"/nil", func(t *testing.T) {
			// A middleware that swallows the hook's error: nothing is recorded.
			r := runMW(t, map[string]error{hk.key: errors.New("hook")}, nil, onHook(hk.key, func(next HookFunc) HookFunc {
				return func(ctx context.Context, rtx *Context) error {
					_ = next(ctx, rtx)
					return nil
				}
			}))
			if !slices.Equal(r.log, mwFullOrder) || r.code != 0 || r.outcome.Failed() {
				t.Errorf("log %v, code %d, outcome %+v; want the full run, 0, nothing recorded", r.log, r.code, r.outcome)
			}
		})
		t.Run(hk.key+"/error", func(t *testing.T) {
			r := runMW(t, nil, nil, onHook(hk.key, func(next HookFunc) HookFunc {
				return func(ctx context.Context, rtx *Context) error {
					if err := next(ctx, rtx); err != nil {
						t.Errorf("next = %v, want nil", err)
					}
					return errMW
				}
			}))
			want := mwFullOrder
			if !teardown {
				want = expectedAfter(i, false)
			}
			if !slices.Equal(r.log, want) {
				t.Errorf("log:\n got %v\nwant %v", r.log, want)
			}
			if r.code != 1 || len(r.outcome.Errors) != 1 || !errors.Is(r.outcome.Errors[0], errMW) {
				t.Errorf("code %d, errors %v; want 1 and the middleware's error once", r.code, r.outcome.Errors)
			}
		})
		t.Run(hk.key+"/skip", func(t *testing.T) {
			r := runMW(t, nil, nil, onHook(hk.key, func(HookFunc) HookFunc {
				return func(context.Context, *Context) error { return nil }
			}))
			want := slices.DeleteFunc(slices.Clone(mwFullOrder), func(k string) bool { return k == hk.key })
			if !slices.Equal(r.log, want) || r.code != 0 {
				t.Errorf("log %v, code %d; want %v, 0", r.log, r.code, want)
			}
		})
		t.Run(hk.key+"/panic", func(t *testing.T) {
			r := runMW(t, nil, nil, onHook(hk.key, func(next HookFunc) HookFunc {
				return func(ctx context.Context, rtx *Context) error {
					_ = next(ctx, rtx)
					panic("boom")
				}
			}))
			want := mwFullOrder
			if !teardown {
				want = expectedAfter(i, false)
			}
			if !slices.Equal(r.log, want) {
				t.Errorf("log:\n got %v\nwant %v", r.log, want)
			}
			if len(r.outcome.Panics) != 1 || r.outcome.Panics[0].Value != "boom" || r.code != 1 {
				t.Errorf("panics %v, code %d; want the one panic, 1", r.outcome.Panics, r.code)
			}
		})
	}
}

// A skipped setup hook that returns an error halts as a returned hook error does: a
// middleware refusing a command.
func TestMiddleware_refuseWithoutRunning(t *testing.T) {
	refused := errors.New("refused")
	r := runMW(t, nil, nil, onHook("sub.Run", func(HookFunc) HookFunc {
		return func(context.Context, *Context) error { return refused }
	}))
	want := []string{"app.CascadingPreRun", "sub.CascadingPreRun", "sub.PreRun", "sub.PostRun", "sub.CascadingPostRun", "app.CascadingPostRun"}
	if !slices.Equal(r.log, want) || r.code != 1 || len(r.outcome.Errors) != 1 || !errors.Is(r.outcome.Errors[0], refused) {
		t.Errorf("log %v, code %d, errors %v", r.log, r.code, r.outcome.Errors)
	}
}

// next returns an ErrorHandler hook's error before it is recorded, and the error is recorded
// once after the chain.
func TestMiddleware_seesHookErrorBeforeRecording(t *testing.T) {
	hookErr := errors.New("hook failed")
	var seen error
	var failedInside bool
	r := runMW(t, map[string]error{"sub.Run": hookErr}, nil, onHook("sub.Run", func(next HookFunc) HookFunc {
		return func(ctx context.Context, rtx *Context) error {
			seen = next(ctx, rtx)
			failedInside = rtx.Failed()
			return seen
		}
	}))
	if !errors.Is(seen, hookErr) || failedInside {
		t.Errorf("next = %v, Failed inside = %v; want the hook's error, not yet recorded", seen, failedInside)
	}
	if len(r.outcome.Errors) != 1 || r.code != 1 {
		t.Errorf("errors %v, code %d; want the error once, 1", r.outcome.Errors, r.code)
	}
}

// An error the hook recorded itself and returned is recorded once, also when the middleware
// returns it; a Handler (void) hook's next returns nil.
func TestMiddleware_noDoubleRecordAndVoidHooks(t *testing.T) {
	hookErr := errors.New("halted")
	r := runMW(t, map[string]error{"sub.PreRun": hookErr},
		map[string]func(context.Context, *Context){"sub.PreRun": func(_ context.Context, rtx *Context) { rtx.HaltWith(hookErr) }},
		onHook("sub.PreRun", func(next HookFunc) HookFunc { return next }))
	if len(r.outcome.Errors) != 1 {
		t.Errorf("errors %v, want one", r.outcome.Errors)
	}

	recorded := errors.New("recorded")
	def := Definition{Name: "app", Handler: "App"}
	var got []error
	p := NewProgramFunc(def, func(string) (Handler, bool) {
		return &comboHandler{name: "app", log: new([]string), do: map[string]comboHook{
			"Run": func(_ context.Context, rtx *Context) { rtx.RecordError(recorded) },
		}}, true
	}).WithStdout(&bytes.Buffer{}).WithStderr(&bytes.Buffer{}).WithoutSignalHandling().
		WithMiddleware(func(h Hook, next HookFunc) HookFunc {
			return func(ctx context.Context, rtx *Context) error {
				err := next(ctx, rtx)
				got = append(got, err)
				return err
			}
		})
	if code, err := p.Run(nil); code != 1 || !errors.Is(err, recorded) {
		t.Errorf("Run = %d, %v; want 1 and the recorded error", code, err)
	}
	if len(got) != 5 || slices.ContainsFunc(got, func(e error) bool { return e != nil }) {
		t.Errorf("next returned %v; want nil from each of the five void hooks", got)
	}
}

// The first middleware registered is the outermost, and a second WithMiddleware call appends
// inside the first; a nil middleware is skipped.
func TestMiddleware_order(t *testing.T) {
	var log []string
	named := func(name string) Middleware {
		return func(h Hook, next HookFunc) HookFunc {
			if h.Kind != HookRun {
				return next
			}
			return func(ctx context.Context, rtx *Context) error {
				log = append(log, name+">")
				err := next(ctx, rtx)
				log = append(log, "<"+name)
				return err
			}
		}
	}
	p := mwProgram(&log, nil, nil).WithMiddleware(named("a"), nil, named("b")).WithMiddleware(named("c"))
	if code, err := p.Run([]string{"sub"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	want := []string{"app.CascadingPreRun", "sub.CascadingPreRun", "sub.PreRun", "a>", "b>", "c>", "sub.Run", "<c", "<b", "<a", "sub.PostRun", "sub.CascadingPostRun", "app.CascadingPostRun"}
	if !slices.Equal(log, want) {
		t.Errorf("log:\n got %v\nwant %v", log, want)
	}
}

// Hook names the hook and its command: the ancestor for a cascading hook, as rtx.Command does.
func TestMiddleware_hookIdentity(t *testing.T) {
	var got []string
	runMW(t, nil, nil, func(h Hook, next HookFunc) HookFunc {
		return func(ctx context.Context, rtx *Context) error {
			if rtx.Command().Name != h.Command.Name {
				t.Errorf("%s: rtx.Command() = %s", h.Kind, rtx.Command().Name)
			}
			got = append(got, fmt.Sprintf("%s.%s:%v", h.Command.Name, h.Kind, h.Command.Invoked))
			return next(ctx, rtx)
		}
	})
	want := []string{
		"app.CascadingPreRun:false", "sub.CascadingPreRun:true", "sub.PreRun:true", "sub.Run:true",
		"sub.PostRun:true", "sub.CascadingPostRun:true", "app.CascadingPostRun:false",
	}
	if !slices.Equal(got, want) {
		t.Errorf("hooks:\n got %v\nwant %v", got, want)
	}
}

type mwKey struct{}

// A derived context passed to next reaches that hook, as its ctx and rtx.Context, and not the
// hooks after it; a hook's own SetContext still hands its context on.
func TestMiddleware_contextPassing(t *testing.T) {
	seen := map[string]any{}
	record := func(key string) func(context.Context, *Context) {
		return func(ctx context.Context, rtx *Context) {
			seen[key] = ctx.Value(mwKey{})
			seen[key+"/rtx"] = rtx.Context().Value(mwKey{})
		}
	}
	do := map[string]func(context.Context, *Context){
		"sub.CascadingPreRun": record("sub.CascadingPreRun"),
		"sub.PreRun": func(ctx context.Context, rtx *Context) {
			record("sub.PreRun")(ctx, rtx)
			rtx.SetContext(context.WithValue(ctx, mwKey{}, "set by PreRun"))
		},
		"sub.Run":     record("sub.Run"),
		"sub.PostRun": record("sub.PostRun"),
	}
	derive := func(value string) Middleware {
		return func(h Hook, next HookFunc) HookFunc {
			return func(ctx context.Context, rtx *Context) error {
				if h.Kind == HookCascadingPreRun || h.Kind == HookPreRun || h.Kind == HookPostRun {
					ctx = context.WithValue(ctx, mwKey{}, value+" "+h.Kind.String())
				}
				return next(ctx, rtx)
			}
		}
	}
	runMW(t, nil, do, derive("mw"))
	want := map[string]any{
		"sub.CascadingPreRun": "mw CascadingPreRun", "sub.CascadingPreRun/rtx": "mw CascadingPreRun",
		"sub.PreRun": "mw PreRun", "sub.PreRun/rtx": "mw PreRun",
		"sub.Run": "set by PreRun", "sub.Run/rtx": "set by PreRun",
		"sub.PostRun": "mw PostRun", "sub.PostRun/rtx": "mw PostRun",
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s saw %v, want %v", k, seen[k], v)
		}
	}
}

// A middleware that hands its derived context on with SetContext reaches every later hook.
func TestMiddleware_setContextHandsOn(t *testing.T) {
	var runSaw any
	do := map[string]func(context.Context, *Context){
		"sub.Run": func(ctx context.Context, _ *Context) { runSaw = ctx.Value(mwKey{}) },
	}
	runMW(t, nil, do, onHook("app.CascadingPreRun", func(next HookFunc) HookFunc {
		return func(ctx context.Context, rtx *Context) error {
			ctx = context.WithValue(ctx, mwKey{}, "span")
			rtx.SetContext(ctx)
			return next(ctx, rtx)
		}
	}))
	if runSaw != "span" {
		t.Errorf("Run saw %v, want the middleware's context", runSaw)
	}
}

// A deeper chain, as a composed child's is under its parent: every command's cascading hooks
// are wrapped.
func TestMiddleware_deepChain(t *testing.T) {
	p, argv, log, _ := comboTree(4, nil)
	var wrapped []string
	p.WithMiddleware(func(h Hook, next HookFunc) HookFunc {
		return func(ctx context.Context, rtx *Context) error {
			wrapped = append(wrapped, h.Command.Name+"."+h.Kind.String())
			return next(ctx, rtx)
		}
	})
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if !slices.Equal(wrapped, *log) || !slices.Equal(wrapped, fullOrder(4)) {
		t.Errorf("wrapped %v\n log %v", wrapped, *log)
	}
}

// Under a custom plan, middleware wraps the hooks the plan takes from DefaultLifecycle and not
// a step function the plan writes itself.
func TestMiddleware_customLifecycle(t *testing.T) {
	var log []string
	p := mwProgram(&log, nil, nil).WithLifecycle(func(chain []Command, handlers []Handler) []LifecycleStep {
		steps := DefaultLifecycle(chain, handlers)
		own := LifecycleStep{Name: "own", Do: func(context.Context, *Context) { log = append(log, "own") }}
		return append([]LifecycleStep{own}, steps...)
	}).WithMiddleware(func(h Hook, next HookFunc) HookFunc {
		return func(ctx context.Context, rtx *Context) error {
			log = append(log, "mw:"+h.Kind.String())
			return next(ctx, rtx)
		}
	})
	if code, err := p.Run([]string{"sub"}); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	want := []string{"own"}
	for _, k := range mwFullOrder {
		_, kind, _ := strings.Cut(k, ".")
		want = append(want, "mw:"+kind, k)
	}
	if !slices.Equal(log, want) {
		t.Errorf("log:\n got %v\nwant %v", log, want)
	}
}

// A middleware returning a nil HookFunc is a panic, reported as one.
func TestMiddleware_nilHookFunc(t *testing.T) {
	r := runMW(t, nil, nil, func(h Hook, next HookFunc) HookFunc {
		if h.Kind == HookRun {
			return nil
		}
		return next
	})
	if len(r.outcome.Panics) != 1 || !strings.Contains(fmt.Sprint(r.outcome.Panics[0].Value), "nil HookFunc for Run") {
		t.Errorf("panics %v, want the nil HookFunc panic", r.outcome.Panics)
	}
}

func TestHookKind_String(t *testing.T) {
	for k, want := range map[HookKind]string{
		HookCascadingPreRun: "CascadingPreRun", HookPreRun: "PreRun", HookRun: "Run",
		HookPostRun: "PostRun", HookCascadingPostRun: "CascadingPostRun", HookKind(9): "HookKind(9)",
	} {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}
