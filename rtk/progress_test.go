package rtk

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// syncWriter is a goroutine-safe writer for exercising the live spinner under -race.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len()
}

func TestRenderBar(t *testing.T) {
	cases := []struct {
		cur, total, width int
		want              string
	}{
		{0, 10, 10, "[----------] 0%"},
		{5, 10, 10, "[#####-----] 50%"},
		{10, 10, 10, "[##########] 100%"},
		{20, 10, 10, "[##########] 100%"}, // clamped
		{-3, 10, 10, "[----------] 0%"},   // clamped
		{3, 0, 4, "[####] 100%"},          // total<=0 → 1, cur clamps to 1
	}
	for _, c := range cases {
		if got := renderBar(c.cur, c.total, c.width); got != c.want {
			t.Errorf("renderBar(%d,%d,%d) = %q, want %q", c.cur, c.total, c.width, got, c.want)
		}
	}
}

func TestBar_inPlaceRenderAndDedupe(t *testing.T) {
	var buf bytes.Buffer
	b := &Bar{out: &buf, tty: true, level: ColorNone, width: 10, total: 10}

	b.Set(5)
	if want := "\r" + renderBar(5, 10, 10) + ClearToLineEnd; buf.String() != want {
		t.Errorf("Set(5) wrote %q, want %q", buf.String(), want)
	}
	// Re-setting the same visible value is a no-op (no extra write).
	n := buf.Len()
	b.Set(5)
	if buf.Len() != n {
		t.Error("redundant Set redrew the bar")
	}
	// Finish appends the 100% bar and a newline, then ignores further updates.
	buf.Reset()
	b.Finish()
	if want := "\r" + renderBar(10, 10, 10) + ClearToLineEnd + "\n"; buf.String() != want {
		t.Errorf("Finish wrote %q, want %q", buf.String(), want)
	}
	buf.Reset()
	b.Add(1)
	if buf.Len() != 0 {
		t.Error("Add after Finish should be a no-op")
	}
}

func TestBar_nonTTYIsSilent(t *testing.T) {
	var buf bytes.Buffer
	b := &Bar{out: &buf, tty: false, level: ColorNone, width: 10, total: 10}
	b.Set(5)
	b.Add(2)
	b.Finish()
	if buf.Len() != 0 {
		t.Errorf("non-tty bar wrote %q, want nothing", buf.String())
	}
}

func TestSpinner_nonTTYDegrades(t *testing.T) {
	var buf bytes.Buffer
	s := &Spinner{out: &buf, tty: false, level: ColorNone, frames: defaultSpinnerFrames, interval: time.Hour, message: "working"}
	s.Start()
	s.Success("done")
	if want := "working\n✓ done\n"; buf.String() != want {
		t.Errorf("non-tty spinner = %q, want %q", buf.String(), want)
	}
}

func TestSpinner_idempotentStartStop(t *testing.T) {
	var buf bytes.Buffer
	s := &Spinner{out: &buf, tty: false, level: ColorNone, frames: defaultSpinnerFrames, interval: time.Hour, message: "x"}
	s.Start()
	s.Start() // no-op
	s.Stop()
	s.Stop() // no-op — must not panic or double-print
}

func TestSpinner_ttyGoroutineStartStop(t *testing.T) {
	w := &syncWriter{}
	s := &Spinner{out: w, tty: true, level: ColorNone, frames: []string{"a", "b"}, interval: time.Millisecond, message: "x"}
	s.Start()
	time.Sleep(15 * time.Millisecond) // let a few frames animate
	s.Stop()

	// After Stop the goroutine must be gone: no further writes.
	n := w.len()
	time.Sleep(15 * time.Millisecond)
	if w.len() != n {
		t.Error("spinner kept writing after Stop")
	}
	if n == 0 {
		t.Error("spinner produced no output while running")
	}
}
