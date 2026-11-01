package rotini

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.HandlerE = (*rotiniMountHandler)(nil)

type rotiniMountHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMountHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMountInputs]()
	if err != nil {
		return inputError(err)
	}
	flags := inputs.RotiniMount.Flags
	specs := inputs.RotiniMount.Arguments.Specs
	if len(specs) == 0 && !flags.Prune {
		return rotini.UsageError(errors.New("name a spec to mount, or pass --prune"))
	}
	if err := notDirectories(flags.Into); err != nil {
		return rotini.UsageError(err)
	}

	rtx.SetDependencyIfAbsent(mountDep, codegen.Mount)
	res, err := rtx.MustDependency(mountDep)(flags.Into, specs, codegen.MountOptions{
		Under:  strings.Fields(flags.Under),
		Group:  flags.Group,
		Prune:  flags.Prune,
		DryRun: flags.DryRun,
	})
	if err != nil {
		return problems(rtx, err)
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
			return writeError(err)
		}
	}
	if flags.DryRun && res.Changed() {
		rtx.HaltWithCode(exitWouldChange)
		return nil
	}
	rtx.HaltWithCode(0)
	return nil
}
