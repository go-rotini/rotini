package rotinitest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/rotinitest"
)

type sharedConn struct{ id int }

var sharedConnDep = rotini.NewDependency[*sharedConn]("tool.conn")

// newSharedTool builds a one-command program whose shared provider numbers each build and
// whose cleanup appends to closed.
func newSharedTool(builds *int, closed *[]int) *rotini.Program {
	def := rotini.Definition{Name: "tool", Handler: "Tool"}
	return rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) {
		return &handler{run: func(rtx *rotini.Context) {
			fmt.Fprint(rtx.Stdout, rtx.MustDependency(sharedConnDep).id)
		}}, true
	}).WithSharedProvider(sharedConnDep, func(context.Context, *rotini.Context) (*sharedConn, func() error, error) {
		*builds++
		c := &sharedConn{id: *builds}
		return c, func() error { *closed = append(*closed, c.id); return nil }, nil
	})
}

// A program reused across runs keeps its shared value; a fresh program builds its own; Run
// releases nothing, so the test closes the program itself.
func TestRunSharedProvider(t *testing.T) {
	var builds int
	var closed []int
	p := newSharedTool(&builds, &closed)
	first := rotinitest.RunArgv(t, p, nil)
	second := rotinitest.RunArgv(t, p, nil)
	if string(first.Stdout) != "1" || string(second.Stdout) != "1" || builds != 1 || len(closed) != 0 {
		t.Fatalf("stdout %q then %q after %d builds, closed %v; want one shared value, still open",
			first.Stdout, second.Stdout, builds, closed)
	}
	fresh := rotinitest.RunArgv(t, newSharedTool(&builds, &closed), nil)
	if string(fresh.Stdout) != "2" {
		t.Errorf("a fresh program's stdout = %q, want its own value", fresh.Stdout)
	}
	if err := p.Close(); err != nil || len(closed) != 1 || closed[0] != 1 {
		t.Errorf("Close = %v, closed %v; want the reused program's value released", err, closed)
	}
}
