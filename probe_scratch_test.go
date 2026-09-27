package rotini

import (
	"os"
	"syscall"
	"testing"
)

func TestScratch_more(t *testing.T) {
	h := &testHandlers{log: new([]string)}

	// WithSignals with NO signals — on, or off, or the default set?
	p, _, _ := newTestProgram(h, nil)
	p.WithSignals()
	t.Logf("WithSignals() → mode=%v set=%v (empty set falls back to trapSignals=%v)", p.signalMode, p.signalSet, trapSignals)

	// WithSignals then WithoutSignalHandling — last wins?
	p2, _, _ := newTestProgram(h, nil)
	p2.WithSignals(syscall.SIGHUP).WithoutSignalHandling()
	t.Logf("WithSignals(HUP).WithoutSignalHandling() → mode=%v set=%v", p2.signalMode, p2.signalSet)

	// ...and the other order
	p3, _, _ := newTestProgram(h, nil)
	p3.WithoutSignalHandling().WithSignals(syscall.SIGHUP)
	t.Logf("WithoutSignalHandling().WithSignals(HUP) → mode=%v set=%v", p3.signalMode, p3.signalSet)

	// Bind with an empty key
	p4, _, _ := newTestProgram(h, nil)
	p4.Bind("", "value")
	var seen any
	h.onRun = func(rtx *Context) { seen = rtx.Value("") }
	p4.Run([]string{"run", "x"})
	t.Logf("Bind(\"\", v) → readable as rtx.Value(\"\"): %v", seen)

	_ = os.Interrupt
}
