package rotini

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Scheduler runs tasks on intervals until the context ends.
//
// It is [Service] with timers: each task becomes a worker that ticks, so the
// same rules apply — the first task error stops the rest and is what Run
// returns, and a canceled context is a graceful stop rather than a failure. A
// task that should survive its own failures handles them inside its func.
//
//	rotini.NewScheduler().
//	    Every("reconcile", time.Minute, reconcile).
//	    Every("gc", time.Hour, collectGarbage).
//	    WithJitter(0.1).
//	    Run(ctx)
//
// The zero value is usable: a Scheduler with no tasks returns nil immediately.
type Scheduler struct {
	tasks      []scheduledTask
	jitter     float64
	runAtStart bool
}

type scheduledTask struct {
	name     string
	interval time.Duration
	fn       func(context.Context) error
}

// NewScheduler returns an empty scheduler.
func NewScheduler() *Scheduler { return &Scheduler{} }

// Every registers fn to run every interval. An interval of zero or less is
// ignored. Nothing runs until [Scheduler.Run]. It returns the receiver to chain.
func (s *Scheduler) Every(name string, interval time.Duration, fn func(context.Context) error) *Scheduler {
	if fn != nil && interval > 0 {
		s.tasks = append(s.tasks, scheduledTask{name: name, interval: interval, fn: fn})
	}
	return s
}

// WithJitter spreads each tick by up to fraction of its interval (0.1 = ±10%), so
// a fleet of instances started together does not stampede a shared dependency in
// lockstep. It is clamped to [0, 1). It returns the receiver to chain.
func (s *Scheduler) WithJitter(fraction float64) *Scheduler {
	switch {
	case fraction < 0:
		s.jitter = 0
	case fraction >= 1:
		s.jitter = 0.99
	default:
		s.jitter = fraction
	}
	return s
}

// WithRunAtStart runs every task once immediately instead of waiting out the
// first interval. It returns the receiver to chain.
func (s *Scheduler) WithRunAtStart(enabled bool) *Scheduler { s.runAtStart = enabled; return s }

// Run ticks every task until the context is done or a task fails.
func (s *Scheduler) Run(ctx context.Context) error {
	if len(s.tasks) == 0 {
		return nil
	}
	svc := NewService()
	for _, task := range s.tasks {
		svc.Go(task.name, s.ticker(task))
	}
	return svc.Run(ctx)
}

// ticker turns one task into a Service worker.
func (s *Scheduler) ticker(task scheduledTask) func(context.Context) error {
	return func(ctx context.Context) error {
		if s.runAtStart {
			if err := task.fn(ctx); err != nil {
				return err
			}
		}
		for {
			timer := time.NewTimer(s.next(task.interval))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil // a canceled scheduler stopped on request
			case <-timer.C:
			}
			if err := task.fn(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			}
		}
	}
}

// next is the interval with jitter applied, never less than a millisecond.
func (s *Scheduler) next(interval time.Duration) time.Duration {
	if s.jitter <= 0 {
		return interval
	}
	spread := float64(interval) * s.jitter
	d := time.Duration(float64(interval) - spread + rand.Float64()*2*spread)
	if d < time.Millisecond {
		return time.Millisecond
	}
	return d
}
