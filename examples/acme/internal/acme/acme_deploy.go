package acme

// The deploy handler: the Binder reconciling every declared channel — argv,
// $ACME_* environment variables, and the walk-up .acme.yaml config file —
// into one typed struct, and (with --explain) the overlay surface answering
// "where did each value come from".

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

type acmeDeployHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

var _ rotini.CommandHandlers = (*acmeDeployHandlers)(nil)

func (*acmeDeployHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	// One Bind fills every channel: flags (with their env/config fallbacks and
	// the enum/constraint checks over the reconciled values), the required
	// positional, and the env input. A violation anywhere is one usage error.
	binder := rotini.NewBinder(BindMeta)
	var inputs AcmeDeployInputs
	if err := binder.Bind(rtx, &inputs); err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n", err)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}
	d := inputs.AcmeDeploy

	if d.Flags.Explain {
		explain(binder, rtx)
		return
	}

	plan := "deploy"
	if d.Flags.DryRun {
		plan = "plan (dry run)"
	}
	fmt.Fprintf(rtx.Stdout, "%s: %s → %s ×%d", plan, d.Arguments.Service, d.Flags.Env, d.Flags.Replicas)
	if d.Env.Region != "" {
		fmt.Fprintf(rtx.Stdout, " in %s", d.Env.Region)
	}
	fmt.Fprintln(rtx.Stdout)
	if d.Flags.Loud > 0 {
		fmt.Fprintf(rtx.Stdout, "(verbosity %d)\n", d.Flags.Loud)
	}
}

// explain acquires the channels one at a time and overlays them with
// provenance — the opt-in alternative to Bind for programs that want custom
// precedence, or to answer "why is this value what it is".
func explain(binder *rotini.Binder, rtx *rotini.Context) {
	files, err := rotini.ParseFiles[AcmeDeployInputs](binder, rtx)
	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.SignalExit(1)
		return
	}
	env, err := rotini.ParseEnv[AcmeDeployInputs](binder, rtx)
	if err != nil {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
		rtx.SignalExit(1)
		return
	}
	argv, err := rotini.ParseArgv[AcmeDeployInputs](binder, rtx)
	if err != nil {
		fmt.Fprintf(rtx.Stderr, "Error: %v\n", err)
		rtx.SignalExit(rotini.ExitUsage)
		return
	}

	_, report := rotini.OverlayInputsP(files, env, argv) // last layer wins
	for _, field := range report.Fields() {
		win, _ := report.Winner(field)
		fmt.Fprintf(rtx.Stdout, "%s = %s (from %s)\n", field, win.Raw, win.Layer)
	}
}
