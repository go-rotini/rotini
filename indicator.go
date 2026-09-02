package rotini

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Activity indicators — [Spinner] (indeterminate) and [Progress] (determinate) — share one
// single-line renderer: each redraw returns to column 0 with "\r" and repaints, so an
// indicator occupies exactly one line for its whole life.
//
// # Animation and non-terminal output
//
// Redrawing with "\r" smears into one long line in a log file or a pipe, so an indicator
// animates only when its writer is a terminal, decided once at construction from [IsTerminal]
// and false for anything that is not an *os.File. This is the one thing rotini detects by
// default, and it is deliberate: unlike color, which is a presentation choice the program
// should own, a "\r" written into a log file is corruption. [Spinner.WithAnimation] and
// [Progress.WithAnimation] override the decision in both directions.
//
// A non-animating indicator is silent while running and prints nothing on stop; the program
// still reports its outcome through the funnel.

// spinnerFrames is the default frame set: ASCII, so it renders in any terminal
// and any font.
var spinnerFrames = []string{"|", "/", "-", "\\"}

// line is the shared single-line renderer.
type line struct {
	drawMu  sync.Mutex
	w       io.Writer
	animate bool
	drawn   int // widest line drawn so far, so clearing erases all of it
}

// animates reports whether w is a terminal and can therefore be redrawn.
func animates(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && IsTerminal(f)
}

// draw repaints the line in place. It is a no-op when not animating.
func (l *line) draw(text string) {
	l.drawMu.Lock()
	defer l.drawMu.Unlock()
	if !l.animate || l.w == nil {
		return
	}
	l.drawn = max(l.drawn, Width(text))
	fmt.Fprint(l.w, "\r"+text+strings.Repeat(" ", l.drawn-Width(text)))
}

// clear erases the line and returns the cursor to column 0.
func (l *line) clear() {
	l.drawMu.Lock()
	defer l.drawMu.Unlock()
	if !l.animate || l.w == nil || l.drawn == 0 {
		return
	}
	fmt.Fprint(l.w, "\r"+strings.Repeat(" ", l.drawn)+"\r")
	l.drawn = 0
}

// Spinner is an animated activity indicator for work of unknown duration. It is inert until
// [Spinner.Start] and stops at [Spinner.Stop] or when the context is done, so it can never
// outlive its run. On a non-terminal writer it never draws at all.
//
// The zero value is not usable; start from [NewSpinner].
type Spinner struct {
	line

	frames   []string
	interval time.Duration
	message  string
	stop     chan struct{}
	done     chan struct{}
	running  bool
	mu       sync.Mutex
}

// NewSpinner returns a stopped spinner drawing on w.
func NewSpinner(w io.Writer) *Spinner {
	return &Spinner{
		w:        w,
		animate:  animates(w),
		frames:   spinnerFrames,
		interval: 100 * time.Millisecond,
	}
}

// WithFrames replaces the animation frames (default ASCII |/-\).
func (s *Spinner) WithFrames(frames ...string) *Spinner {
	if len(frames) > 0 {
		s.frames = frames
	}
	return s
}

// WithInterval sets the time between frames (default 100ms).
func (s *Spinner) WithInterval(d time.Duration) *Spinner {
	if d > 0 {
		s.interval = d
	}
	return s
}

// WithMessage sets the text drawn beside the spinner.
func (s *Spinner) WithMessage(msg string) *Spinner {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = msg
	return s
}

// WithAnimation forces animation on or off, overriding the terminal decision.
func (s *Spinner) WithAnimation(enabled bool) *Spinner {
	s.drawMu.Lock()
	defer s.drawMu.Unlock()
	s.animate = enabled
	return s
}

// Start begins animating until [Spinner.Stop] or ctx is done. Calling it on a
// running spinner does nothing. It returns the receiver so a caller can
// `defer NewSpinner(w).WithMessage("working").Start(ctx).Stop()`.
func (s *Spinner) Start(ctx context.Context) *Spinner {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return s
	}
	s.running = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})

	go func(stop <-chan struct{}, done chan<- struct{}) {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for i := 0; ; i++ {
			s.mu.Lock()
			msg := s.message
			s.mu.Unlock()
			s.draw(strings.TrimRight(s.frames[i%len(s.frames)]+" "+msg, " "))
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}(s.stop, s.done)
	return s
}

// Message updates the text beside a running spinner.
func (s *Spinner) Message(msg string) {
	s.mu.Lock()
	s.message = msg
	s.mu.Unlock()
}

