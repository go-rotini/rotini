package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// comboHook is an action injected into one hook of one command of a comboTree.
type comboHook func(ctx context.Context, rtx *Context)

// comboHandler logs every hook it runs as "<command>.<hook>" and runs the action injected
// for that hook, if any.
type comboHandler struct {
	name string
	log  *[]string
	do   map[string]comboHook
}

func (h *comboHandler) hook(ctx context.Context, rtx *Context, which string) {
	*h.log = append(*h.log, h.name+"."+which)
	if fn := h.do[which]; fn != nil {
		fn(ctx, rtx)
	}
}

func (h *comboHandler) CascadingPreRun(ctx context.Context, rtx *Context) {
	h.hook(ctx, rtx, "CascadingPreRun")
}
func (h *comboHandler) PreRun(ctx context.Context, rtx *Context) { h.hook(ctx, rtx, "PreRun") }
func (h *comboHandler) Run(ctx context.Context, rtx *Context)    { h.hook(ctx, rtx, "Run") }
func (h *comboHandler) PostRun(ctx context.Context, rtx *Context) {
	h.hook(ctx, rtx, "PostRun")
}
func (h *comboHandler) CascadingPostRun(ctx context.Context, rtx *Context) {
	h.hook(ctx, rtx, "CascadingPostRun")
}

// comboTree is a chain of depth commands, c0 (the root) to c<depth-1>, each the only child of
// the one before. actions maps "<command>.<hook>" to the action that hook runs. It returns the
// program, the argv that invokes the deepest command, the hook log and the stderr buffer.
func comboTree(depth int, actions map[string]comboHook) (*Program, []string, *[]string, *bytes.Buffer) {
	var log []string
	var build func(i int) CommandDef
	build = func(i int) CommandDef {
		cd := CommandDef{Name: fmt.Sprintf("c%d", i), Handler: fmt.Sprintf("C%d", i)}
		if i+1 < depth {
			cd.Commands = []CommandDef{build(i + 1)}
		}
		return cd
	}
	def := Definition{Name: "c0", Handler: "C0"}
	if depth > 1 {
		def.Commands = []CommandDef{build(1)}
	}
	argv := make([]string, 0, depth-1)
	for i := 1; i < depth; i++ {
		argv = append(argv, fmt.Sprintf("c%d", i))
	}
	lookup := func(handler string) (Handler, bool) {
		name := "c" + strings.TrimPrefix(handler, "C")
		do := map[string]comboHook{}
		for key, fn := range actions {
			if cmd, hook, _ := strings.Cut(key, "."); cmd == name {
				do[hook] = fn
			}
		}
		return &comboHandler{name: name, log: &log, do: do}, true
	}
	var errs bytes.Buffer
	p := NewProgramFunc(def, lookup).WithStdout(&bytes.Buffer{}).WithStderr(&errs).WithoutSignalHandling()
	return p, argv, &log, &errs
}

// fullOrder is the hook order of an uneventful run of a comboTree of depth.
func fullOrder(depth int) []string {
	var order []string
	for i := range depth {
		order = append(order, fmt.Sprintf("c%d.CascadingPreRun", i))
	}
	leaf := fmt.Sprintf("c%d", depth-1)
	order = append(order, leaf+".PreRun", leaf+".Run", leaf+".PostRun")
	for i := depth - 1; i >= 0; i-- {
		order = append(order, fmt.Sprintf("c%d.CascadingPostRun", i))
	}
	return order
}

// A tree deeper than the plan's on-stack record still runs every hook in order, and a halt in
// the deepest setup hook unwinds exactly the begun steps.
func TestLifecycle_deepTreeOrderAndUnwind(t *testing.T) {
	p, argv, log, _ := comboTree(9, nil)
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v; want 0, nil", code, err)
	}
	if want := fullOrder(9); !slices.Equal(*log, want) {
		t.Errorf("order:\n got %v\nwant %v", *log, want)
	}

	p, argv, log, _ = comboTree(9, map[string]comboHook{
		"c6.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.HaltWithCode(4) },
	})
	if code, _ := p.Run(argv); code != 4 {
		t.Errorf("code = %d, want 4", code)
	}
	var want []string
	for i := range 7 {
		want = append(want, fmt.Sprintf("c%d.CascadingPreRun", i))
	}
	for i := 6; i >= 0; i-- {
		want = append(want, fmt.Sprintf("c%d.CascadingPostRun", i))
	}
	if !slices.Equal(*log, want) {
		t.Errorf("halted order:\n got %v\nwant %v", *log, want)
	}
}

