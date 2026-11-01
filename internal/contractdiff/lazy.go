package contractdiff

import "sync"

// lazy holds a value built on first use. Its zero value is ready and costs nothing at init, as
// a package-level sync.OnceValue does in the closures it allocates.
type lazy[T any] struct {
	once sync.Once
	v    T
}

// value returns the value, built by build on the first call.
func (l *lazy[T]) value(build func() T) T {
	l.once.Do(func() { l.v = build() })
	return l.v
}
