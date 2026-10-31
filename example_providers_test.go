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