// An error recorded in the root's setup and a panic in the leaf's teardown both reach the
// reporter after every teardown hook ran; the run's error reaches each with errors.As, and the
// default reporter prints the error before the panic and exits 1.
func TestLifecycle_rootErrorThenLeafTeardownPanic(t *testing.T) {
	errSetup := errors.New("setup failed")
	p, argv, log, errs := comboTree(3, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.RecordError(errSetup) },
		"c2.PostRun":         func(context.Context, *Context) { panic(fmt.Errorf("leaf teardown: %w", ErrUsage)) },
	})
	code, err := p.Run(argv)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if want := fullOrder(3); !slices.Equal(*log, want) {
		t.Errorf("order:\n got %v\nwant %v", *log, want)
	}
	if !errors.Is(err, errSetup) {
		t.Errorf("Run's error %v does not reach the recorded error", err)
	}
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Error() != "leaf teardown: usage error" || len(pe.Stack) == 0 {
		t.Errorf("Run's error %v: want a *PanicError with the panic value and its stack", err)
	}
	if got, want := errs.String(), "Error: setup failed\nFatal Error: leaf teardown: usage error\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// A teardown hook that halts changes nothing: the remaining teardown runs, and a code set in
// teardown applies only when nothing set one before it.
func TestLifecycle_haltsInTeardown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions map[string]comboHook
		want    int
	}{
		{"Halt in PostRun", map[string]comboHook{
			"c2.PostRun": func(_ context.Context, rtx *Context) { rtx.Halt() },
		}, 0},
		{"HaltWithCode in CascadingPostRun", map[string]comboHook{
			"c1.CascadingPostRun": func(_ context.Context, rtx *Context) { rtx.HaltWithCode(6) },
		}, 6},
		{"forward code wins over teardown code", map[string]comboHook{
			"c2.Run":              func(_ context.Context, rtx *Context) { rtx.HaltWithCode(3) },
			"c1.CascadingPostRun": func(_ context.Context, rtx *Context) { rtx.HaltWithCode(6) },
		}, 3},
		{"HaltWith in PostRun only records", map[string]comboHook{
			"c2.PostRun": func(_ context.Context, rtx *Context) { rtx.HaltWith(errors.New("late")) },
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, argv, log, _ := comboTree(3, tc.actions)
			if code, _ := p.Run(argv); code != tc.want {
				t.Errorf("code = %d, want %d", code, tc.want)
			}
			if want := fullOrder(3); !slices.Equal(*log, want) {
				t.Errorf("order:\n got %v\nwant %v", *log, want)
			}
		})
	}
}

// A warning recorded with a halt stops forward progress without failing the run: exit 0, the
// warning on stderr, and no error from Run.
func TestLifecycle_haltWithAWarningIsNotAFailure(t *testing.T) {
	p, argv, log, errs := comboTree(2, map[string]comboHook{
		"c1.PreRun": func(_ context.Context, rtx *Context) {
			rtx.RecordWarning(errors.New("nothing to do"))
			rtx.Halt()
		},
		"c1.PostRun": func(_ context.Context, rtx *Context) {
			if rtx.Failed() {
				rtx.RecordError(errors.New("a warning made Failed true"))
			}
		},
	})
	code, err := p.Run(argv)
	if code != 0 || err != nil {
		t.Errorf("Run = %d, %v; want 0, nil", code, err)
	}
	if slices.Contains(*log, "c1.Run") {
		t.Errorf("Run ran after a PreRun halt: %v", *log)
	}
	if got := errs.String(); got != "Warning: nothing to do\n" {
		t.Errorf("stderr = %q", got)
	}
}

// The same error recorded twice is reported twice: the runtime reports what was recorded and
// does not deduplicate a handler's records.
func TestLifecycle_sameErrorRecordedTwiceIsReportedTwice(t *testing.T) {
	errDup := errors.New("dup")
	p, argv, _, errs := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			rtx.RecordError(errDup)
			rtx.HaltWith(errDup)
		},
	})
	if code, _ := p.Run(argv); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if got := strings.Count(errs.String(), "Error: dup\n"); got != 2 {
		t.Errorf("stderr %q reports the error %d times, want 2", errs.String(), got)
	}
}

