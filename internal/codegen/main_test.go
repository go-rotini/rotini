package codegen

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeGeneratorEnv) != "" {
		os.Exit(fakeGenerator())
	}
	// The go commands tests start build throwaway modules, which a workspace would reject.
	os.Setenv("GOWORK", "off")
	os.Exit(m.Run())
}