// Stop halts the animation and clears the line. It is safe to call on a spinner
// that was never started, and safe to call more than once.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	stop, done := s.stop, s.done
	s.mu.Unlock()

	close(stop)
	<-done
	s.clear()
}

// Progress is a determinate progress bar for "n of total" work. Like [Spinner] it draws one
// line in place and only on a terminal, and it is caller-driven: nothing advances until
// [Progress.Set] or [Progress.Add], so there is no goroutine and no timer to leak.
//
// The zero value is not usable; start from [NewProgress].
type Progress struct {
	line

	mu      sync.Mutex
	total   int64
	current int64
	width   int
	message string
	started time.Time
}

// NewProgress returns a progress bar over total units drawn on w. A total of zero
// or less makes the bar indeterminate: it reports counts and rate but draws no
// filled bar (the fraction is unknowable).
func NewProgress(w io.Writer, total int64) *Progress {
	return &Progress{
		w:       w,
		animate: animates(w),
		total:   total,
		width:   30,
		started: time.Now(),
	}
}

// WithWidth sets the bar's width in cells (default 30).
func (p *Progress) WithWidth(n int) *Progress {
	if n > 0 {
		p.mu.Lock()
		p.width = n
		p.mu.Unlock()
	}
	return p
}

// WithMessage sets the text drawn after the bar.
func (p *Progress) WithMessage(msg string) *Progress {
	p.mu.Lock()
	p.message = msg
	p.mu.Unlock()
	return p
}

// WithAnimation forces drawing on or off, overriding the terminal decision.
func (p *Progress) WithAnimation(enabled bool) *Progress {
	p.drawMu.Lock()
	defer p.drawMu.Unlock()
	p.animate = enabled
	return p
}

// Set moves the bar to n units of total and redraws.
func (p *Progress) Set(n int64) {
	p.mu.Lock()
	p.current = n
	if p.total > 0 && p.current > p.total {
		p.current = p.total
	}
	text := p.render()
	p.mu.Unlock()
	p.draw(text)
}

// Add advances the bar by delta units and redraws.
func (p *Progress) Add(delta int64) {
	p.mu.Lock()
	n := p.current + delta
	p.mu.Unlock()
	p.Set(n)
}

// Message updates the text drawn after the bar and redraws.
func (p *Progress) Message(msg string) {
	p.mu.Lock()
	p.message = msg
	text := p.render()
	p.mu.Unlock()
	p.draw(text)
}

// Done clears the line. A progress bar's final state belongs in the program's
// own reported outcome, not in a leftover terminal line.
func (p *Progress) Done() { p.clear() }

// Current returns the units completed so far.
func (p *Progress) Current() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

// Rate returns the completed units per second since construction, or 0 before any
// time has passed.
func (p *Progress) Rate() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rate()
}

// ETA estimates the time remaining from the current rate. It returns 0 when the
// total is unknown, the work is done, or nothing has completed yet.
func (p *Progress) ETA() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	rate := p.rate()
	if p.total <= 0 || rate <= 0 || p.current >= p.total {
		return 0
	}
	return time.Duration(float64(p.total-p.current) / rate * float64(time.Second))
}

// rate computes units/second. The caller holds p.mu.
func (p *Progress) rate() float64 {
	elapsed := time.Since(p.started).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(p.current) / elapsed
}

// render builds the bar line. The caller holds p.mu.
func (p *Progress) render() string {
	var b strings.Builder
	if p.total > 0 {
		filled := min(int(float64(p.width)*float64(p.current)/float64(p.total)), p.width)
		fmt.Fprintf(&b, "[%s%s] %3.0f%% ", strings.Repeat("=", filled),
			strings.Repeat(" ", p.width-filled), float64(p.current)/float64(p.total)*100)
		fmt.Fprintf(&b, "%d/%d", p.current, p.total)
	} else {
		fmt.Fprintf(&b, "%d", p.current)
	}
	if eta := p.etaLocked(); eta > 0 {
		fmt.Fprintf(&b, " eta %s", eta.Round(time.Second))
	}
	if p.message != "" {
		b.WriteString(" ")
		b.WriteString(p.message)
	}
	return b.String()
}

// etaLocked is ETA without re-taking the lock.
func (p *Progress) etaLocked() time.Duration {
	rate := p.rate()
	if p.total <= 0 || rate <= 0 || p.current >= p.total {
		return 0
	}
	return time.Duration(float64(p.total-p.current) / rate * float64(time.Second))
}
