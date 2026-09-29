package rotini

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// What a custom Resolver or Lifecycle is handed, and what it may do with it. These are the
// expert seams: nothing else in the API lets a caller replace a phase, so nothing else can
// corrupt a Program that serves many runs.

type seamProgram struct{ ran *[]string }

func (p seamProgram) App() Handlers    { return seamHandlers{ran: p.ran} }
func (p seamProgram) AppRun() Handlers { return seamHandlers{ran: p.ran} }

type seamHandlers struct {
	DefaultHooks
	ran *[]string
}

func (h seamHandlers) Run(_ context.Context, rtx *Context) {
	*h.ran = append(*h.ran, rtx.Frame().Name)
}

// TestResolver_definitionIsTheProgramsOwnTree is the hazard the Resolver doc now names.
//
// Definition arrives by value, but it is mostly slices, and those are the program's own. An edit
// through one outlives the run — which for a REPL means every line after the
// first sees a tree the previous line rewrote.
func TestResolver_definitionIsTheProgramsOwnTree(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).WithStdout(io.Discard).WithStderr(io.Discard)

	var secondSaw string
	first := true
	p.WithResolver(func(def Definition, argv []string) (Resolution, error) {
		if first {
			def.Name = "value-field"                   // a value field: local to this copy
			def.Commands[0].Name = "through-the-slice" // a slice element: the program's own
			first = false
		} else {
			secondSaw = def.Name + "/" + def.Commands[0].Name
		}
		return DefaultResolver(def, argv)
	})

	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if !strings.HasPrefix(secondSaw, "app/") {
		t.Errorf("a value field leaked across runs: %q", secondSaw)
	}
	if !strings.HasSuffix(secondSaw, "through-the-slice") {
		t.Fatalf("the fixture no longer demonstrates the hazard: %q", secondSaw)
	}
	// The behaviour is the documented convention, not a bug to fix: deep-copying the tree per
	// run is what the convention exists to avoid. This pins that the doc and the runtime agree.
}

// TestResolver_emptyChainIsReportedNotCrashed: Resolution.Chain is documented non-empty, and a
// custom resolver is the only thing that can break that.
func TestResolver_emptyChainIsReportedNotCrashed(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).
		WithResolver(func(Definition, []string) (Resolution, error) { return Resolution{}, nil }).
		WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code == 0 || err == nil {
		t.Fatalf("an empty chain gave (%d, %v), want a reported failure", code, err)
	}
	if !strings.Contains(err.Error(), "empty chain") {
		t.Errorf("error = %v, want it to name the empty chain", err)
	}
	if len(ran) != 0 {
		t.Errorf("handlers ran on an empty chain: %v", ran)
	}
}

// TestResolver_errorIsRoutedNotPanicked.
func TestResolver_errorIsRoutedNotPanicked(t *testing.T) {
	p := NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithResolver(func(Definition, []string) (Resolution, error) {
			return Resolution{}, errors.New("resolver said no")
		}).WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code == 0 || err == nil || !strings.Contains(err.Error(), "resolver said no") {
		t.Errorf("(%d, %v), want the resolver's error routed through the funnel", code, err)
	}
}

// TestLifecycle_nilDoIsATeardownOnlyStep: nil Undo was documented and safe; nil Do was a nil
// dereference reported as "invalid memory address", which is an opaque diagnostic for a plain
// wiring mistake — and it ruled out a legitimate shape, a teardown that pairs with no setup.
func TestLifecycle_nilDoIsATeardownOnlyStep(t *testing.T) {
	var order []string
	p := NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithLifecycle(func(chain []ResolvedCommand, hs []Handlers) []LifecycleStep {
			return []LifecycleStep{
				{Name: "teardown-only", Undo: func(context.Context, *Context) {
					order = append(order, "undo")
				}},
				{Name: "work", Do: func(context.Context, *Context) {
					order = append(order, "do")
				}},
			}
		}).WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code != 0 || err != nil {
		t.Fatalf("(%d, %v), want a clean run", code, err)
	}
	if strings.Join(order, ",") != "do,undo" {
		t.Errorf("order = %v, want the work then the teardown-only step's Undo", order)
	}
}

// TestLifecycle_emptyPlanIsACleanNoOp.
func TestLifecycle_emptyPlanIsACleanNoOp(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).
		WithLifecycle(func([]ResolvedCommand, []Handlers) []LifecycleStep { return nil }).
		WithStdout(io.Discard).WithStderr(io.Discard)

	if code, err := p.Run([]string{"run", "x"}); code != 0 || err != nil {
		t.Errorf("(%d, %v), want a plan with no steps to do nothing, quietly", code, err)
	}
	if len(ran) != 0 {
		t.Errorf("an empty plan still ran %v", ran)
	}
}
