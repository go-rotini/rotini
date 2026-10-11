package rotini

import (
	"context"
	"testing"
)

func usageDef() Definition {
	return Definition{
		Name: "taskr", Handler: "Taskr", Usage: "taskr [flags] <command>",
		Commands: []CommandDef{
			{Name: "add", Handler: "Add", Aliases: []string{"a"}, Usage: "taskr add <title> [flags]"},
			{Name: "list", Handler: "List", Usage: "taskr list [--all]"},
		},
	}
}

func TestContextUsage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		argv []string
		want string
	}{
		{nil, "taskr [flags] <command>"},
		{[]string{"add", "x"}, "taskr add <title> [flags]"},
		{[]string{"a", "x"}, "taskr add <title> [flags]"},
		{[]string{"list"}, "taskr list [--all]"},
	}
	for _, c := range cases {
		if got := NewContextFor(usageDef(), c.argv).Usage(); got != c.want {
			t.Errorf("Usage() for %q = %q, want %q", c.argv, got, c.want)
		}
	}
}

// A cascading hook gets its own command's line, not the invoked command's.
func TestContextUsage_cascadingHookFrame(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	fns := map[string]func(context.Context, *Context){
		"Add": func(_ context.Context, rtx *Context) { got["add"] = rtx.Usage() },
	}
	lookup := func(name string) (Handler, bool) {
		if name == "Taskr" {
			return usageRoot{seen: got}, true
		}
		return runFn{fn: fns[name]}, true
	}
	p := NewProgramFunc(usageDef(), lookup).WithoutSignalHandling()
	if code, err := p.Run([]string{"add", "x"}); code != 0 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if got["root"] != "taskr [flags] <command>" || got["add"] != "taskr add <title> [flags]" {
		t.Errorf("usage lines = %v", got)
	}
}

type usageRoot struct {
	NoHooks
	seen map[string]string
}

func (h usageRoot) CascadingPreRun(_ context.Context, rtx *Context) { h.seen["root"] = rtx.Usage() }
func (usageRoot) Run(context.Context, *Context)                     {}

func TestContextUsage_none(t *testing.T) {
	t.Parallel()
	var nilCtx *Context
	if got := nilCtx.Usage(); got != "" {
		t.Errorf("nil Context Usage() = %q", got)
	}
	if got := newContext().Usage(); got != "" {
		t.Errorf("empty chain Usage() = %q", got)
	}
	if got := NewContextFor(Definition{Name: "bare"}, nil).Usage(); got != "" {
		t.Errorf("hand-built Definition without a line: Usage() = %q", got)
	}
}
