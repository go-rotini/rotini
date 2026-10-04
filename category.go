package rotini

import "errors"

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
