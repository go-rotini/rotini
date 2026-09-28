package rotini

import (
	"context"
	"io"
	"strings"
	"testing"
)

// Collect and the per-channel layer functions must agree about which frames a struct describes.
//
// They did not, for one release of this work: the anchor moved from the leaf to the caller's own
// frame everywhere, but the fit check that came with it was added only to Collect and CollectP.
// ParseArgv, Defaults, ParseEnv and ParseFiles went on accepting a struct that could not describe
// the running command and returning it zeroed with a nil error — the same silent failure the
// anchor work existed to remove, left in the corner of the same API.
//
// These are the tests that would have caught that, so they assert the whole family together
// rather than one function.

type acFlags struct {
	Own  bool `rotini:"own"`
	Help bool `rotini:"help"`
}
type acCmd struct {
	Flags     acFlags
	Arguments struct{}
}

// acDeep describes three commands. The root's hook is one deep, so it can never be right there.
type acDeep struct {
	A acCmd
	B acCmd
	C acCmd
}

// acOwn describes one command — what a root hook legitimately collects.
type acOwn struct{ Root acCmd }

func acDefs(own string) []FlagDef {
	return []FlagDef{
		{Name: own, Identifiers: []string{"--" + own}, Type: "bool"},
		{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"},
	}
}

func acDef() Definition {
	return Definition{
		Name: "root", Handler: "Root", Flags: acDefs("own"),
		Commands: []CommandDef{{Name: "leaf", Handler: "Leaf", Flags: acDefs("leafown")}},
	}
}

type acProg struct{ inRootHook func(*Context) }

func (p acProg) Root() Handlers { return acRootH{probe: p.inRootHook} }
func (p acProg) Leaf() Handlers { return acNoop{} }

type acNoop struct{ DefaultHooks }

func (acNoop) Run(context.Context, *Context) {}

type acRootH struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	probe func(*Context)
}

func (h acRootH) Run(context.Context, *Context) {}
func (h acRootH) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.probe != nil {
		h.probe(rtx)
	}
}

// acEntryPoints is every public way to acquire inputs, so a new one cannot be added without
// deciding what it does here.
func acEntryPoints(rtx *Context) map[string]error {
	_, ec := Collect[acDeep](rtx)
	_, _, ep := CollectP[acDeep](rtx)
	_, ea := ParseArgv[acDeep](rtx)
	_, ed := Defaults[acDeep](rtx)
	_, ee := ParseEnv[acDeep](rtx)
	_, ef := ParseFiles[acDeep](rtx)
	return map[string]error{
		"Collect": ec, "CollectP": ep, "ParseArgv": ea,
		"Defaults": ed, "ParseEnv": ee, "ParseFiles": ef,
	}
}

// TestAnchor_everyEntryPointRejectsATooDeepType is the regression. A struct describing more
// commands than the caller is deep cannot be describing the caller, and every entry point has to
// say so rather than hand back zeros.
func TestAnchor_everyEntryPointRejectsATooDeepType(t *testing.T) {
	var got map[string]error
	p := NewProgram(acDef(), acProg{inRootHook: func(rtx *Context) { got = acEntryPoints(rtx) }}).
		WithStdout(io.Discard).WithStderr(io.Discard)
	p.Run([]string{"--own", "leaf"})

	if got == nil {
		t.Fatal("the root's cascading hook never ran")
	}
	for name, err := range got {
		if err == nil {
			t.Errorf("%s accepted a 3-command type from a hook 1 command deep, silently", name)
			continue
		}
		if !strings.Contains(err.Error(), "acDeep") || !strings.Contains(err.Error(), "ITS OWN") {
			t.Errorf("%s rejected it, but not with the shared explanation: %v", name, err)
		}
	}
}

// TestAnchor_everyEntryPointAcceptsTheCallersOwnType is the other half: the check must not fire
// on the type a hook is supposed to collect, in any of the six.
func TestAnchor_everyEntryPointAcceptsTheCallersOwnType(t *testing.T) {
	var errs map[string]error
	var own bool
	p := NewProgram(acDef(), acProg{inRootHook: func(rtx *Context) {
		in, ec := Collect[acOwn](rtx)
		own = in.Root.Flags.Own
		_, _, ep := CollectP[acOwn](rtx)
		_, ea := ParseArgv[acOwn](rtx)
		_, ed := Defaults[acOwn](rtx)
		_, ee := ParseEnv[acOwn](rtx)
		_, ef := ParseFiles[acOwn](rtx)
		errs = map[string]error{
			"Collect": ec, "CollectP": ep, "ParseArgv": ea,
			"Defaults": ed, "ParseEnv": ee, "ParseFiles": ef,
		}
	}}).WithStdout(io.Discard).WithStderr(io.Discard)
	p.Run([]string{"--own", "leaf"})

	for name, err := range errs {
		if err != nil {
			t.Errorf("%s rejected the hook's own inputs type: %v", name, err)
		}
	}
	if !own {
		t.Error("Collect did not read the root's own --own from its cascading hook")
	}
}
