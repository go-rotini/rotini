package rotini

import (
	"path/filepath"
	"testing"
)

// runtimeConformanceCases are the matrix rows for the run's directory flag and value origins.
// Their IDs are in the canonical list TestConformance_matrixComplete checks.
func runtimeConformanceCases() []dataCase {
	return []dataCase{
		{"INJ-04", func(t *testing.T) { // -C: config discovery, @file and path checks follow the flag
			base := chdirTree(t)
			seen, code, stderr := runChdir(t, base, "show", "--spec", "@spec.txt", "-C", "sub", "--manifest", "m.yaml")
			sub := filepath.Join(base, "sub")
			f := seen.in.Show.Flags
			if code != 0 || seen.dir != sub || seen.in.Proj.Flags.Dir != sub || f.Name != "from-sub" || f.Spec != "spec-in-sub" {
				t.Errorf("code %d (%s): dir %q, -C %q, name %q, spec %q; want everything read from %s", code, stderr, seen.dir, seen.in.Proj.Flags.Dir, f.Name, f.Spec, sub)
			}
		}},
		{"PREC-06", func(t *testing.T) { // each value names where it came from
			rep := formatFixture(t, "deploy", "--env", "prod")
			for path, want := range map[FieldPath]string{
				"Deploy.Flags.Env":    "argv:--env",
				"Deploy.Flags.Output": "config:project#acme.output",
				"Deploy.Env.Region":   "env:ACME_REGION",
			} {
				if win, _ := rep.Winner(path); win.Origin != want {
					t.Errorf("%s origin = %q, want %q", path, win.Origin, want)
				}
			}
		}},
	}
}

// TestConformance_RuntimeMatrix runs the directory-flag and origin rows of the input
// conformance matrix.
func TestConformance_RuntimeMatrix(t *testing.T) {
	for _, c := range runtimeConformanceCases() {
		t.Run(c.id, c.check)
	}
}
