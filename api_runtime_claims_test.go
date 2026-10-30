package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"testing"
	"time"
)

type leakRoot struct{ NoHooks }

func (leakRoot) Run(_ context.Context, rtx *Context) {
	var in struct {
		App struct {
			Flags struct {
				Name string `rotini:"name"`
			}
			Arguments struct{}
		}
	}
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		rtx.HaltWith(err)
		return
	}
	f, err := OpenInput(rtx, "-")
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	_, _ = io.Copy(rtx.Stdout, f)
	_ = f.Close()
	rtx.RecordWarning(errors.New("w"))
}

type leakApp struct{}

func (leakApp) App() Handler { return leakRoot{} }

// settledGoroutines waits for goroutines that are finishing to exit, then counts them.
func settledGoroutines(limit int) int {
	n := runtime.NumGoroutine()
	for deadline := time.Now().Add(2 * time.Second); n > limit && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// The package doc: rotini starts no goroutine that outlives a run, with the default signal
// trap on, a pipe on stdin and the default reporter writing.
func TestAPI_NoGoroutineOutlivesRun(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "name", Identifiers: []string{"--name"}, Type: "string"}},
	}
	// os/signal starts its one receive loop on the first Notify and keeps it for the process.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	signal.Stop(ch)
	before := runtime.NumGoroutine()
	for range 3 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			_, _ = w.WriteString("piped\n")
			_ = w.Close()
		}()
		var out, errOut bytes.Buffer
		code, _ := NewProgram(def, leakApp{}).
			WithStdin(r).WithStdout(&out).WithStderr(&errOut).
			WithEnviron([]string{}).
			Run([]string{"--name", "x"})
		_ = r.Close()
		if code != 0 || out.String() != "piped\n" || !strings.Contains(errOut.String(), "Warning: w") {
			t.Fatalf("run = %d, stdout %q, stderr %q", code, out.String(), errOut.String())
		}

		ctx, cancel := context.WithCancel(t.Context())
		_, _ = NewProgram(def, leakApp{}).
			WithStdin(strings.NewReader("")).WithStdout(io.Discard).WithStderr(io.Discard).
			WithSignals(os.Interrupt).
			RunContext(ctx, nil)
		cancel()
	}
	if after := settledGoroutines(before); after > before {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines %d → %d after the runs returned:\n%s", before, after, buf[:runtime.Stack(buf, true)])
	}
}
