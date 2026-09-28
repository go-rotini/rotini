package rotini

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Benchmarks for the paths a CLI runs on every invocation, or runs many times within
// one invocation. `make test-bench` reports them; they exist so a change that makes
// one of these materially worse is visible rather than discovered later.

// Width is called per cell, per column-measuring pass, when aligning a table — so an
// allocation here is an allocation per cell. Unstyled text must cost none.
func BenchmarkWidth_unstyled(b *testing.B) {
	const s = "a moderately long plain cell value"
	b.ReportAllocs()
	for b.Loop() {
		_ = Width(s)
	}
}

func BenchmarkWidth_styled(b *testing.B) {
	s := NewStyle().Bold().ForegroundHex("#cc6666").Sprint("a moderately long styled cell")
	b.ReportAllocs()
	for b.Loop() {
		_ = Width(s)
	}
}

func BenchmarkStrip_unstyled(b *testing.B) {
	const s = "nothing to strip in this string at all"
	b.ReportAllocs()
	for b.Loop() {
		_ = Strip(s)
	}
}

func BenchmarkStrip_styled(b *testing.B) {
	s := NewStyle().Bold().Sprint("some styled text here")
	b.ReportAllocs()
	for b.Loop() {
		_ = Strip(s)
	}
}

func BenchmarkTable_render(b *testing.B) {
	t := NewTable("NAME", "SIZE", "DESCRIPTION")
	for i := range 200 {
		t.Row(fmt.Sprintf("item-%d", i), fmt.Sprintf("%d", i*37), "a description of moderate length")
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = t.Render()
	}
}

func BenchmarkPrinter_json(b *testing.B) {
	rows := make([]benchRow, 200)
	for i := range rows {
		rows[i] = benchRow{Name: fmt.Sprintf("w%d", i), Size: i}
	}
	p := NewPrinter(io.Discard).WithFormat(FormatJSON)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Print(rows)
	}
}

func BenchmarkPrinter_table(b *testing.B) {
	rows := make([]benchRow, 200)
	for i := range rows {
		rows[i] = benchRow{Name: fmt.Sprintf("w%d", i), Size: i}
	}
	p := NewPrinter(io.Discard).WithFormat(FormatTable)
	b.ReportAllocs()
	for b.Loop() {
		_ = p.Print(rows)
	}
}

type benchRow struct {
	Name string `json:"name"`
	Size int    `json:"size"`
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

// Argv parsing is the other per-invocation cost, and the one that grows with the
// number of declared inputs.
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

func mustResolve(b *testing.B, def Definition, argv []string) []ResolvedCommand {
	b.Helper()
	res, err := DefaultResolver(def, argv)
	if err != nil {
		b.Fatal(err)
	}
	return res.Chain
}

// A REPL dispatches once per typed line, so its per-line overhead is the cost of
// being interactive at all.
func BenchmarkREPL_perLine(b *testing.B) {
	h := &testHandlers{log: new([]string)}
	p := NewProgram(testDef(), h).WithStdout(io.Discard).WithStderr(io.Discard)
	line := strings.Repeat("run x\n", 100)
	b.ReportAllocs()
	for b.Loop() {
		r := NewREPL(p).WithPrompt("").WithInput(strings.NewReader(line)).WithOutput(io.Discard)
		if err := r.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

// Run vs RunContext isolates the cost of rotini's signal trap: Run with no supplied
// context installs (and tears down) a signal handler and its goroutine per call,
// which is right for Execute — once per process — and pure overhead for a host that
// dispatches in a loop. RunContext supplies a context, so no trap is installed.
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
