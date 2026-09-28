package rotini

import (
	"context"
	"io"
	"sync"
	"testing"
)

// Frame tracks the lifecycle's progress, which makes it the one thing a handler reads off the
// Context that is not fixed for the run. The Context promises goroutine-safe reads, and that
// promise holds — the frame is mutex-guarded — but "safe" and "unchanging" are different claims,
// and only the first one is true here.
//
// These pin the documented behaviour so it is a contract rather than an accident: a goroutine
// the hook WAITS for sees its spawner's frame; one that outlives the hook sees whatever step is
// running when it looks.

type fgHandlers struct {
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
	cascading func(*Context)
}

func (h fgHandlers) Run(context.Context, *Context) {}
func (h fgHandlers) CascadingPreRun(_ context.Context, rtx *Context) {
	if h.cascading != nil {
		h.cascading(rtx)
	}
}

type fgLeaf struct {
	DefaultHooks
	onRun func(*Context)
}

func (h fgLeaf) Run(_ context.Context, rtx *Context) {
	if h.onRun != nil {
		h.onRun(rtx)
	}
}

type fgProg struct {
	cascading func(*Context)
	onRun     func(*Context)
}

func (p fgProg) App() Handlers    { return fgHandlers{cascading: p.cascading} }
func (p fgProg) AppRun() Handlers { return fgLeaf{onRun: p.onRun} }

// TestFrame_goroutineTheHookWaitsForSeesTheSpawnersFrame is the supported shape: fan out, wait,
// return. Everything the goroutines read is the frame their spawner was in.
func TestFrame_goroutineTheHookWaitsForSeesTheSpawnersFrame(t *testing.T) {
	var seen []string
	var mu sync.Mutex

	p := NewProgram(testDef(), fgProg{cascading: func(rtx *Context) {
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, rtx.Frame().Name)
			}()
		}
		wg.Wait() // the hook does not return until they are done
	}}).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d readings, want 4", len(seen))
	}
	for _, got := range seen {
		if got != "app" {
			t.Errorf("a waited-for goroutine read Frame() = %q, want its spawner's %q", got, "app")
		}
	}
}

// TestFrame_goroutineThatOutlivesItsHookSeesTheCurrentStep is the documented hazard, pinned so
// it cannot change silently in either direction.
//
// It is NOT a data race — run this file under -race and it is clean. The value is simply the
// step running at the moment of the call, which for a detached goroutine is not its spawner's.
// The guidance on Frame is to capture what you need before spawning, and the second half of this
// test is that capture working.
func TestFrame_goroutineThatOutlivesItsHookSeesTheCurrentStep(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	var detached, captured string
	var capturedFrame ResolvedCommand

	p := NewProgram(testDef(), fgProg{
		cascading: func(rtx *Context) {
			capturedFrame = rtx.Frame() // the remedy: read it while the hook still owns the frame
			go func() {
				defer close(done)
				<-release // resume only after the spawning hook has returned
				detached = rtx.Frame().Name
				captured = capturedFrame.Name
			}()
		},
		onRun: func(rtx *Context) {
			close(release)
			<-done
		},
	}).WithStdout(io.Discard).WithStderr(io.Discard)

	if _, err := p.Run([]string{"run", "x"}); err != nil {
		t.Fatal(err)
	}

	// The run has moved on to the leaf by the time the detached goroutine looks.
	if detached != "run" {
		t.Errorf("a detached goroutine read Frame() = %q; the documented behaviour is the current step, %q", detached, "run")
	}
	// The captured value is stable, which is what the doc tells authors to rely on.
	if captured != "app" {
		t.Errorf("the captured frame changed under the caller: %q, want %q", captured, "app")
	}
}
