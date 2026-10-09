package rotini

import (
	"bufio"
	"errors"
	"io"
	"os"
	"sync"
)

// bufferedStdoutSize is the buffer [Program.WithBufferedOutput] puts in front of stdout.
const bufferedStdoutSize = 32 << 10

// bufferedStdout is [Context.Stdout] under [Program.WithBufferedOutput]: a buffer in front of
// the program's stdout, safe for the goroutines a hook starts. The run flushes it when it
// settles and then writes through, so the reporter's output follows the handler's in order.
type bufferedStdout struct {
	mu      sync.Mutex
	dst     io.Writer
	buf     *bufio.Writer
	through bool // settled: writes go straight to dst
}

func newBufferedStdout(dst io.Writer) *bufferedStdout {
	return &bufferedStdout{dst: dst, buf: bufio.NewWriterSize(dst, bufferedStdoutSize)}
}

// Write buffers p, writing the buffer out when it fills.
func (b *bufferedStdout) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.through {
		return b.dst.Write(p)
	}
	return b.buf.Write(p)
}

// Flush writes out what is buffered, so a program can show output before it waits, for
// example before a prompt on stderr:
//
//	if f, ok := rtx.Stdout.(interface{ Flush() error }); ok {
//		if err := f.Flush(); err != nil { … }
//	}
func (b *bufferedStdout) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Flush() //nolint:wrapcheck // a stream: the writer's error, as is
}

// Fd is the descriptor of the stdout this buffers, so [IsTerminal] can check it, or
// ^uintptr(0) when that is not a file.
func (b *bufferedStdout) Fd() uintptr {
	if f, ok := b.dst.(*os.File); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}

// settle flushes the buffer and makes every later write go straight through.
func (b *bufferedStdout) settle() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.through = true
	return b.buf.Flush() //nolint:wrapcheck // wrapped by the caller
}

// tryFlush flushes when no write is in progress, and otherwise gives up: a forced exit
// cannot wait for a handler's goroutine.
func (b *bufferedStdout) tryFlush() {
	if !b.mu.TryLock() {
		return
	}
	defer b.mu.Unlock()
	_ = b.buf.Flush() // best effort: the process is about to exit
}

// settleOutput flushes the run's buffered stdout, if any, and records a failed flush as the
// run's error, unless a handler already recorded that error from a write.
func (rtx *Context) settleOutput() {
	b := rtx.bufferedOut()
	if b == nil {
		return
	}
	err := b.settle()
	if err == nil {
		return
	}
	for _, recorded := range rtx.copyErrors() {
		if errors.Is(recorded, err) {
			return
		}
	}
	rtx.RecordError(&pathError{msg: "write output", err: err, detail: true})
}

// flushOutput writes out the run's buffered stdout, ignoring a failure: the run is ending
// without its reporter (a re-raised panic or a forced exit).
func (rtx *Context) flushOutput() {
	if b := rtx.bufferedOut(); b != nil {
		b.tryFlush()
	}
}

func (rtx *Context) bufferedOut() *bufferedStdout {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.bufOut
}

// WithBufferedOutput buffers [Context.Stdout] for every run, which makes a command that writes
// many small pieces of output much faster (two million lines: about 2.5s unbuffered, 0.06s
// buffered). It is off by default, and WithBufferedOutput(false) turns it off again:
//
//	cmd.NewProgram(cmd.Handlers()).WithBufferedOutput(true).Execute()
//
// When the run ends, before the reporter runs, rotini flushes the buffer and checks the result:
// a write that fails then (a full disk, a closed pipe) is recorded as an error, so the run
// exits non-zero. This happens on every path that reaches the reporter, a halt, a recovered
// panic and a trapped signal included. A panic re-raised by [Program.WithPanicRecover](false)
// flushes first, ignoring a failure; a second signal's forced exit flushes only when no write is
// in progress, so it may drop buffered output.
//
// On a terminal, stdout is not buffered, so prompts and progress show as they are written.
// Under buffering, rtx.Stdout is not an *os.File: use [IsTerminal] to check it, and flush
// before waiting for input or handing the terminal to another program:
//
//	if f, ok := rtx.Stdout.(interface{ Flush() error }); ok {
//		if err := f.Flush(); err != nil {
//			return err
//		}
//	}
//
// Buffered stdout reaches its destination after what was written to stderr in the meantime,
// which shows only when both go to one place (>out 2>&1). Writes made to os.Stdout directly
// bypass the buffer and come out of order: use rtx.Stdout. A subprocess given rtx.Stdout as its
// stdout writes through a pipe into the buffer, and does not see a terminal; give it os.Stdout
// (after a flush) if it needs one. Plugins and completion requests write to the program's
// stdout directly, unbuffered.
func (p *Program) WithBufferedOutput(enabled bool) *Program {
	p.bufferedOutput = enabled
	return p
}
