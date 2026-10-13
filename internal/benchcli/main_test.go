package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain lets the test binary stand in for a benchmarked CLI: with BENCHCLI_EXIT set it
// writes a usage error and exits with that status.
func TestMain(m *testing.M) {
	if code := os.Getenv("BENCHCLI_EXIT"); code != "" {
		os.Stderr.WriteString("Error: unknown flag --nope\n")
		if code == "0" {
			os.Exit(0)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// A case whose command fails is an error, not a fast timing.
func TestTimeRunsRefusesAFailingCommand(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCHCLI_EXIT", "2")
	if _, err := timeRuns(bin, []string{"--nope"}, 3, 1); err == nil || !strings.Contains(err.Error(), "unknown flag --nope") {
		t.Errorf("err = %v, want the command's failure", err)
	}
	t.Setenv("BENCHCLI_EXIT", "0")
	if ds, err := timeRuns(bin, nil, 3, 1); err != nil || len(ds) != 3 {
		t.Errorf("timeRuns = %v, %v", ds, err)
	}
}
