package rotini

import (
	"os"
	"time"
)

// WithTerminationTimeout bounds how long a run may take to finish after the first trapped
// signal halts it. When the run hasn't returned within d (teardown and the reporter included),
// the exit action is called with the signal's exit code (128+n), as a second signal does:
// buffered stdout is flushed on a best-effort basis, the remaining teardown doesn't run, and
// the reporter may not have printed.
//
// d <= 0 restores the default: wait for teardown, or for a second signal. It has no effect when
// rotini installs no signal trap (see [Program.WithSignals] and
// [Program.WithoutSignalHandling]).
func (p *Program) WithTerminationTimeout(d time.Duration) *Program {
	p.terminationTimeout = max(d, 0)
	return p
}

// WithArgv0 sets the name the program was invoked as (default os.Args[0]), which a spec that
// declares `multicall` dispatches on: a binary linked as `ls` runs the `ls` command. Only that
// dispatch reads it, so tests and embedding hosts set it to exercise multicall. "" restores
// the default.
func (p *Program) WithArgv0(name string) *Program {
	if name == "" {
		name = invokedName()
	}
	p.argv0 = name
	return p
}

// invokedName is the process's argv[0], "" when there is none.
func invokedName() string {
	if len(os.Args) == 0 {
		return ""
	}
	return os.Args[0]
}
