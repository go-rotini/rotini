package rotini

import "errors"

// Category classifies an error by whose fault it is, so one OnError funnel can decide the
// exit code and message style from a single call to [CategoryOf] — rather than every CLI
// re-deriving the taxonomy. rotini tags its
// OWN errors (a missing service is [CategoryInternal]; a parse/bind failure is
// [CategoryUsage]); user code tags its domain errors with [UsageError] / [InternalError].
//
// rotini only labels and reports — it maps NO category to an exit code or message itself
// (Pillar 1). The conventional mapping (usage → 2, internal → 70, success → 0) is the
// funnel's to apply.
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

// ErrUsage and ErrInternal are the sentinels the categories match on. rotini's own
// errors wrap the appropriate one, and [CategoryOf] resolves a category by testing an
// error against them with errors.Is — so a funnel may match either way:
//
//	if errors.Is(err, rotini.ErrUsage) { /* usage */ }
//	switch rotini.CategoryOf(err) { case rotini.CategoryUsage: /* usage */ }
//
// Prefer the [UsageError] / [InternalError] constructors over wrapping these with
// fmt.Errorf("%w", …) directly: the constructors tag the category WITHOUT prepending the
// sentinel's text to your message.
var (
	ErrUsage    = errors.New("rotini: usage error")
	ErrInternal = errors.New("rotini: internal error")
)

// CategoryOf returns the [Category] an error carries — [CategoryUsage] or
// [CategoryInternal] when it (or anything it wraps) matches [ErrUsage] / [ErrInternal],
// else [CategoryNone] (including for a nil error). It is the single classification call a
// funnel makes:
//
//	func onError(ctx context.Context, rtx *rotini.Context, err error) {
//	    switch rotini.CategoryOf(err) {
//	    case rotini.CategoryUsage:    fmt.Fprintln(rtx.Stderr, err); rtx.SignalExit(2)
//	    case rotini.CategoryInternal: report(err); rtx.SignalExit(70)
//	    default:                      fmt.Fprintln(rtx.Stderr, err); rtx.SignalExit(1)
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

// UsageError tags err as a [CategoryUsage] error — bad input the end-user can correct —
// without altering its message: the returned error reads exactly like err but matches
// [ErrUsage] (and so [CategoryOf] reports [CategoryUsage]). errors.Is/As still see through
// to err. It returns nil when err is nil.
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

// categorized tags an error with a category sentinel via multi-unwrap, so errors.Is finds
// both the original error and the sentinel — while Error() delegates to the original, so
// the category adds no text to the message.
type categorized struct {
	err      error
	sentinel error
}

func (c *categorized) Error() string   { return c.err.Error() }
func (c *categorized) Unwrap() []error { return []error{c.err, c.sentinel} }
