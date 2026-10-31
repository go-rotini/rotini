package rotini

import (
	"context"
	"fmt"
	"os"
)

// exampleStore is a dependency a run opens and must close.
type exampleStore struct{ path string }

func (s *exampleStore) Close() error {
	fmt.Println("closed", s.path)
	return nil
}

// exampleCache is a dependency the program owns, shared by every run.
type exampleCache struct{ hits int }

var (
	exampleStoreDep = NewDependency[*exampleStore]("example.store")
	exampleCacheDep = NewDependency[*exampleCache]("example.cache")
)

type exampleProviderApp struct{}

func (exampleProviderApp) App() HandlerE { return exampleProviderCmd{} }

type exampleProviderCmd struct{ NoHooksE }

func (exampleProviderCmd) Run(_ context.Context, rtx *Context) error {
	store, err := rtx.Dependency(exampleStoreDep)
	if err != nil {
		return err
	}
	cache, err := rtx.Dependency(exampleCacheDep)
	if err != nil {
		return err
	}
	cache.hits++
	fmt.Fprintln(rtx.Stdout, "read", store.path, "cache hits:", cache.hits)
	return nil
}

// A provider returns the value, the cleanup that releases it after the run, and an error. The
// store is opened per run and closed by its cleanup; the cache is shared across runs, so its
// provider returns a nil cleanup and it stays open.
func ExampleProgram_WithProvider() {
	cache := &exampleCache{}
	p := NewProgram(Definition{Name: "app", Handler: "App"}, exampleProviderApp{}).
		WithStdout(os.Stdout).
		WithProvider(exampleStoreDep, func(context.Context, *Context) (*exampleStore, func() error, error) {
			s := &exampleStore{path: "todo.db"}
			return s, s.Close, nil
		}).
		WithProvider(exampleCacheDep, func(context.Context, *Context) (*exampleCache, func() error, error) {
			return cache, nil, nil
		})

	for range 2 {
		if _, err := p.Run(nil); err != nil {
			fmt.Println(err)
		}
	}
	// Output:
	// read todo.db cache hits: 1
	// closed todo.db
	// read todo.db cache hits: 2
	// closed todo.db
}

// examplePool is a dependency one process builds once and every run shares.
type examplePool struct{ opened int }

func (p *examplePool) Close() error {
	fmt.Println("pool closed")
	return nil
}

var examplePoolDep = NewDependency[*examplePool]("example.pool")

type examplePoolApp struct{}

func (examplePoolApp) App() HandlerE { return examplePoolCmd{} }

type examplePoolCmd struct{ NoHooksE }

func (examplePoolCmd) Run(_ context.Context, rtx *Context) error {
	pool, err := rtx.Dependency(examplePoolDep)
	if err != nil {
		return err
	}
	fmt.Fprintln(rtx.Stdout, "query on pool", pool.opened)
	return nil
}

// A shared provider builds its value on the first request in any run, and every later run
// reuses it. A host that runs the program repeatedly releases it with Close when it is done.
func ExampleProgram_WithSharedProvider() {
	opens := 0
	p := NewProgram(Definition{Name: "app", Handler: "App"}, examplePoolApp{}).
		WithStdout(os.Stdout).
		WithSharedProvider(examplePoolDep, func(context.Context, *Context) (*examplePool, func() error, error) {
			opens++
			pool := &examplePool{opened: opens}
			return pool, pool.Close, nil
		})
	defer p.Close()

	for range 2 {
		if _, err := p.Run(nil); err != nil {
			fmt.Println(err)
		}
	}
	// Output:
	// query on pool 1
	// query on pool 1
	// pool closed
}
