package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// stdinState is one run's stdin: read at most once, either whole (slurped, for the declared
// payload and the "-" value sentinels) or as one shared stream. It is keyed by the reader it was
// built for, so a Context whose Stdin changes starts afresh.
//
// The slurped payload is kept raw, as one string, for the rest of the run: each consumer strips
// a byte-order mark and trims as its format requires.
type stdinState struct {
	mu         sync.Mutex
	src        io.Reader
	checked    bool // terminal and direct are decided
	terminal   bool // src is nil or a terminal: reads give nothing
	direct     bool // src never blocks (a regular file or an in-memory reader): no pump needed
	mode       uint8
	text       string // the slurped payload, raw
	err        error
	stream     *cancelReader // the one reader every stream consumer shares
	lineReader *stdinLines   // the one line reader every streamed field shares
	run        func() *stdinRun
	ctx        func() context.Context // the context the reading hook holds, read at read time
}

const (
	stdinUnread uint8 = iota
	stdinSlurped
	stdinStreaming
)

// errStdinStreaming reports a whole-payload read of stdin after a stream consumer has started
// reading it.
var errStdinStreaming = errors.New("stdin is already being read as a stream")

// stdinRun is the run a Context belongs to, as far as a blocking stdin read is concerned: the
// run context, which a trapped signal cancels with the signal's exit code as the cause, and a
// channel closed when the run settles.
type stdinRun struct {
	ctx  context.Context
	done chan struct{}
	once sync.Once
}

// canceled is the run context's Done channel, or nil outside a run.
func (r *stdinRun) canceled() <-chan struct{} {
	if r == nil || r.ctx == nil {
		return nil
	}
	return r.ctx.Done()
}

// cause is the run context's cancellation cause, or nil while it runs.
func (r *stdinRun) cause() error {
	if r == nil || r.ctx == nil || r.ctx.Err() == nil {
		return nil
	}
	return context.Cause(r.ctx) //nolint:wrapcheck // the cause itself, matched with errors.Is
}

// interrupted reports whether err is the run's cancellation cause.
func (r *stdinRun) interrupted(err error) bool {
	cause := r.cause()
	return cause != nil && errors.Is(err, cause)
}

// signalCode is the exit code of the trapped signal that canceled the run when err is that
// cancellation, else 0.
func (r *stdinRun) signalCode(err error) int {
	if !r.interrupted(err) {
		return 0
	}
	if ec, ok := errors.AsType[exitCodeError](r.cause()); ok {
		return ec.code
	}
	return 0
}

func (r *stdinRun) settled() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.done
}

// bindRun ties the Context to its run: a stdin read waiting for data ends when ctx is canceled.
// The runtime calls it once per run, before dispatch.
func (rtx *Context) bindRun(ctx context.Context) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.runState = &stdinRun{ctx: ctx, done: make(chan struct{})}
}

// endStdin marks the run settled: a stdin reader goroutine that is not blocked inside the
// source's Read exits.
func (rtx *Context) endStdin() {
	rtx.mu.RLock()
	r := rtx.runState
	rtx.mu.RUnlock()
	if r != nil {
		r.once.Do(func() { close(r.done) })
	}
}

// resetStdin replaces the Context's stdin with r and discards what was read from the old one.
// A stream already handed out keeps reading the old source.
func (rtx *Context) resetStdin(r io.Reader) {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	rtx.Stdin = r
	rtx.stdinRead = nil
}

// currentRun is the run a blocking read watches; nil outside a run.
func (rtx *Context) currentRun() *stdinRun {
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.runState
}

// stdinState returns the run's stdin state for the current rtx.Stdin, building it on first use
// or when Stdin has changed since.
func (rtx *Context) stdinState() *stdinState {
	rtx.mu.Lock()
	defer rtx.mu.Unlock()
	if rtx.stdinRead == nil || rtx.stdinRead.src != rtx.Stdin {
		rtx.stdinRead = &stdinState{src: rtx.Stdin, run: rtx.currentRun, ctx: rtx.Context}
	}
	return rtx.stdinRead
}

