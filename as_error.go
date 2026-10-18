package rotini

import "errors"

// asError is errors.AsType through errors.As, which is linked anyway: each AsType
// instantiation is its own copy of the error-chain walk. A nil err allocates nothing.
func asError[E error](err error) (E, bool) {
	if err == nil {
		var zero E
		return zero, false
	}
	e := new(E)
	ok := errors.As(err, e)
	return *e, ok
}
