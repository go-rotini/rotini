package rtk

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Progress is rtk's progress-feedback service: it makes [Spinner]s (for work of
// unknown duration) and [Bar]s (for work with a known total), rendered in place on a
// terminal using the ANSI control sequences and degraded to plain, scroll-friendly
// output when the destination is not a terminal (a pipe, a file, NO_COLOR-style logs).
// Built on the same color/level detection as the other UX services.
//
//	// main.go
//	rth.Program.Bind("progress", rtk.NewProgress()).Execute()
//
//	// a handler
//	pg := rotini.MustGet[*rtk.Progress](rtx, "progress")
//	sp := pg.Spinner("building").Start()
//	defer sp.Success("built")
//	bar := pg.Bar(len(items))
//	for range items { work(); bar.Add(1) }
//	bar.Finish()
type Progress struct {
	out   io.Writer
	tty   bool
	level ColorLevel
}

// NewProgress returns a Progress writing to os.Stdout, with the color level detected
// from it, ready to bind under the "progress" registry key. In-place animation is used
// only when the output is a terminal.
func NewProgress() *Progress {
	level, _ := detectFor(os.Stdout)
	return &Progress{out: os.Stdout, tty: writerIsTTY(os.Stdout), level: level}
}

// WithOutput replaces the output writer, re-detecting the terminal-ness and color
// level from it (a non-*os.File, e.g. a test buffer, disables in-place animation).
func (p *Progress) WithOutput(w io.Writer) *Progress {
	p.out = w
	p.tty = writerIsTTY(w)
	p.level, _ = detectFor(w)
	return p
}

// WithColorLevel forces the color level used for spinner/bar styling.
func (p *Progress) WithColorLevel(l ColorLevel) *Progress { p.level = l; return p }

// defaultSpinnerFrames is the braille-dot animation most terminals render well.
var defaultSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner creates a spinner with an initial message; call [Spinner.Start] to begin.
func (p *Progress) Spinner(message string) *Spinner {
	return &Spinner{
		out: p.out, tty: p.tty, level: p.level,
		frames: defaultSpinnerFrames, interval: 80 * time.Millisecond, message: message,
	}
}

// Bar creates a progress bar for work of the given total; update it with
// [Bar.Add]/[Bar.Set] and end it with [Bar.Finish].
func (p *Progress) Bar(total int) *Bar {
	return &Bar{out: p.out, tty: p.tty, level: p.level, width: 30, total: total}
}

// Spinner is an animated, single-line activity indicator. On a terminal it animates a
// frame next to its message in place; off a terminal it prints the message once at
// Start and the final line at Stop/Success/Fail. Start/Stop are idempotent.
type Spinner struct {
	out      io.Writer
	tty      bool
	level    ColorLevel
	frames   []string
	interval time.Duration

	mu      sync.Mutex
	message string
	running bool
	stop    chan struct{}
	wg      sync.WaitGroup
}

// Start begins the spinner (a no-op if already running) and returns it so calls chain.
func (s *Spinner) Start() *Spinner {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return s
	}
	s.running = true
	if !s.tty {
		if s.message != "" {
			_, _ = fmt.Fprintln(s.out, s.message)
		}
		return s
	}
	s.stop = make(chan struct{})
	s.wg.Add(1)
	go s.run()
	return s
}

// Message updates the text shown next to the spinner.
func (s *Spinner) Message(m string) *Spinner {
	s.mu.Lock()
	s.message = m
	s.mu.Unlock()
	return s
}

// Stop ends the spinner, clearing its line on a terminal. It leaves no final message.
func (s *Spinner) Stop() { s.stopWith("") }

// Success ends the spinner with a green "✓ msg" line.
func (s *Spinner) Success(msg string) { s.stopWith(Style("✓", s.level, Green) + " " + msg) }

// Fail ends the spinner with a red "✗ msg" line.
func (s *Spinner) Fail(msg string) { s.stopWith(Style("✗", s.level, Red) + " " + msg) }

func (s *Spinner) stopWith(final string) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	tty, stop := s.tty, s.stop
	s.mu.Unlock()

	if tty {
		if stop != nil {
			close(stop)
		}
		s.wg.Wait()
		_, _ = fmt.Fprint(s.out, "\r"+ClearLine) // erase the spinner line
	}
	if final != "" {
		_, _ = fmt.Fprintln(s.out, final)
	}
}

// run is the animation goroutine (terminal only): redraw on each tick until stopped.
func (s *Spinner) run() {
	defer s.wg.Done()
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for i := 0; ; i++ {
		s.draw(i)
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
	}
}

// draw writes one frame in place (carriage return, content, clear-to-end).
func (s *Spinner) draw(i int) {
	s.mu.Lock()
	frame := s.frames[i%len(s.frames)]
	content := Style(frame, s.level, Cyan) + " " + s.message
	s.mu.Unlock()
	_, _ = fmt.Fprint(s.out, "\r"+content+ClearToLineEnd)
}

// Bar is a determinate progress bar. On a terminal it redraws in place as it advances
// (skipping redundant redraws); off a terminal it stays silent (a bar in a log is
// noise — emit a summary separately). All methods are safe for concurrent use.
type Bar struct {
	out   io.Writer
	tty   bool
	level ColorLevel
	width int
	total int

	mu   sync.Mutex
	cur  int
	last string
	done bool
}

// Set sets the current progress to n (clamped to [0, total]) and redraws.
func (b *Bar) Set(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.cur = n
	b.draw()
}

// Add advances the current progress by n and redraws.
func (b *Bar) Add(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.cur += n
	b.draw()
}

// Finish completes the bar (to total), ends its line on a terminal, and makes further
// updates no-ops.
func (b *Bar) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.cur = b.total
	if b.tty {
		_, _ = fmt.Fprint(b.out, "\r"+renderBar(b.cur, b.total, b.width)+ClearToLineEnd+"\n")
	}
	b.done = true
}

// draw redraws the bar in place when on a terminal and the rendering changed (caller
// holds the lock).
func (b *Bar) draw() {
	if !b.tty {
		return
	}
	s := renderBar(b.cur, b.total, b.width)
	if s == b.last {
		return
	}
	b.last = s
	_, _ = fmt.Fprint(b.out, "\r"+s+ClearToLineEnd)
}

// renderBar formats a bar of the given cell width as "[####----] 42%". A non-positive
// total is treated as 1, and cur is clamped to [0, total].
func renderBar(cur, total, width int) string {
	if total <= 0 {
		total = 1
	}
	if cur < 0 {
		cur = 0
	}
	if cur > total {
		cur = total
	}
	if width < 1 {
		width = 1
	}
	ratio := float64(cur) / float64(total)
	filled := int(ratio * float64(width))
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "] " +
		strconv.Itoa(int(ratio*100)) + "%"
}

// writerIsTTY reports whether w is a terminal (only an *os.File can be).
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTTY(f)
}
