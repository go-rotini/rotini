package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

// referenceDir holds the annotated, exhaustive spec/conf references shipped with
// rotini — the files the README points authors at.
const referenceDir = "reference"

// TestReferenceDocsValidate is the guard that keeps the shipped reference files
// HONEST. They are hand-authored prose whose whole value is being trustworthy, and
// they had silently drifted out of validity twice: they still described a five-way
// `packages` split, a `path:` key, an `initialize:` block, and a root command with no
// `command:` wrapper — none of which the schemas accept any more.
//
// Running the real validate over them means a schema change now BREAKS THE BUILD until
// the documentation is updated with it, which is the only mechanism that has ever kept
// docs in sync.
func TestReferenceDocsValidate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, referenceDir)
	spec := filepath.Join(dir, ".rotini.spec.yaml")
	conf := filepath.Join(dir, ".rotini.conf.yaml")
	for _, p := range []string{spec, conf} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("reference file missing: %v", err)
		}
	}

	// Validate from the module root: the references declare relative paths, and the
	// version guard reads the conf's `version` against the processor's.
	t.Chdir(root)

	var warnings []error
	if err := NewProcessor("0.0.0").Validate(spec, conf, false, "collect",
		func(string, error) {},
		func(w []error) { warnings = append(warnings, w...) },
	); err != nil {
		t.Errorf("the shipped reference documents do not validate — update them with the schema change:\n%v", err)
	}
	for _, w := range warnings {
		t.Logf("reference warning (non-fatal): %v", w)
	}
}
