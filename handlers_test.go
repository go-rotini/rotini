package rotini

import (
	"context"
	"testing"
)

// onlyRun is the common case: embed all four non-Run defaults, supply only Run.
type onlyRun struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	ran bool
}

func (o *onlyRun) Run(ctx context.Context, rtx *Context) { o.ran = true }

// granular embeds individual defaults and overrides one hook (CascadingPreRun) plus
// the mandatory Run — proving the defaults mix and match and that an explicit hook
// shadows a default it does not embed.
type granular struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	preRan bool
}

func (g *granular) CascadingPreRun(ctx context.Context, rtx *Context) { g.preRan = true }
func (g *granular) Run(ctx context.Context, rtx *Context)             {}

// Compile-time proof that both shapes satisfy the lifecycle interface. (A handler that
// embedded the defaults but omitted Run would fail to compile here — the guarantee
// that Run is mandatory.)
var (
	_ CommandHandlers = (*onlyRun)(nil)
	_ CommandHandlers = (*granular)(nil)
)

func TestDefaultHooks_bundleSatisfiesInterfaceAndNoOps(t *testing.T) {
	h := &onlyRun{}
	var iface CommandHandlers = h

	// The four embedded hooks run as harmless no-ops (nil rtx is fine — they ignore it).
	iface.CascadingPreRun(context.Background(), nil)
	iface.PreRun(context.Background(), nil)
	iface.PostRun(context.Background(), nil)
	iface.CascadingPostRun(context.Background(), nil)
	iface.Run(context.Background(), nil)

	if !h.ran {
		t.Error("Run was not invoked through the interface")
	}
}

func TestDefaultHooks_granularAndOverride(t *testing.T) {
	g := &granular{}
	var iface CommandHandlers = g

	// The explicitly-defined CascadingPreRun is used (not a default — none was embedded).
	iface.CascadingPreRun(context.Background(), nil)
	if !g.preRan {
		t.Error("explicit CascadingPreRun did not run")
	}

	// The embedded defaults still no-op without panic.
	iface.PreRun(context.Background(), nil)
	iface.PostRun(context.Background(), nil)
	iface.CascadingPostRun(context.Background(), nil)
}

func TestDefaultHooks_overrideShadowsDefault(t *testing.T) {
	// Embedding the defaults but defining a hook explicitly: the explicit one wins.
	var iface CommandHandlers = &overrider{}
	iface.PreRun(context.Background(), nil)
	if !overriderPreRan {
		t.Error("explicit PreRun did not shadow the embedded DefaultPreRun")
	}
	overriderPreRan = false
}

type overrider struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}

var overriderPreRan bool

func (*overrider) PreRun(ctx context.Context, rtx *Context) { overriderPreRan = true }
func (*overrider) Run(ctx context.Context, rtx *Context)    {}
