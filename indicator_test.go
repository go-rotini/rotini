package rotini

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a concurrency-safe buffer: the spinner draws from its own goroutine
// while the test reads.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// THE contract that keeps a CI log readable: a "\r"-redrawing indicator writes
// NOTHING to a non-terminal writer.
func TestSpinner_silentOnNonTerminal(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf).WithMessage("working").WithInterval(time.Millisecond).Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	if buf.String() != "" {
		t.Errorf("spinner wrote %q to a non-terminal writer, want nothing", buf.String())
	}
}

func TestProgress_silentOnNonTerminal(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 10)
	p.Set(5)
	p.Done()
	if buf.String() != "" {
		t.Errorf("progress wrote %q to a non-terminal writer, want nothing", buf.String())
	}
}

// WithAnimation overrides the decision in both directions, which is also how the
// drawing itself gets tested without a real terminal.
func TestSpinner_animationForcedOn(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf).WithAnimation(true).WithMessage("working").
		WithInterval(time.Millisecond).WithFrames("A", "B").Start(context.Background())
	time.Sleep(30 * time.Millisecond)
	s.Stop()

	got := buf.String()
	if !strings.Contains(got, "working") {
		t.Errorf("spinner never drew its message: %q", got)
	}
	if !strings.Contains(got, "\r") {
		t.Errorf("spinner did not redraw in place: %q", got)
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Errorf("spinner did not cycle its frames: %q", got)
	}
	// Stop clears the line, so nothing is left behind for the next output.
	if !strings.HasSuffix(got, "\r") {
		t.Errorf("Stop did not clear the line: %q", got)
	}
}

// A spinner must never outlive its run: a done context stops it without Stop.
func TestSpinner_stopsWithContext(t *testing.T) {
	var buf syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	s := NewSpinner(&buf).WithAnimation(true).WithInterval(time.Millisecond).Start(ctx)
	cancel()
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("spinner did not stop after its context was canceled")
	}
}

// Start/Stop are idempotent: a double stop, or a stop on a spinner that never
// started, must not panic or block.
func TestSpinner_idempotentLifecycle(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf).WithAnimation(true).WithInterval(time.Millisecond)
	s.Stop() // never started
	s.Start(context.Background())
	s.Start(context.Background()) // already running
	s.Stop()
	s.Stop()
}

func TestProgress_rendersBarAndPercent(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 10).WithAnimation(true).WithWidth(10).WithMessage("files")
	p.Set(5)
	got := buf.String()
	for _, want := range []string{"=", "50%", "5/10", "files"} {
		if !strings.Contains(got, want) {
			t.Errorf("progress line missing %q: %q", want, got)
		}
	}
}

func TestProgress_addAndClamp(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 10).WithAnimation(true)
	p.Add(4)
	if p.Current() != 4 {
		t.Errorf("Current after Add(4) = %d, want 4", p.Current())
	}
	p.Add(100) // past the total
	if p.Current() != 10 {
		t.Errorf("Current clamped = %d, want the total 10", p.Current())
	}
}

// A total of zero is legal: the fraction is unknowable, so the bar reports the
// count instead of drawing a misleading empty bar.
func TestProgress_indeterminateTotal(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 0).WithAnimation(true)
	p.Set(7)
	got := buf.String()
	if !strings.Contains(got, "7") {
		t.Errorf("indeterminate progress did not report its count: %q", got)
	}
	if strings.Contains(got, "%") {
		t.Errorf("indeterminate progress drew a percentage it cannot know: %q", got)
	}
	if p.ETA() != 0 {
		t.Errorf("ETA with no total = %v, want 0", p.ETA())
	}
}

func TestProgress_rateAndETA(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 100).WithAnimation(true)
	time.Sleep(10 * time.Millisecond)
	p.Set(10)
	if p.Rate() <= 0 {
		t.Errorf("Rate = %v, want a positive rate", p.Rate())
	}
	if p.ETA() <= 0 {
		t.Errorf("ETA = %v, want a positive estimate", p.ETA())
	}
	p.Set(100) // finished
	if p.ETA() != 0 {
		t.Errorf("ETA at completion = %v, want 0", p.ETA())
	}
}

