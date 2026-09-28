package rotini

import (
	"context"
	"io"
	"strings"
	"testing"
)

// An inputs struct is anchored on the command whose hook is running, not guessed from its field
// count against the chain. These tests are the matrix that motivated the change, asserted the
// right way round.
//
// The tree is root → mid → leaf. Each frame owns a uniquely named flag so attribution is
// unambiguous, and every frame also declares --help, which every real CLI does and which used to
// disable the alignment guard entirely — so a misalignment here cannot be reported and would
// come back as a wrong value instead.

type fRootFlags struct {
	RootOnly bool `rotini:"rootonly"`
	Help     bool `rotini:"help"`
}
type fMidFlags struct {
	MidOnly bool `rotini:"midonly"`
	Help    bool `rotini:"help"`
}
type fLeafFlags struct {
	LeafOnly bool `rotini:"leafonly"`
	Help     bool `rotini:"help"`
}

type fRootCmd struct {
	Flags     fRootFlags
	Arguments struct{}
}
type fMidCmd struct {
	Flags     fMidFlags
	Arguments struct{}
}
type fLeafCmd struct {
	Flags     fLeafFlags
	Arguments struct{}
}

// fMidSpan is what codegen emits for `mid` in an ORDINARY cli: its whole lineage.
type fMidSpan struct {
	Root fRootCmd
	Mid  fMidCmd
}

// fMidOwn is what codegen emits for a COMPOSED CHILD's root command: one field, because the
// child cannot know which tree it will be mounted into.
type fMidOwn struct{ Mid fMidCmd }

// fLeafSpan is the leaf's own full-lineage type.
type fLeafSpan struct {
	Root fRootCmd
	Mid  fMidCmd
	Leaf fLeafCmd
}

func fFlags(own string) []FlagDef {
	return []FlagDef{
		{Name: own, Identifiers: []string{"--" + own}, Type: "bool"},
		{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
	}
}

func fDef() Definition {
	return Definition{
		Name: "root", Handler: "Root", Flags: fFlags("rootonly"),
		Commands: []CommandDef{{
			Name: "mid", Handler: "Mid", Flags: fFlags("midonly"),
			Commands: []CommandDef{{Name: "leaf", Handler: "Leaf", Flags: fFlags("leafonly")}},
		}},
	}
}

type fProg struct {
	inMid  func(*Context)
	inRoot func(*Context)
}

func (h fProg) Root() Handlers { return fHooks{cascading: h.inRoot} }
func (h fProg) Mid() Handlers  { return fHooks{cascading: h.inMid} }
func (h fProg) Leaf() Handlers { return fHooks{} }

type fHooks struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	cascading func(*Context)
}

func (h fHooks) Run(context.Context, *Context) {}
func (h fHooks) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.cascading != nil {
		h.cascading(rtx)
	}
}

func runF(t *testing.T, argv []string, inMid, inRoot func(*Context)) {
	t.Helper()
	p := NewProgram(fDef(), fProg{inMid: inMid, inRoot: inRoot}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run(argv); err != nil {
		t.Fatalf("Run(%v) = %v", argv, err)
	}
}

// TestFrame_reportsTheHooksOwnCommand is the information that did not exist before: Command is
// the leaf in every hook, Frame is the command this hook belongs to.
func TestFrame_reportsTheHooksOwnCommand(t *testing.T) {
	var rootSaw, midSaw, cmdSaw string
	runF(t, []string{"mid", "leaf"},
		func(rtx *Context) { midSaw, cmdSaw = rtx.Frame().Name, rtx.Command().Name },
		func(rtx *Context) { rootSaw = rtx.Frame().Name },
	)
	if rootSaw != "root" || midSaw != "mid" {
		t.Errorf("Frame() = root:%q mid:%q, want root/mid", rootSaw, midSaw)
	}
	if cmdSaw != "leaf" {
		t.Errorf("Command() in mid's cascading hook = %q, want the leaf %q", cmdSaw, "leaf")
	}
}

// TestCollect_isCorrectInACascadingHookAtEveryDepth is the finding, fixed. The same call reads
// mid's own flag whether or not a sub-command was invoked, and whether mid's type spans its
// whole lineage (an ordinary cli) or only itself (a composed child).
//
// Before this, three of these four cells returned false with a nil error.
func TestCollect_isCorrectInACascadingHookAtEveryDepth(t *testing.T) {
	for _, argv := range [][]string{
		{"mid", "--midonly"},         // mid IS the leaf
		{"mid", "--midonly", "leaf"}, // mid is a MIDDLE frame
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			runF(t, argv, func(rtx *Context) {
				span, err := Collect[fMidSpan](rtx)
				if err != nil {
					t.Errorf("Collect[fMidSpan] (ordinary cli): %v", err)
				} else if !span.Mid.Flags.MidOnly {
					t.Error("Collect[fMidSpan] (ordinary cli) did not read mid's own --midonly")
				}

				own, err := Collect[fMidOwn](rtx)
				if err != nil {
					t.Errorf("Collect[fMidOwn] (composed child): %v", err)
				} else if !own.Mid.Flags.MidOnly {
					t.Error("Collect[fMidOwn] (composed child) did not read mid's own --midonly")
				}
			}, nil)
		})
	}
}

