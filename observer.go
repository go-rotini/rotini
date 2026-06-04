package rotini

import "time"

// ObserverPhase identifies which of the runtime's control-flow transitions an [Event]
// reports.
type ObserverPhase int

const (
	// PhaseResolved fires once after the command path is resolved from argv, before any
	// handler runs. [Event.Path] is the resolved path (root→leaf); [Event.Command] is the leaf.
	PhaseResolved ObserverPhase = iota
	// PhaseRemoteExec fires when resolution selects a remote or discovered-plugin
	// sub-command, exec'd instead of dispatching a local lifecycle. [Event.Command] is its name.
	PhaseRemoteExec
	// PhaseHookStart fires just before a lifecycle hook runs. [Event.Command] and [Event.Hook]
	// name it.
	PhaseHookStart
	// PhaseHookEnd fires after a lifecycle hook returns. [Event.Duration] is its wall-clock;
	// [Event.Err] is non-nil when the hook panicked.
	PhaseHookEnd
	// PhaseExit fires once when the run settles its exit code. [Event.Code] is that code;
	// [Event.Command] is the leaf (or the remote's name).
	PhaseExit
)

// Event is the structured data the runtime passes to an [Observer] at each control-flow
// transition. It surfaces the framework-internal flow a handler cannot observe from its
// own hooks: how argv resolved, which hook ran and for how long, and the exit code the
// lifecycle settled on. Fields not relevant to the [Event.Phase] are zero.
type Event struct {
	Phase    ObserverPhase // which transition this is
	Command  string        // the command this event concerns
	Path     []string      // resolved command path, root→leaf (PhaseResolved)
	Hook     string        // lifecycle hook name, e.g. "CascadingPreRun" (PhaseHookStart / PhaseHookEnd)
	Duration time.Duration // hook wall-clock (PhaseHookEnd)
	Code     int           // settled exit code (PhaseExit)
	Err      error         // panic recovered in the hook (PhaseHookEnd)
}

// Observer receives an [Event] at each of the runtime's control-flow transitions, when one
// is set via [Program.WithObserver]. It is rotini's observability seam: the runtime emits
// structured DATA and the Observer decides what to do with it — write a slog line, open an
// OpenTelemetry span, increment a counter, or nothing. rotini logs nothing itself and picks
// no format, level, or destination (Pillar 1: data + a seam, never a framework-initiated
// write or an injected --verbose flag); with no Observer set the runtime emits nothing and
// pays no cost.
//
// It is the lifecycle-wide generalization of [Program.OnError], which remains where the
// error itself is handled. The Observer runs synchronously on the dispatch goroutine, so it
// must be fast and must not block; a panic inside it is the caller's bug and is NOT recovered
// into OnError.
type Observer func(Event)

// chainPath is the resolved command names, root→leaf — the [Event.Path] for PhaseResolved.
func chainPath(chain []ResolvedCommand) []string {
	path := make([]string, len(chain))
	for i, c := range chain {
		path[i] = c.Name
	}
	return path
}
