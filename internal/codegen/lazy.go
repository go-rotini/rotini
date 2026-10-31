package codegen

import "sync"

// lazy holds a value built on first use. Its zero value is ready and costs nothing at init, as
// a package-level sync.OnceValue does in the closures it allocates, so the companion's start-up,
// every completion included, builds none of the package's tables and regexps.
type lazy[T any] struct {
	once sync.Once
	v    T
}

// value returns the value, built by build on the first call.
func (l *lazy[T]) value(build func() T) T {
	l.once.Do(func() { l.v = build() })
	return l.v
}

// lazyErr is [lazy] for a value built with an error.
type lazyErr[T any] struct {
	once sync.Once
	v    T
	err  error
}

// value returns the value and error, built by build on the first call.
func (l *lazyErr[T]) value(build func() (T, error)) (T, error) {
	l.once.Do(func() { l.v, l.err = build() })
	return l.v, l.err
}
