package rotini

import (
	"context"
	"errors"
	"testing"
)

// TestReporter_commandPathWithoutDispatch pins what a reporter's rtx.CommandPath() reports
// when the run never reached a handler: the deepest command resolved, so a "Run '<path>
// --help'" hint names a real command.
func TestReporter_commandPathWithoutDispatch(t *testing.T) {
	failing := func(Definition, []string) (Resolution, error) {
		return Resolution{}, errors.New("resolver said no")
	}
	for _, c := range []struct {
		name     string
		argv     []string
		resolver Resolver
		want     string
	}{
		{"misplaced flag", []string{"grp", "-v", "found"}, nil, "demo grp"},
		{"discovered plugin not found", []string{"grp", "nosuch"}, nil, "demo grp"},
		{"declared plugin not found", []string{"sig"}, nil, "demo"},
		{"resolver error without a chain", []string{"grp"}, failing, "demo"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, _ := pluginProgram(pluginHostDef(), c.argv)
			if c.resolver != nil {
				p.WithResolver(c.resolver)
			}
			var path string
			var chain int
			p.WithReporter(func(_ context.Context, rtx *Context, _ Outcome) {
				path, chain = rtx.CommandPath(), len(rtx.CommandChain())
			})
			if _, err := p.Run(p.args); err == nil {
				t.Fatal("run err = nil, want a failure")
			}
			if path != c.want {
				t.Errorf("CommandPath() = %q, want %q", path, c.want)
			}
			if chain == 0 {
				t.Error("CommandChain() is empty")
			}
		})
	}
}
