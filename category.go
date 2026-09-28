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
	ErrUsage    = errors.New("rotini: usage error")
	ErrInternal = errors.New("rotini: internal error")
)

// CategoryOf returns the [Category] an error carries, or [CategoryNone] when it matches
// neither sentinel — the single classification call a funnel makes:
//
//	func onError(ctx context.Context, rtx *rotini.Context, err error) {
//	    switch rotini.CategoryOf(err) {
//	    case rotini.CategoryUsage:    fmt.Fprintln(rtx.Stderr, err); rtx.HaltWithCode(1)
//	    case rotini.CategoryInternal: report(err); rtx.HaltWithCode(70)
//	    default:                      fmt.Fprintln(rtx.Stderr, err); rtx.HaltWithCode(1)
//	    }
//	}
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
