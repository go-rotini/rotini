package rtk

import "github.com/go-rotini/rotini"

// Get returns the service bound under key as T — the typed, comma-ok form of the
// raw rotini.Context.Value (which returns any). ok is false when no service is
// bound under key or the bound value is not a T:
//
//	parser, ok := rtk.Get[*rtk.Parser](rtx, "parser")
//	if !ok {
//		// not bound — fail the command, or fall back
//	}
//
// It never panics; use [MustGet] to route a missing/wrong-type service through the
// OnError funnel instead of handling it inline.
func Get[T any](rtx *rotini.Context, key string) (T, bool) {
	v, ok := rtx.Value(key).(T)
	return v, ok
}

// MustGet returns the service bound under key as T, or panics with a
// [rotini.ServiceError] (unwrapping to [rotini.ErrServiceNotFound]) when it is
// absent or not a T. The panic is intentional and recoverable: the runtime
// recovers it inside dispatch and routes it through the program's OnError funnel —
// so a handler that cannot run without a service reaches for MustGet instead of
// handling a miss inline:
//
//	parser := rtk.MustGet[*rtk.Parser](rtx, "parser")
//	var in rtg.MycliInputs
//	err := parser.Parse(rtx, &in)
func MustGet[T any](rtx *rotini.Context, key string) T {
	v, ok := Get[T](rtx, key)
	if !ok {
		panic(&rotini.ServiceError{Key: key})
	}
	return v
}
