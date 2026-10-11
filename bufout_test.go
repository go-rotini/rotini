package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func bufferedProgram(run func(*Context), stdout io.Writer) (*Program, *bytes.Buffer) {
	var errb bytes.Buffer
	p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: run}).
		WithBufferedOutput(true).WithStdout(stdout).WithStderr(&errb).WithoutSignalHandling()
	return p, &errb
}

func TestBufferedOutput_flushesBeforeTheReporter(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p, _ := bufferedProgram(func(rtx *Context) {
		fmt.Fprintln(rtx.Stdout, "from the handler")
		if out.Len() != 0 {
			t.Errorf("a write reached stdout before the run settled: %q", out.String())
		}
		rtx.RecordSuccess("done")
	}, &out)
	p.WithReporter(func(_ context.Context, rtx *Context, o Outcome) {
		fmt.Fprintln(rtx.Stdout, "from the reporter:", o.Successes[0])
	})
	if code, err := p.Run(nil); code != 0 || err != nil {
		t.Fatalf("Run = %d, %v", code, err)
	}
	if got, want := out.String(), "from the handler\nfrom the reporter: done\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestBufferedOutput_failedFlushIsAnError(t *testing.T) {
	t.Parallel()
	p, errb := bufferedProgram(func(rtx *Context) {
		fmt.Fprintln(rtx.Stdout, "lost")
	}, failingWriter{syscall.ENOSPC})
	code, err := p.Run(nil)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Errorf("Run error = %v, want it to match ENOSPC", err)
	}
	if got := errb.String(); got != "Error: write output: "+syscall.ENOSPC.Error()+"\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestBufferedOutput_failedWriteRecordedOnce(t *testing.T) {
	t.Parallel()
	p, errb := bufferedProgram(func(rtx *Context) {
		// More than the buffer holds, so the write itself fails.
		if _, err := rtx.Stdout.Write(make([]byte, 2*bufferedStdoutSize)); err != nil {
			rtx.RecordError(fmt.Errorf("write the report: %w", err))
		}
	}, failingWriter{syscall.ENOSPC})
	if code, _ := p.Run(nil); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if got := strings.Count(errb.String(), "Error:"); got != 1 {
		t.Errorf("stderr has %d errors, want 1:\n%s", got, errb)
	}
}

func TestBufferedOutput_replacedStdoutStillFlushed(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p, _ := bufferedProgram(func(rtx *Context) {
		fmt.Fprint(rtx.Stdout, "kept")
		rtx.Stdout = io.Discard
	}, &out)
	p.Run(nil)
	if out.String() != "kept" {
		t.Errorf("stdout = %q, want %q", out.String(), "kept")
	}
}

func TestBufferedOutput_recoveredPanicFlushes(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p, _ := bufferedProgram(func(rtx *Context) {
		fmt.Fprint(rtx.Stdout, "before")
		panic("boom")
	}, &out)
	if code, _ := p.Run(nil); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if out.String() != "before" {
		t.Errorf("stdout = %q, want %q", out.String(), "before")
	}
}

func TestBufferedOutput_reraisedPanicFlushes(t *testing.T) {
	t.Parallel()
	for _, teardown := range []bool{true, false} {
		var out bytes.Buffer
		p, _ := bufferedProgram(func(rtx *Context) {
			fmt.Fprint(rtx.Stdout, "before")
			panic("boom")
		}, &out)
		p.WithPanicRecover(false).WithTeardownOnPanic(teardown)
		recovered := func() (r any) {
			defer func() { r = recover() }()
			p.Run(nil)
			return nil
		}()
		if recovered != "boom" {
			t.Errorf("teardown=%v: recovered %v, want the panic", teardown, recovered)
		}
		if out.String() != "before" {
			t.Errorf("teardown=%v: stdout = %q, want %q", teardown, out.String(), "before")
		}
	}
}

func TestBufferedOutput_terminalPassesThrough(t *testing.T) {
	t.Parallel()
	tty := openTerminal(t)
	p, _ := bufferedProgram(func(rtx *Context) {
		if rtx.Stdout != tty {
			t.Errorf("stdout on a terminal is %T, want the terminal itself", rtx.Stdout)
		}
	}, tty)
	p.Run(nil)
}

func TestBufferedOutput_offByDefault(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p, _ := bufferedProgram(func(rtx *Context) {
		if rtx.Stdout != &out {
			t.Errorf("stdout is %T, want the program's own", rtx.Stdout)
		}
	}, &out)
	p.WithBufferedOutput(false).Run(nil)
}

func TestBufferedOutput_fdAndFlush(t *testing.T) {
	t.Parallel()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := newBufferedStdout(f)
	if b.Fd() != f.Fd() {
		t.Errorf("Fd = %d, want the file's %d", b.Fd(), f.Fd())
	}
	if IsTerminal(b) {
		t.Error("a buffered regular file reported a terminal")
	}
	if newBufferedStdout(&bytes.Buffer{}).Fd() != ^uintptr(0) {
		t.Error("a buffer over a non-file has a descriptor")
	}
	fmt.Fprint(b, "x")
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f.Name()); string(data) != "x" {
		t.Errorf("after Flush the file holds %q", data)
	}
}

func TestBufferedOutput_forcedFlushSkipsWhenBusy(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	rtx := newContext()
	rtx.bufOut = newBufferedStdout(&out)
	fmt.Fprint(rtx.bufOut, "pending")
	rtx.bufOut.mu.Lock()
	rtx.flushOutput()
	rtx.bufOut.mu.Unlock()
	if out.Len() != 0 {
		t.Errorf("a busy buffer was flushed: %q", out.String())
	}
	rtx.flushOutput()
	if out.String() != "pending" {
		t.Errorf("an idle buffer was not flushed: %q", out.String())
	}
}

func TestBufferedOutput_concurrentWriters(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p, _ := bufferedProgram(func(rtx *Context) {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 1000 {
					fmt.Fprintln(rtx.Stdout, "line")
				}
			})
		}
		wg.Wait()
	}, &out)
	p.Run(nil)
	if got := strings.Count(out.String(), "line\n"); got != 8000 {
		t.Errorf("wrote %d lines, want 8000", got)
	}
}

// BenchmarkBufferedOutput_2Mlines writes two million short lines to a file, unbuffered and
// buffered.
func BenchmarkBufferedOutput_2Mlines(b *testing.B) {
	for _, buffered := range []bool{false, true} {
		b.Run(fmt.Sprintf("buffered=%v", buffered), func(b *testing.B) {
			f, err := os.Create(filepath.Join(b.TempDir(), "out"))
			if err != nil {
				b.Fatal(err)
			}
			defer f.Close()
			p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: func(rtx *Context) {
				for i := range 2_000_000 {
					fmt.Fprintln(rtx.Stdout, i)
				}
			}}).WithBufferedOutput(buffered).WithStdout(f).WithoutSignalHandling()
			for b.Loop() {
				p.Run(nil)
			}
		})
	}
}
