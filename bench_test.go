package rotini

import (
	"context"
	"io"
	"testing"
)

// Benchmarks for the paths a CLI runs on every invocation, or many times within one.
// `make test-bench` reports them.

// StripANSI runs over every generated man, markdown and completion page; unstyled text should
// cost nothing.
func BenchmarkStripANSI_unstyled(b *testing.B) {
	const s = "nothing to strip in this string at all"
	b.ReportAllocs()
	for b.Loop() {
		_ = StripANSI(s)
	}
}

func BenchmarkStripANSI_styled(b *testing.B) {
	const s = "\x1b[1msome\x1b[0m \x1b[38;5;203mstyled\x1b[0m text here"
	b.ReportAllocs()
	for b.Loop() {
		_ = StripANSI(s)
	}
}

// Dispatch is the cost every invocation pays before a handler runs: resolve the
// chain, build the Context, walk the lifecycle.
func BenchmarkProgram_dispatch(b *testing.B) {
	h := &testHandlers{log: new([]string)}
	p := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard)
	argv := []string{"run", "target", "--count", "3"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.Run(argv); err != nil {
			b.Fatal(err)
		}
	}
}

// Argv parsing is the other per-invocation cost; it grows with the number of declared inputs.
func BenchmarkParser_parse(b *testing.B) {
	h := &testHandlers{log: new([]string)}
	p := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard)
	parser := NewParser()
	b.ReportAllocs()
	for b.Loop() {
		rtx := p.newRunContext()
		rtx.chain = mustResolve(b, testDef(), []string{"run", "target", "--count", "3"})
		rtx.Argv = []string{"run", "target", "--count", "3"}
		var in struct {
			App struct {
				Flags struct {
					Verbose bool `rotini:"verbose"`
				}
				Arguments struct{}
			}
			AppRun struct {
				Flags struct {
					Count int `rotini:"count"`
				}
				Arguments struct {
					Name string   `rotini:"name"`
					Rest []string `rotini:"rest"`
				}
			}
		}
		if err := parser.Parse(rtx, &in); err != nil {
			b.Fatal(err)
		}
	}
}

func mustResolve(b *testing.B, def Definition, argv []string) []Command {
	b.Helper()
	res, err := DefaultResolver(def, argv)
	if err != nil {
		b.Fatal(err)
	}
	return res.Chain
}

// Run vs RunContext isolates the cost of the signal trap: Run with no supplied context
// installs and removes a signal handler and its goroutine per call; RunContext installs none.
func BenchmarkProgram_runVariants(b *testing.B) {
	h := &testHandlers{log: new([]string)}
	p := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard)
	argv := []string{"run", "target"}

	b.Run("Run/traps-signals", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.Run(argv); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("RunContext/no-trap", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.RunContext(ctx, argv); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Run/WithoutSignalHandling", func(b *testing.B) {
		q := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard).WithoutSignalHandling()
		b.ReportAllocs()
		for b.Loop() {
			if _, err := q.Run(argv); err != nil {
				b.Fatal(err)
			}
		}
	})
}
