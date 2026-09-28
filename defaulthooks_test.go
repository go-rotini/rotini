package rotini

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dhHandlers is the shape DefaultHooks exists for: a handler written by hand, behind a spec's
// `handler: {import, convention}`, with only the hook it actually implements written out.
type dhHandlers struct {
	DefaultHooks
	ran *bool
}

func (h *dhHandlers) Run(_ context.Context, rtx *Context) { *h.ran = true }

type dhProgram struct{ ran *bool }

func (p dhProgram) App() Handlers    { return &dhHandlers{ran: p.ran} }
func (p dhProgram) AppRun() Handlers { return &dhHandlers{ran: p.ran} }

// TestDefaultHooks_satisfiesHandlersWithOnlyRun is the whole point: one embed plus Run, and the
// promotion through two levels still produces a complete Handlers.
func TestDefaultHooks_satisfiesHandlersWithOnlyRun(t *testing.T) {
	var ran bool
	p := NewProgram(testDef(), dhProgram{ran: &ran}).WithStdout(io.Discard).WithStderr(io.Discard)
	if code, err := p.Run([]string{"run", "x"}); err != nil || code != 0 {
		t.Fatalf("Run = (%d, %v), want (0, nil)", code, err)
	}
	if !ran {
		t.Error("the handler's Run never fired")
	}
}

// TestDefaultHooks_isTheSameFourNoOps pins that it adds no behaviour of its own — it is an
// assembly of the existing embeds, not a new kind of hook.
func TestDefaultHooks_isTheSameFourNoOps(t *testing.T) {
	var h DefaultHooks
	ctx, rtx := context.Background(), newContext()
	h.CascadingPreRun(ctx, rtx)
	h.PreRun(ctx, rtx)
	h.PostRun(ctx, rtx)
	h.CascadingPostRun(ctx, rtx)

	if !(Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}).Empty() {
		t.Error("a DefaultHooks hook recorded something")
	}
}

// TestDefaultHooks_ownMethodWins: embedding it must not make implementing a hook any different
// from before — a method on the outer type shadows the promoted no-op.
func TestDefaultHooks_ownMethodWins(t *testing.T) {
	log := []string{}
	p := NewProgram(testDef(), dhOverride{log: &log}).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if !contains(log, "PreRun") || !contains(log, "Run") {
		t.Errorf("hooks run = %v, want the declared PreRun to have shadowed the embedded no-op", log)
	}
}

type dhOverride struct{ log *[]string }

func (p dhOverride) App() Handlers    { return &dhOverrideLeaf{log: p.log} }
func (p dhOverride) AppRun() Handlers { return &dhOverrideLeaf{log: p.log} }

type dhOverrideLeaf struct {
	DefaultHooks
	log *[]string
}

func (h *dhOverrideLeaf) PreRun(context.Context, *Context) { *h.log = append(*h.log, "PreRun") }
func (h *dhOverrideLeaf) Run(context.Context, *Context)    { *h.log = append(*h.log, "Run") }

// TestDefaultHooks_stillRequiresRunAtCompileTime is the property that must survive collapsing
// the embeds, and it cannot be asserted from inside a passing test binary — a program that does
// not compile cannot be linked into this one. So it is compiled out of process.
//
// Without it, a handler could embed DefaultHooks, forget Run entirely, satisfy Handlers, and do
// nothing at runtime. The whole reason `rotini generate`'s hook audit skips Run is that the
// compiler owns that case.
func TestDefaultHooks_stillRequiresRunAtCompileTime(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a throwaway package")
	}
	dir := t.TempDir()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module dhcheck\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+root+"\n")
	write("main.go", `package main

import "github.com/go-rotini/rotini"

// DefaultHooks supplies the four no-ops. Run is NOT among them, so this must not compile.
type handlers struct{ rotini.DefaultHooks }

var _ rotini.Handlers = (*handlers)(nil)

func main() {}
`)

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("a handler embedding DefaultHooks with no Run compiled — the compile-time guarantee is gone")
	}
	if !strings.Contains(string(out), "missing method Run") {
		t.Errorf("compile failed, but not by naming the missing Run:\n%s", out)
	}
}
