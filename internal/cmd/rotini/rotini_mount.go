package rotini

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniMountHandler)(nil)

type rotiniMountHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMountHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMountInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	flags := inputs.RotiniMount.Flags
	specs := inputs.RotiniMount.Arguments.Specs
	if len(specs) == 0 && !flags.Prune {
		rtx.HaltWith(rotini.UsageError(errors.New("name a spec to mount, or pass --prune")))
		return
	}
	if err := notDirectories(flags.Into); err != nil {
		rtx.HaltWith(rotini.UsageError(err))
		return
	}

	rtx.SetDependencyIfAbsent(mountDep, codegen.Mount)
	res, err := rtx.MustGetDependency(mountDep)(flags.Into, specs, codegen.MountOptions{
		Under:  strings.Fields(flags.Under),
		Group:  flags.Group,
		Prune:  flags.Prune,
		DryRun: flags.DryRun,
	})
	if err != nil {
		haltWithProblems(rtx, err)
		return
	}

	verb := map[bool][2]string{false: {"mounted", "removed"}, true: {"would mount", "would remove"}}[flags.DryRun]
	var lines []string
	for _, e := range res.Mounted {
		lines = append(lines, fmt.Sprintf("%s %s from %s", verb[0], e.Name, e.Ref))
	}
	for _, ref := range res.Removed {
		lines = append(lines, fmt.Sprintf("%s %s: the file no longer exists", verb[1], ref))
	}
	for _, s := range res.Skipped {
		lines = append(lines, fmt.Sprintf("skipped %s: it is the spec being mounted into", s))
	}
	where := flags.Into
	if under := strings.Join(strings.Fields(flags.Under), " "); under != "" {
		where += ", under " + under
	}
	lines = append(lines, fmt.Sprintf("%s: %d %s, %d already mounted, %d %s",
		where, len(res.Mounted), verb[0], len(res.Already), len(res.Removed), verb[1]))
	for _, line := range lines {
		if _, err := fmt.Fprintln(rtx.Stdout, line); err != nil {
			haltWithWriteError(rtx, err)
			return
		}
	}
	if flags.DryRun && res.Changed() {
		rtx.HaltWithCode(exitWouldChange)
		return
	}
	rtx.HaltWithCode(0)
}
