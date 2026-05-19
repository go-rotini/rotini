package rtk

import "os"

// OS is the rtk-default operating-system service. Handlers retrieve it
// via rtk.Get[rtk.OS] for env-var lookups and exit control. Tests bind a
// fake OS under the "os" registry key to capture Exit calls and stub
// env-var reads.
//
// The default implementation satisfies [EnvLookup] so the parser can
// consult it directly for env-fallback flag resolution.
type OS interface {
	// Lookup returns the value bound to key and whether the key is set.
	// Matches the os.LookupEnv signature.
	Lookup(key string) (value string, ok bool)

	// Exit short-circuits the lifecycle with the given exit code. The
	// default implementation panics with [*EarlyExit]; codegen-emitted
	// dispatch code recovers the panic and surfaces the code through the
	// program's exit-code path.
	//
	// This call does not return when invoked on the default OS. Test
	// fakes may record the code and return normally.
	Exit(code int)
}

// NewOS returns the rtk-default [OS] implementation. It reads env vars
// from the process environment and implements Exit by panicking with
// [*EarlyExit].
func NewOS() OS { return defaultOS{} }

// defaultOS is the rtk-default [OS] implementation. It satisfies both
// [OS] and [EnvLookup].
type defaultOS struct{}

// Lookup satisfies [EnvLookup] and the [OS] interface.
func (defaultOS) Lookup(key string) (string, bool) { return os.LookupEnv(key) }

// Exit panics with [*EarlyExit]. The lifecycle layer recovers it and
// surfaces the encoded code through the program's exit-code path.
func (defaultOS) Exit(code int) { panic(&EarlyExit{Code: code}) }

// EarlyExit is the sentinel value panicked by [defaultOS.Exit]. The
// lifecycle layer recovers it and returns it (via [errors.As]) so the
// program's exit-code resolution can pick up the encoded code.
//
// It implements the error interface so [errors.As] / [errors.Is] work
// against an error-returning lifecycle boundary. The name omits the
// conventional "Error" suffix because the value is primarily a
// panic-sentinel; the [error] satisfaction is a secondary affordance.
//
//nolint:errname // see godoc above; named for its sentinel role
type EarlyExit struct {
	// Code is the exit code requested by the handler.
	Code int
}

// Error implements the [error] interface.
func (e *EarlyExit) Error() string { return "rtk: early exit" }