// Inside the reporter the halt methods are no-ops and records are dropped: the exit code is
// what the lifecycle set until the reporter calls Exit, and Run's error is the lifecycle's.
func TestReporter_haltAndRecordsInsideAreInert(t *testing.T) {
	errRun := errors.New("run")
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) { rtx.RecordError(errRun) },
	})
	p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) {
		rtx.HaltWithCode(9)
		rtx.Halt()
		rtx.HaltWith(errors.New("dropped"))
		rtx.RecordWarning(errors.New("dropped"))
	})
	code, err := p.Run(argv)
	if code != 0 {
		t.Errorf("code = %d, want 0: a custom reporter owns the code, and HaltWithCode inside it is a no-op", code)
	}
	if !errors.Is(err, errRun) || strings.Contains(err.Error(), "dropped") {
		t.Errorf("Run's error = %v, want the lifecycle's error only", err)
	}
}

// A panic inside the reporter is not recovered: it reaches Run's caller, after buffered stdout
// was already flushed.
func TestReporter_panicIsNotRecovered(t *testing.T) {
	var out bytes.Buffer
	p, argv, _, _ := comboTree(1, map[string]comboHook{
		"c0.Run": func(_ context.Context, rtx *Context) {
			fmt.Fprint(rtx.Stdout, "partial")
			rtx.RecordSuccess("done")
		},
	})
	p.WithStdout(&out).WithBufferedOutput(true).
		WithReporter(func(context.Context, *Context, Outcome) { panic("reporter bug") })
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		p.Run(argv)
	}()
	if recovered != "reporter bug" {
		t.Errorf("recovered %v, want the reporter's panic", recovered)
	}
	if out.String() != "partial" {
		t.Errorf("stdout = %q, want the handler's buffered output", out.String())
	}
}

// With recovery off and teardown on, a panic in a teardown hook does not stop the rest of
// teardown, and the first panic is the one re-raised.
func TestLifecycle_reRaiseKeepsTheFirstPanic(t *testing.T) {
	p, argv, log, _ := comboTree(3, map[string]comboHook{
		"c2.Run":              func(context.Context, *Context) { panic("first") },
		"c1.CascadingPostRun": func(context.Context, *Context) { panic("second") },
	})
	p.WithPanicRecover(false)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		p.Run(argv)
	}()
	if recovered != "first" {
		t.Errorf("re-raised %v, want the first panic", recovered)
	}
	if want := fullOrder(3); !slices.Equal(*log, want) {
		t.Errorf("order:\n got %v\nwant %v", *log, want)
	}
}

// With teardown on panic off, a panic in a teardown hook skips the teardown that remains, and
// is still reported.
func TestLifecycle_teardownPanicWithoutTeardownOnPanicStopsTheUnwind(t *testing.T) {
	p, argv, log, errs := comboTree(3, map[string]comboHook{
		"c2.PostRun": func(context.Context, *Context) { panic("teardown") },
	})
	p.WithTeardownOnPanic(false)
	if code, _ := p.Run(argv); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if slices.Contains(*log, "c2.CascadingPostRun") || slices.Contains(*log, "c0.CascadingPostRun") {
		t.Errorf("teardown continued after a teardown panic: %v", *log)
	}
	if !strings.Contains(errs.String(), "Fatal Error: teardown") {
		t.Errorf("stderr = %q, want the panic reported", errs.String())
	}
}

// Exit inside a teardown hook skips the rest of teardown but keeps an earlier error for the
// reporter, and the code is the first one set.
func TestLifecycle_exitInTeardownAfterAnError(t *testing.T) {
	p, argv, log, errs := comboTree(3, map[string]comboHook{
		"c0.CascadingPreRun":  func(_ context.Context, rtx *Context) { rtx.RecordError(errors.New("root")) },
		"c2.Run":              func(_ context.Context, rtx *Context) { rtx.HaltWithCode(2) },
		"c1.CascadingPostRun": func(_ context.Context, rtx *Context) { rtx.Exit(7) },
	})
	if code, _ := p.Run(argv); code != 2 {
		t.Errorf("code = %d, want 2, the first code set", code)
	}
	if slices.Contains(*log, "c0.CascadingPostRun") {
		t.Errorf("Exit did not skip the remaining teardown: %v", *log)
	}
	if errs.String() != "Error: root\n" {
		t.Errorf("stderr = %q", errs.String())
	}
}

// The default reporter prints a panic as its value alone, never its stack.
func TestReporter_panicStackNeverPrinted(t *testing.T) {
	p, argv, _, errs := comboTree(1, map[string]comboHook{
		"c0.Run": func(context.Context, *Context) { panic("boom") },
	})
	p.Run(argv)
	if got := errs.String(); got != "Fatal Error: boom\n" {
		t.Errorf("stderr = %q, want only the panic value", got)
	}
}

