//go:build race

package rotini

// raceEnabled reports a -race build, whose instrumentation makes allocation counts noisy.
const raceEnabled = true