// TestCollect_doesNotSeeADescendantsFlag is the other half of correctness: anchoring on the
// caller's frame must not reach DOWN the chain either.
func TestCollect_doesNotSeeADescendantsFlag(t *testing.T) {
	runF(t, []string{"mid", "leaf", "--leafonly"}, func(rtx *Context) {
		own, err := Collect[fMidOwn](rtx)
		if err != nil {
			t.Fatal(err)
		}
		if own.Mid.Flags.MidOnly {
			t.Error("mid's frame reported --midonly, which was never passed")
		}
	}, nil)
}

// TestCollect_rejectsADescendantsType is the exact check the frame makes possible: a struct that
// describes more commands than the caller is deep cannot be describing the caller.
//
// Under leaf-anchoring this was silent — the struct did not fit, binding was skipped, and every
// field came back zero with a nil error.
func TestCollect_rejectsADescendantsType(t *testing.T) {
	var err error
	runF(t, []string{"mid", "leaf"}, nil, func(rtx *Context) {
		_, err = Collect[fLeafSpan](rtx) // 3 fields, but the root is 1 deep
	})
	if err == nil {
		t.Fatal("collecting a descendant's 3-field type from the root's hook returned no error")
	}
	for _, want := range []string{"fLeafSpan", "describes 3 commands", "only 1 deep", "ITS OWN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%s", want, err)
		}
	}
}

