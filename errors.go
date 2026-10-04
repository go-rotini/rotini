package rotini

import (
	"context"
	"errors"
	"fmt"
)

// Category classifies an error by whose fault it is, so a reporter can choose the exit code and
// message style from one call to [CategoryOf]. rotini tags its own errors (a missing dependency
// is [CategoryInternal], a parse failure [CategoryUsage]); user code tags domain errors with
// [UsageError] or [InternalError].
//
// A category is a label, not an exit code. The default reporter exits 1 for any recorded error
// or fault; a program that wants distinct codes maps categories in its own reporter.
//
// The constants are ordered by increasing severity (none < usage < internal), so a reporter can
// keep the worst of several with a plain comparison. The ordering is part of the contract; the
// numeric values are not.
type Category int

const (
	// CategoryNone is an unclassified error that rotini cannot attribute.
	CategoryNone Category = iota
	// CategoryUsage is bad input from the end-user: an unknown flag, a missing required
	// argument, a value that fails validation. The user fixes it by changing the command.
	CategoryUsage
	// CategoryInternal is a bug or misconfiguration in the program, such as a missing
	// dependency or a wiring mistake. Only the author can fix it.
	CategoryInternal
)

// String renders the category as a short, stable label.
func (c Category) String() string {
	switch c {
	case CategoryUsage:
		return "usage"
	case CategoryInternal:
		return "internal"
	default:
		return "none"
	}
}

// ErrUsage and ErrInternal are the sentinels the categories match on, so a reporter may branch
// either way:
//
//	if errors.Is(err, rotini.ErrUsage) { /* usage */ }
//	switch rotini.CategoryOf(err) { case rotini.CategoryUsage: /* usage */ }
//
// Prefer the [UsageError] and [InternalError] constructors to wrapping these with fmt.Errorf:
// they tag the category without prepending the sentinel's text to the message.
var (
	ErrUsage    = errors.New("usage error")
	ErrInternal = errors.New("internal error")
)

// CategoryOf returns the [Category] an error carries, or [CategoryNone] when it matches neither
// sentinel:
//
//	cmd.Program.WithReporter(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
//	    worst := rotini.CategoryNone
//	    for _, err := range out.Errors {
//	        fmt.Fprintln(rtx.Stderr, err)
//	        if c := rotini.CategoryOf(err); c > worst {
//	            worst = c
//	        }
//	    }
//	    switch worst {
//	    case rotini.CategoryInternal:
//	        rtx.Exit(70)
//	    case rotini.CategoryUsage:
//	        rtx.Exit(2)
//	    }
//	})
//
// Inside a reporter the lifecycle has already settled, so [Context.HaltWithCode] is a no-op and
// [Context.Exit] is the only way to set a code.
//
// CategoryOf classifies a single error and tests [ErrUsage] first, so an error carrying both
// sentinels (such as the [errors.Join] that [Program.Run] returns for a run that recorded both)
// reports [CategoryUsage]. To classify a whole run, walk out.Errors and keep the most severe
// category, as above.
func CategoryOf(err error) Category {
	switch {
	case err == nil:
		return CategoryNone
	case errors.Is(err, ErrUsage):
		return CategoryUsage
	case errors.Is(err, ErrInternal):
		return CategoryInternal
	default:
		return CategoryNone
	}
}

// UsageError tags err as bad input the end-user can correct, without altering its message: the
// result reads exactly like err but matches [ErrUsage], and errors.Is/As still reach err. It
// returns nil when err is nil.
//
//	if id == "" {
//	    return rotini.UsageError(fmt.Errorf("a widget id is required"))
//	}
func UsageError(err error) error { return categorize(err, ErrUsage) }

// InternalError tags err as a [CategoryInternal] error (a bug or misconfiguration) without
// altering its message. It returns nil when err is nil.
func InternalError(err error) error { return categorize(err, ErrInternal) }

func categorize(err, sentinel error) error {
	if err == nil {
		return nil
	}
	return &categorized{err: err, sentinel: sentinel}
}

// categorized tags an error with a category sentinel via multi-unwrap, so errors.Is finds both
// the original error and the sentinel while Error adds no text to the message.
type categorized struct {
	err      error
	sentinel error
}

func (c *categorized) Error() string   { return c.err.Error() }
func (c *categorized) Unwrap() []error { return []error{c.err, c.sentinel} }

// ExitCause returns a context-cancellation cause that sets the exit code of the run the
// cancellation halts:
//
//	ctx, cancel := context.WithCancelCause(parent)
//	prog.WithContext(ctx)
//	cancel(rotini.ExitCause(3)) // exits 3
//
// A cancellation without an ExitCause also halts the run, and the exit code is resolved as
// usual. Cancellation never preempts a running hook, and teardown always runs. See
// [Program.WithContext].
func ExitCause(code int) error { return exitCodeError{code: code} }

// exitCodeError carries a process exit code as a context-cancellation cause.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string {
	return fmt.Sprintf("run canceled (exit code %d)", e.code)
}

// canceledExitCode returns the code carried by a run context's cancellation cause, or 0 when
// it carries none.
func canceledExitCode(ctx context.Context) int {
	if ec, ok := errors.AsType[exitCodeError](context.Cause(ctx)); ok {
		return ec.code
	}
	return 0
}

// PanicError carries a panic recovered from a lifecycle hook to the reporter: Value is the
// value passed to panic, Stack the goroutine stack captured at recovery. Error renders Value
// alone.
//
// It also carries faults rotini detects without a panic, such as a [*WiringError] or a
// resolver failure, with the error as Value and a nil Stack.
type PanicError struct {
	Value any
	Stack []byte
}

// Error renders the panic value without the stack.
func (e *PanicError) Error() string { return fmt.Sprintf("%v", e.Value) }

// Unwrap returns the panic value when it is an error, followed by [ErrInternal], so a panic
// is [CategoryInternal] unless its error value classifies otherwise ([CategoryOf] tests
// [ErrUsage] first).
func (e *PanicError) Unwrap() []error {
	if err, ok := e.Value.(error); ok {
		return []error{err, ErrInternal}
	}
	return []error{ErrInternal}
}

// WiringError reports that the generated [Definition] and the handler set are out of sync — a
// resolved command names a handler method that does not exist, or whose return value does not
// implement [Handler]. It is always [CategoryInternal].
//
// Command and Handler are empty when [NewProgram] was given a nil handlers value.
type WiringError struct {
	Command string // the command whose handler wiring is broken
	Handler string // the handler method name the Definition referenced
	Msg     string // the human-readable failure
}

// Error renders the mismatch as a single line.
func (e *WiringError) Error() string { return e.Msg }

// Unwrap reports [ErrInternal]: a wiring mismatch is the author's bug, never the user's.
func (e *WiringError) Unwrap() error { return ErrInternal }