// Done clears the line rather than leaving a finished bar on screen — the
// program reports its own outcome through the funnel.
func TestProgress_doneClearsTheLine(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, 10).WithAnimation(true)
	p.Set(10)
	p.Done()
	if !strings.HasSuffix(buf.String(), "\r") {
		t.Errorf("Done did not clear the line: %q", buf.String())
	}
}

// An indicator owns a line and redraws it with \r. A handler writing to the same stream while
// one is live produces corrupt output, and the audit captured the byte stream:
//
//	\r | fetching \r / fetching done: 0 \n \r -
//
// "fetchingdone: 0" — the frame is never cleared, so the handler's line lands on top of it and
// the next frame lands on top of that. The mutex and the drawn-cells counter that make clearing
// possible are unexported, so this could not be fixed from outside the package.
func TestSpinner_printDoesNotSmearTheLine(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf).WithAnimation(true).WithMessage("fetching").
		WithInterval(time.Millisecond).WithFrames("A").Start(context.Background())

	for i := range 3 {
		s.Print(func() { fmt.Fprintf(&buf, "done: %d\n", i) })
		time.Sleep(2 * time.Millisecond)
	}
	s.Stop()

	got := buf.String()
	for i := range 3 {
		want := fmt.Sprintf("done: %d\n", i)
		if !strings.Contains(got, want) {
			t.Errorf("the handler's line %q never appeared:\n%q", want, got)
		}
		// The smear signature: a frame's text butted directly against the handler's line
		// with no clear between them.
		if strings.Contains(got, "fetchingdone: "+fmt.Sprint(i)) {
			t.Errorf("line %d smeared into the spinner frame:\n%q", i, got)
		}
	}
}

// TestSpinner_printIsSafeWhenNotAnimating: on a pipe or in a test nothing is drawn, so there is
// nothing to clear — but fn must still run, or output would vanish exactly where it is hardest
// to notice.
func TestSpinner_printIsSafeWhenNotAnimating(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf) // no terminal, no WithAnimation
	ran := false
	s.Print(func() { ran = true; fmt.Fprintln(&buf, "plain") })

	if !ran {
		t.Error("Print swallowed fn on a non-animating indicator")
	}
	if got := buf.String(); got != "plain\n" {
		t.Errorf("output = %q, want just the handler's line with no control characters", got)
	}
}

// TestSpinner_printOnAStoppedOrUnstartedSpinner: Print must be as forgiving as Stop.
func TestSpinner_printOnAStoppedOrUnstartedSpinner(t *testing.T) {
	var buf syncBuf
	s := NewSpinner(&buf).WithAnimation(true)
	s.Print(func() { fmt.Fprint(&buf, "before-start ") }) // never started
	s.Start(context.Background())
	s.Stop()
	s.Print(func() { fmt.Fprint(&buf, "after-stop") }) // already stopped
	s.Print(nil)                                       // nil fn

	got := buf.String()
	if !strings.Contains(got, "before-start") || !strings.Contains(got, "after-stop") {
		t.Errorf("Print dropped output outside the running window: %q", got)
	}
}

// TestProgress_printDoesNotSmearTheBar is the same guarantee on the other indicator.
func TestProgress_printDoesNotSmearTheBar(t *testing.T) {
	var buf syncBuf
	p := NewProgress(&buf, 10).WithAnimation(true).WithMessage("copying")
	p.Set(5)
	p.Print(func() { fmt.Fprintln(&buf, "skipped: a") })
	p.Set(6)
	p.Done()

	got := buf.String()
	if !strings.Contains(got, "skipped: a\n") {
		t.Errorf("the handler's line never appeared:\n%q", got)
	}
	if strings.Contains(got, "copyingskipped") {
		t.Errorf("the line smeared into the bar:\n%q", got)
	}
}