// slurpStdin reads the run's stdin whole, once; every later call gets the same text. A read
// that a trapped signal interrupts returns an [*InputError] whose cause is the run's
// cancellation cause, and stops the run with the signal's exit code.
func (rtx *Context) slurpStdin() (string, error) {
	text, err := rtx.stdinState().slurp()
	rtx.haltOnSignal(err)
	return text, err
}

// haltOnSignal stops the run with the signal's exit code when err is the run's own signal
// cancellation, so a handler that records the error still exits 128+n.
func (rtx *Context) haltOnSignal(err error) {
	if code := rtx.currentRun().signalCode(err); code != 0 {
		rtx.HaltWithCode(code)
	}
}

// flagStdin returns the reader a parse resolves a `from: [stdin]` value against: a replay of the
// run's one whole read of stdin. Nothing is read unless a "-" value asks.
func (rtx *Context) flagStdin() io.Reader {
	if rtx.Stdin == nil {
		return nil
	}
	return &slurpReader{rtx: rtx, state: rtx.stdinState()}
}

// slurpReader replays the slurped stdin, reading it on first use.
type slurpReader struct {
	rtx   *Context
	state *stdinState
	r     *strings.Reader
}

func (r *slurpReader) Read(p []byte) (int, error) {
	if r.r == nil {
		text, err := r.state.slurp()
		if err != nil {
			r.rtx.haltOnSignal(err)
			return 0, err
		}
		r.r = strings.NewReader(text)
	}
	return r.r.Read(p) //nolint:wrapcheck // a replay: io.EOF must reach the caller as is
}

// inspect decides once whether the source is a terminal and whether it can block.
func (s *stdinState) inspect() {
	if s.checked {
		return
	}
	s.checked = true
	switch src := s.src.(type) {
	case nil:
		s.terminal = true
	case *os.File:
		info, err := src.Stat()
		if err != nil {
			return // read anyway; the read reports the problem
		}
		// A character device is an interactive terminal (or /dev/null): the stdin channel is
		// for piped input, not typing.
		s.terminal = info.Mode()&os.ModeCharDevice != 0
		s.direct = info.Mode().IsRegular()
	case *strings.Reader, *bytes.Reader, *bytes.Buffer:
		s.direct = true
	}
}

// slurp reads the source to EOF once and keeps the text; a terminal reads as "". After a stream
// consumer has started reading, it fails.
func (s *stdinState) slurp() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == stdinSlurped {
		return s.text, s.err
	}
	s.inspect()
	if s.terminal {
		s.mode = stdinSlurped
		return "", nil
	}
	if s.stream != nil && s.stream.started.Load() {
		return "", internalBind(channelStdin, "", errStdinStreaming.Error(), errStdinStreaming)
	}
	var b strings.Builder
	_, err := io.Copy(&b, s.streamLocked())
	s.text, s.mode = b.String(), stdinSlurped
	if err != nil {
		s.err = stdinReadError(s.run(), err)
	}
	return s.text, s.err
}

// reader returns the stream every stream consumer shares: after a slurp, a replay of the text;
// otherwise the one cancellable reader over the source. It must be read from one goroutine.
func (s *stdinState) reader() io.Reader {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == stdinSlurped {
		return strings.NewReader(s.text)
	}
	s.inspect()
	if s.terminal {
		return strings.NewReader("")
	}
	s.mode = stdinStreaming
	return s.streamLocked()
}

func (s *stdinState) streamLocked() *cancelReader {
	if s.stream == nil {
		s.stream = &cancelReader{src: s.src, direct: s.direct, run: s.run, ctx: s.ctx}
	}
	return s.stream
}

// stdinReadError turns a failed stdin read into an [*InputError]: an interruption by the run's
// cancellation, or by the reading hook's context ending, keeps the cause; anything else is an
// internal read failure.
func stdinReadError(run *stdinRun, err error) error {
	if _, ended := errors.AsType[readEndedError](err); ended || run.interrupted(err) {
		return usageBind(channelStdin, "", "reading stdin was interrupted", err)
	}
	return internalBind(channelStdin, "", "could not read stdin", err)
}

