package rotini

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler_ticksOnInterval(t *testing.T) {
	var ticks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for ticks.Load() < 3 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := NewScheduler().
		Every("t", 2*time.Millisecond, func(context.Context) error { ticks.Add(1); return nil }).
		Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if ticks.Load() < 3 {
		t.Errorf("ticked %d times, want at least 3", ticks.Load())
	}
}

// Without WithRunAtStart the first run waits out the interval; with it, work
// starts immediately.
func TestScheduler_runAtStart(t *testing.T) {
	var ran atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	NewScheduler().WithRunAtStart(true).
		Every("t", time.Hour, func(context.Context) error { ran.Store(true); return nil }).
		Run(ctx)
	if !ran.Load() {
		t.Error("WithRunAtStart did not run the task immediately")
	}

	ran.Store(false)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	NewScheduler().
		Every("t", time.Hour, func(context.Context) error { ran.Store(true); return nil }).
		Run(ctx2)
	if ran.Load() {
		t.Error("the task ran before its first interval elapsed")
	}
}

// The Service contract carries through: the first task error stops the rest and
// is what Run returns.
func TestScheduler_taskErrorStopsScheduler(t *testing.T) {
	boom := errors.New("boom")
	var other atomic.Int32
	err := NewScheduler().WithRunAtStart(true).
		Every("failing", time.Hour, func(context.Context) error { return boom }).
		Every("other", 2*time.Millisecond, func(context.Context) error { other.Add(1); return nil }).
		Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want the task error", err)
	}
}

// A canceled scheduler stopped on request, not in failure.
func TestScheduler_cancellationIsCleanStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := NewScheduler().
		Every("t", time.Millisecond, func(context.Context) error { return nil }).
		Run(ctx); err != nil {
		t.Errorf("canceled Run = %v, want nil", err)
	}
}

func TestScheduler_emptyAndInvalidTasks(t *testing.T) {
	if err := NewScheduler().Run(context.Background()); err != nil {
		t.Errorf("empty scheduler = %v, want nil", err)
	}
	// A non-positive interval or a nil func is ignored rather than spinning.
	s := NewScheduler().
		Every("zero", 0, func(context.Context) error { return nil }).
		Every("nil", time.Second, nil)
	if len(s.tasks) != 0 {
		t.Errorf("registered %d invalid tasks, want 0", len(s.tasks))
	}
}

// Jitter spreads ticks so a fleet started together does not stampede in lockstep.
func TestScheduler_jitterVariesTheInterval(t *testing.T) {
	s := NewScheduler().WithJitter(0.5)
	const interval = 100 * time.Millisecond
	seen := map[time.Duration]bool{}
	for range 50 {
		d := s.next(interval)
		if d < 50*time.Millisecond || d > 150*time.Millisecond {
			t.Fatalf("jittered interval %v is outside ±50%% of %v", d, interval)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Error("jitter produced a constant interval")
	}
	// No jitter is exact, so a scheduler that wants determinism gets it.
	if got := NewScheduler().next(interval); got != interval {
		t.Errorf("next without jitter = %v, want %v", got, interval)
	}
}

func TestScheduler_jitterIsClamped(t *testing.T) {
	if got := NewScheduler().WithJitter(-1).jitter; got != 0 {
		t.Errorf("negative jitter = %v, want 0", got)
	}
	if got := NewScheduler().WithJitter(5).jitter; got >= 1 {
		t.Errorf("jitter = %v, want it clamped below 1", got)
	}
}
