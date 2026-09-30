package rotini

import "errors"

// The error taxonomy: whose fault an error is, and the sentinels every rotini error
// type unwraps to so a funnel can classify one with a single call.

// Category classifies an error by whose fault it is, so a funnel can decide the exit code and
// message style from one call to [CategoryOf]. rotini tags its own errors — a missing service
// is [CategoryInternal], a parse or bind failure [CategoryUsage] — and user code tags its
// domain errors with [UsageError] or [InternalError].
//
// rotini labels; the funnel decides what to do with the label. There are no named exit-code
// constants and no forced category→code mapping: the default funnel exits 1 for any recorded
// error or fault, and a program that wants distinct codes maps them in its own funnel.
//
// The constants are declared in increasing severity — none < usage < internal — so a funnel
// summarizing several errors can keep the worst with a plain comparison. That ordering is part
// of the contract; the numbers are not.
type Category int

const (
	// CategoryNone is an unclassified error — a plain error rotini cannot attribute.
	CategoryNone Category = iota
	// CategoryUsage is bad input from the end-user: an unknown flag, a missing required
	// argument, a value that fails validation. The user can fix it by changing the command.
	CategoryUsage
	// CategoryInternal is a bug or misconfiguration in the program: a missing bound
	// service, a wiring mistake. The end-user cannot fix it; the author must.
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

// ErrUsage and ErrInternal are the sentinels the categories match on, so a funnel may branch
// either way:
//
//	if errors.Is(err, rotini.ErrUsage) { /* usage */ }
//	switch rotini.CategoryOf(err) { case rotini.CategoryUsage: /* usage */ }
//
// Prefer the [UsageError] and [InternalError] constructors over wrapping these with fmt.Errorf
// directly: they tag the category without prepending the sentinel's text to your message.
var (
	ErrUsage    = errors.New("usage error")
	ErrInternal = errors.New("internal error")
)

// CategoryOf returns the [Category] an error carries, or [CategoryNone] when it matches neither
// sentinel — the single classification call a funnel makes:
//
//	cmd.Program.WithFunnel(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
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
// Note [Context.Exit] rather than [Context.HaltWithCode]: inside a funnel the lifecycle has
// already settled, so HaltWithCode is a no-op and Exit is the only way to claim a code.
//
// # It answers for ONE error, and usage wins a tie
//
// An error can carry both sentinels — [errors.Join] of a user's bad input and an internal bug is
// exactly what [Program.Run] returns for a run that recorded both. CategoryOf tests [ErrUsage]
// first, so such a value reports [CategoryUsage].
//
// That is the right answer for a single error and a poor summary of a whole run: "the user can
// fix this" is misleading when a bug is also in the pile. A funnel classifying a run should walk
// out.Errors and keep the MOST SEVERE category, as above — the constants are ordered
// none < usage < internal so that a comparison does it.
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
// result reads exactly like err but matches [ErrUsage], and errors.Is/As still see through to
// err. It returns nil when err is nil.
//
//	if id == "" {
//	    return rotini.UsageError(fmt.Errorf("a widget id is required"))
//	}
func UsageError(err error) error { return categorize(err, ErrUsage) }

// InternalError tags err as a [CategoryInternal] error — a bug or misconfiguration —
// without altering its message. It returns nil when err is nil.
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