// stdinChunkSize is how much one pump read asks the source for.
const stdinChunkSize = 64 << 10

type stdinChunk struct {
	buf []byte
	n   int
	err error
}

// cancelReader reads a source that may block forever (a pipe its writer holds open) so that a
// Read waiting for data returns as soon as the run is canceled, or the context the reading hook
// holds ([Context.Context]) ends, with the cancellation cause as its error. A source that never
// blocks, or a read nothing can cancel, is read directly.
// Otherwise a pump goroutine reads chunks into two recycled buffers; it exits when the source
// ends or the run settles, unless it is blocked inside the source's Read, where it stays until
// the source yields.
//
// A cancelReader is not safe for concurrent Reads.
type cancelReader struct {
	src     io.Reader
	direct  bool
	run     func() *stdinRun
	ctx     func() context.Context
	started atomic.Bool // a consumer has read from it

	once   sync.Once
	chunks chan stdinChunk
	free   chan []byte
	cur    []byte // the unread part of the current chunk
	buf    []byte // the current chunk's buffer, recycled once cur is drained
	err    error  // the source's final error (io.EOF at the end)
}

func (r *cancelReader) Read(p []byte) (int, error) {
	r.started.Store(true)
	if len(r.cur) > 0 {
		return r.drain(p), nil
	}
	if r.err != nil {
		return 0, r.err
	}
	run := r.run()
	canceled := run.canceled()
	var ctx context.Context
	var ended <-chan struct{}
	if r.ctx != nil {
		ctx = r.ctx()
		ended = ctx.Done()
	}
	if r.chunks == nil && (r.direct || (canceled == nil && ended == nil)) {
		return r.src.Read(p)
	}
	r.once.Do(func() { r.start(canceled, run.settled()) })
	for {
		select {
		case c, ok := <-r.chunks:
			if !ok {
				return 0, io.EOF
			}
			if c.err != nil {
				r.err = c.err
			}
			if c.n == 0 {
				r.recycle(c.buf)
				if r.err != nil {
					return 0, r.err
				}
				continue
			}
			r.cur, r.buf = c.buf[:c.n], c.buf
			return r.drain(p), nil
		case <-canceled:
			return 0, run.cause()
		case <-ended:
			if cause := run.cause(); cause != nil {
				return 0, cause
			}
			// A deadline or cancel the handler set: the pump keeps running for a later read.
			return 0, readEndedError{context.Cause(ctx)}
		}
	}
}

// readEndedError is a stdin read ended by a context a hook passed to [Context.SetContext], rather
// than by the run's own cancellation. It unwraps to that context's cause.
type readEndedError struct{ cause error }

func (e readEndedError) Error() string { return e.cause.Error() }
func (e readEndedError) Unwrap() error { return e.cause }

// drain copies from the current chunk, recycling its buffer once it is empty.
func (r *cancelReader) drain(p []byte) int {
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	if len(r.cur) == 0 {
		r.recycle(r.buf)
		r.buf = nil
	}
	return n
}

func (r *cancelReader) recycle(buf []byte) {
	if buf == nil {
		return
	}
	select {
	case r.free <- buf:
	default:
	}
}

// start launches the pump for one run.
func (r *cancelReader) start(canceled, settled <-chan struct{}) {
	r.chunks = make(chan stdinChunk)
	r.free = make(chan []byte, 2)
	r.free <- make([]byte, stdinChunkSize)
	r.free <- make([]byte, stdinChunkSize)
	go r.pump(canceled, settled)
}

func (r *cancelReader) pump(canceled, settled <-chan struct{}) {
	for {
		var buf []byte
		select {
		case buf = <-r.free:
		case <-canceled:
			return
		case <-settled:
			return
		}
		n, err := r.src.Read(buf)
		select {
		case r.chunks <- stdinChunk{buf: buf, n: n, err: err}:
		case <-canceled:
			return
		case <-settled:
			return
		}
		if err != nil {
			return
		}
	}
}