// TestCollect_leafRunIsUnchanged pins the compatibility claim: for a leaf hook the frame IS the
// leaf, so the anchor reduces to what it always was.
func TestCollect_leafRunIsUnchanged(t *testing.T) {
	var got fLeafSpan
	var err error
	p := NewProgram(fDef(), fLeafProbe{capture: func(rtx *Context) { got, err = Collect[fLeafSpan](rtx) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, e := p.Run([]string{"mid", "--midonly", "leaf", "--leafonly"}); e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !got.Leaf.Flags.LeafOnly || !got.Mid.Flags.MidOnly {
		t.Errorf("leaf Run got leaf=%v mid=%v, want both true", got.Leaf.Flags.LeafOnly, got.Mid.Flags.MidOnly)
	}
}

type fLeafProbe struct{ capture func(*Context) }

func (h fLeafProbe) Root() Handlers { return fHooks{} }
func (h fLeafProbe) Mid() Handlers  { return fHooks{} }
func (h fLeafProbe) Leaf() Handlers { return fLeafRun(h) }

type fLeafRun fLeafProbe

func (fLeafRun) CascadingPreRun(context.Context, *Context)  {}
func (fLeafRun) PreRun(context.Context, *Context)           {}
func (fLeafRun) PostRun(context.Context, *Context)          {}
func (fLeafRun) CascadingPostRun(context.Context, *Context) {}
func (h fLeafRun) Run(_ context.Context, rtx *Context)      { h.capture(rtx) }

// TestAtFrame_customLifecycleFallsBackToTheLeaf is where the risk of this change is concentrated.
//
// A custom Lifecycle that wraps DefaultLifecycle inherits frame labelling. One that builds steps
// from scratch does not label them, and an unlabeled hook must report the LEAF — the behaviour
// the whole API had before frames existed — rather than the root, which would silently change
// what every existing custom lifecycle collects.
func TestAtFrame_customLifecycleFallsBackToTheLeaf(t *testing.T) {
	var unlabeled, labeled string

	// Steps built by hand, with no AtFrame: the root's cascading hook must see the leaf.
	bare := func(chain []ResolvedCommand, hs []Handlers) []LifecycleStep {
		return []LifecycleStep{
			{Name: "bare", Do: func(_ context.Context, rtx *Context) { unlabeled = rtx.Frame().Name }},
		}
	}
	p := NewProgram(fDef(), fProg{}).WithLifecycle(bare).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if unlabeled != "leaf" {
		t.Errorf("an unlabeled step saw Frame() = %q, want the leaf %q", unlabeled, "leaf")
	}

	// The same plan with AtFrame applied opts in explicitly.
	opted := func(chain []ResolvedCommand, hs []Handlers) []LifecycleStep {
		return []LifecycleStep{
			{Name: "opted", Do: AtFrame(0, func(_ context.Context, rtx *Context) { labeled = rtx.Frame().Name })},
		}
	}
	p2 := NewProgram(fDef(), fProg{}).WithLifecycle(opted).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p2.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if labeled != "root" {
		t.Errorf("AtFrame(0) saw Frame() = %q, want %q", labeled, "root")
	}
}

// TestAtFrame_restoresThePreviousFrame: a hook that drives another hook must not leave the
// Context describing the wrong command.
func TestAtFrame_restoresThePreviousFrame(t *testing.T) {
	var outer, inner, after string
	runF(t, []string{"mid", "leaf"}, func(rtx *Context) {
		outer = rtx.Frame().Name
		AtFrame(0, func(_ context.Context, r *Context) { inner = r.Frame().Name })(context.Background(), rtx)
		after = rtx.Frame().Name
	}, nil)

	if outer != "mid" || inner != "root" || after != "mid" {
		t.Errorf("frames = outer:%q inner:%q after:%q, want mid/root/mid", outer, inner, after)
	}
}

// TestFrame_outsideAHookIsTheLeaf covers a Context from NewContextFor, where no hook is running.
func TestFrame_outsideAHookIsTheLeaf(t *testing.T) {
	rtx := NewContextFor(fDef(), []string{"mid", "leaf"})
	if got := rtx.Frame().Name; got != "leaf" {
		t.Errorf("Frame() outside a hook = %q, want the leaf %q", got, "leaf")
	}
	if _, err := Collect[fLeafSpan](rtx); err != nil {
		t.Errorf("the leaf's own type must collect from a hookless Context: %v", err)
	}
}

// TestIsLeaf answers the question the frame surface could not express: a cascading hook runs at
// every depth, so "is this invocation about ME, or am I an ancestor of it?" is real — and
// rtx.Frame() == rtx.Command() does not compile, because ResolvedCommand holds slices.
func TestIsLeaf(t *testing.T) {
	for _, tc := range []struct {
		argv     []string
		wantMid  bool
		wantRoot bool
	}{
		{[]string{"mid"}, true, false},          // mid IS the invocation
		{[]string{"mid", "leaf"}, false, false}, // both are ancestors of `leaf`
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			var midSaw, rootSaw bool
			runF(t, tc.argv,
				func(rtx *Context) { midSaw = rtx.IsLeaf() },
				func(rtx *Context) { rootSaw = rtx.IsLeaf() },
			)
			if midSaw != tc.wantMid {
				t.Errorf("mid's cascading hook: IsLeaf() = %v, want %v", midSaw, tc.wantMid)
			}
			if rootSaw != tc.wantRoot {
				t.Errorf("root's cascading hook: IsLeaf() = %v, want %v", rootSaw, tc.wantRoot)
			}
		})
	}
}

// TestIsLeaf_isAlwaysTrueInANonCascadingHook: PreRun, Run and PostRun only ever run for the
// leaf, so the answer there is not interesting — but it must not be wrong.
func TestIsLeaf_isAlwaysTrueInANonCascadingHook(t *testing.T) {
	var inRun bool
	p := NewProgram(fDef(), fLeafProbe{capture: func(rtx *Context) { inRun = rtx.IsLeaf() }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf"}); err != nil {
		t.Fatal(err)
	}
	if !inRun {
		t.Error("the leaf's Run reported IsLeaf() = false")
	}
}

// TestIsLeaf_outsideAHook: a Context from NewContextFor has no running hook, so the frame is the
// leaf and the answer is true.
func TestIsLeaf_outsideAHook(t *testing.T) {
	if !NewContextFor(fDef(), []string{"mid", "leaf"}).IsLeaf() {
		t.Error("a hookless Context reported IsLeaf() = false")
	}
}