// A run-local dependency set in a setup hook reaches every later hook, the teardown of the
// hook that set it included, and a missing one requested in setup unwinds the begun steps and
// reaches the run's error as a *DependencyError.
func TestLifecycle_dependenciesAcrossHooks(t *testing.T) {
	conn := NewDependency[*strings.Builder]("conn")
	var seen []string
	p, argv, _, _ := comboTree(3, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) {
			rtx.SetDependencyIfAbsent(conn, &strings.Builder{})
		},
		"c2.Run": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(conn).WriteString("work") },
		"c0.CascadingPostRun": func(_ context.Context, rtx *Context) {
			seen = append(seen, rtx.MustGetDependency(conn).String())
		},
	})
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if code, err := p.Run(argv); code != 0 || err != nil {
		t.Fatalf("second Run = %d, %v", code, err)
	}
	if !slices.Equal(seen, []string{"work", "work"}) {
		t.Errorf("teardown saw %q, want each run's own value", seen)
	}

	p, argv, log, errs := comboTree(3, map[string]comboHook{
		"c1.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.MustGetDependency(conn) },
	})
	code, err := p.Run(argv)
	var de *DependencyError
	if code != 1 || !errors.As(err, &de) || de.Name != "conn" || CategoryOf(err) != CategoryInternal {
		t.Errorf("Run = %d, %v; want 1 and an internal *DependencyError naming conn", code, err)
	}
	want := []string{"c0.CascadingPreRun", "c1.CascadingPreRun", "c1.CascadingPostRun", "c0.CascadingPostRun"}
	if !slices.Equal(*log, want) {
		t.Errorf("order:\n got %v\nwant %v", *log, want)
	}
	if got := errs.String(); got != "Fatal Error: rotini: no dependency registered as \"conn\"\n" {
		t.Errorf("stderr = %q", got)
	}
}

// A resolver error its producer already tagged internal keeps its message, gaining only the
// "resolve:" context, and arrives as a fault.
func TestResolver_internalErrorKeepsItsTag(t *testing.T) {
	errRoute := errors.New("routing table missing")
	p, _, log, errs := comboTree(1, nil)
	p.WithResolver(func(Definition, []string) (Resolution, error) { return Resolution{}, InternalError(errRoute) })
	code, err := p.Run(nil)
	var pe *PanicError
	if code != 1 || !errors.As(err, &pe) || !errors.Is(err, errRoute) || CategoryOf(err) != CategoryInternal {
		t.Errorf("Run = %d, %v; want 1 and an internal fault wrapping the resolver's error", code, err)
	}
	if len(*log) != 0 {
		t.Errorf("hooks ran after a resolver fault: %v", *log)
	}
	if got := errs.String(); got != "Fatal Error: resolve: routing table missing\n" {
		t.Errorf("stderr = %q", got)
	}
}

// An output file left open is discarded when a panic is re-raised to the caller too, with or
// without teardown, so no temporary file is left beside the target.
func TestLifecycle_reRaisedPanicDiscardsOpenOutputs(t *testing.T) {
	for _, teardown := range []bool{true, false} {
		dir := t.TempDir()
		p, argv, _, _ := comboTree(1, map[string]comboHook{
			"c0.Run": func(_ context.Context, rtx *Context) {
				out, err := CreateOutput(rtx, filepath.Join(dir, "report.txt"))
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprint(out, "partial")
				panic("boom")
			},
		})
		p.WithPanicRecover(false).WithTeardownOnPanic(teardown)
		func() {
			defer func() { _ = recover() }()
			p.Run(argv)
		}()
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("teardown=%v: the run left %v behind", teardown, left)
		}
	}
}

// Execute under a supplied context exits with the code its cancellation cause carries, and an
// empty info is not a record.
func TestExecute_withContextCancelCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ExitCause(5))
	p, argv, log, errs := comboTree(2, map[string]comboHook{
		"c0.CascadingPreRun": func(_ context.Context, rtx *Context) { rtx.RecordInfo("") },
	})
	code := -1
	if err := p.WithContext(ctx).WithArgs(argv).WithExit(func(c int) { code = c }).Execute(); err != nil {
		t.Errorf("Execute = %v, want nil", err)
	}
	if code != 5 {
		t.Errorf("exit code = %d, want 5", code)
	}
	if want := []string{"c0.CascadingPreRun", "c0.CascadingPostRun"}; !slices.Equal(*log, want) {
		t.Errorf("order = %v, want %v", *log, want)
	}
	if errs.Len() != 0 {
		t.Errorf("stderr = %q, want nothing reported", errs.String())
	}
}
