package codegen

import (
	"bytes"
	"path/filepath"
	"testing"
)

// The contract is the same bytes on every build of the same spec, whatever order Go walks its
// maps in, for every golden spec and the companion's own.
func TestContract_sameBytesEveryBuild(t *testing.T) {
	specs := map[string]string{
		"golden": goldenSpec,
		"facts":  factsSpec,
		"pages":  readTestFile(t, filepath.Join("testdata", "pages", "spec.yaml")),
		"rotini": readTestFile(t, filepath.Join("..", "..", "cmd", "rotini", ".rotini.spec.yaml")),
	}
	shapes, _ := filepath.Glob(filepath.Join("testdata", "stubshapes", "*", "spec.yaml"))
	for _, path := range shapes {
		specs[path] = readTestFile(t, path)
	}
	for name, spec := range specs {
		first, _ := contractJSON(t, spec)
		for range 10 {
			if again, _ := contractJSON(t, spec); !bytes.Equal(first, again) {
				t.Errorf("%s: two builds of the contract differ", name)
				break
			}
		}
	}
}
