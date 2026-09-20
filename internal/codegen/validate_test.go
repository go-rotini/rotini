package codegen

import (
	"errors"
	"strings"
	"testing"
)

// validateInModule runs the real Validate workflow over a spec+conf pair in a temp
// module, returning the fatal error (nil when the documents are clean) and the
// non-fatal warnings.
func validateInModule(t *testing.T, spec, conf, failMode string) (err error, warnings []error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/v\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	err = NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, failMode,
		func(string, error) {},
		func(w []error) { warnings = append(warnings, w...) })
	return err, warnings
}

// TestValidate_accepts pins the happy path of the `rotini validate` command: a
// schema-valid, lint-clean pair passes with no error and no warnings.
func TestValidate_accepts(t *testing.T) {
	err, warnings := validateInModule(t, goldenSpec, goldenConf, "")
	if err != nil {
		t.Errorf("Validate(valid documents) = %v, want nil", err)
	}
	if len(warnings) != 0 {
		t.Errorf("Validate(valid documents) warnings = %v, want none", warnings)
	}
}

// TestValidate_reportsSchemaProblemsWithPosition covers the reporting contract that
// makes validation usable: a rejected value names its source line:col, not just a
// JSON pointer.
func TestValidate_reportsSchemaProblemsWithPosition(t *testing.T) {
	const bad = `version: 0.0.0
command:
  name: demo
  bogus_key: nope
`
	err, _ := validateInModule(t, bad, goldenConf, "")
	if err == nil {
		t.Fatal("Validate(unknown command key) = nil, want an error")
	}
	got := err.Error()
	if !strings.Contains(got, ".rotini.spec.yaml:") {
		t.Errorf("problem %q does not name the source file:line:col", got)
	}
	if !strings.Contains(got, "spec:") {
		t.Errorf("problem %q is not tagged with its document kind", got)
	}
}

// TestValidate_failFastStopsAtFirst covers the one configurable knob: `collect`
// (the default) reports every problem, `fast` stops at the first.
func TestValidate_failFastStopsAtFirst(t *testing.T) {
	const twoProblems = `version: 0.0.0
command:
  name: demo
  bogus_one: nope
  bogus_two: nope
`
	collectErr, _ := validateInModule(t, twoProblems, goldenConf, "collect")
	fastErr, _ := validateInModule(t, twoProblems, goldenConf, "fast")
	if collectErr == nil || fastErr == nil {
		t.Fatalf("both modes must fail: collect=%v fast=%v", collectErr, fastErr)
	}
	if collectLines, fastLines := strings.Count(collectErr.Error(), "\n"), strings.Count(fastErr.Error(), "\n"); collectLines <= fastLines {
		t.Errorf("collect reported %d extra lines, fast %d — collect must report more", collectLines, fastLines)
	}
}

// TestValidate_confProblemsAreTagged proves the conf half is validated too, and that
// a conf finding is distinguishable from a spec finding.
func TestValidate_confProblemsAreTagged(t *testing.T) {
	const badConf = `version: 0.0.0
generate:
  packages:
    - type: not-a-target
      file: internal/cmd/demo/zz_demo.go
`
	err, _ := validateInModule(t, goldenSpec, badConf, "")
	if err == nil {
		t.Fatal("Validate(bad conf) = nil, want an error")
	}
	if got := err.Error(); !strings.Contains(got, "conf:") {
		t.Errorf("conf problem %q is not tagged as a conf finding", got)
	}
}

// TestProblem_ErrorAndUnwrap pins the finding type itself: how it renders with and
// without a source position, and that a typed cause stays errors.As-reachable through
// it (so a caller can branch on the cause, not the message).
func TestProblem_ErrorAndUnwrap(t *testing.T) {
	positioned := &problem{kind: "spec", loc: "/command/name", pos: "spec.yaml:3:9", msg: "bad"}
	if got, want := positioned.Error(), "spec: spec.yaml:3:9: /command/name: bad"; got != want {
		t.Errorf("positioned problem = %q, want %q", got, want)
	}
	bare := &problem{kind: "conf", loc: "command app deploy", msg: "bad"}
	if got, want := bare.Error(), "conf: command app deploy: bad"; got != want {
		t.Errorf("unpositioned problem = %q, want %q", got, want)
	}

	sentinel := errors.New("underlying")
	withCause := &problem{kind: "spec", loc: "/x", msg: "bad", cause: sentinel}
	if !errors.Is(withCause, sentinel) {
		t.Error("a problem's typed cause is not reachable via errors.Is")
	}
	if bare.Unwrap() != nil {
		t.Error("a problem with no cause must unwrap to nil")
	}
}

// ── version + fail-mode ─────────────────────────────────────.

// TestVersionProblem pins the compatibility rule a document's `version` expresses: a
// MINIMUM, not an equality. The exact-match rule this replaced meant every rotini patch
// release invalidated every spec and conf in every project until each was hand-edited —
// the whole table below, rows 3 through 6, used to be errors.
func TestVersionProblem(t *testing.T) {
	cases := []struct {
		name        string
		doc, binary string
		wantProblem bool
	}{
		{"exact match", "1.2.3", "1.2.3", false},
		{"v prefix on the binary", "1.2.3", "v1.2.3", false},

		// Same major, binary at or ahead of the document: the point of the change.
		{"binary a patch ahead", "1.2.3", "1.2.9", false},
		{"binary a minor ahead", "1.2.3", "1.4.0", false},
		{"binary far ahead, same major", "1.0.0", "1.99.99", false},
		{"document at zero, binary ahead", "0.0.0", "0.4.1", false},

		// Binary behind the document: it may not know the keys the document uses.
		{"binary a patch behind", "1.2.3", "1.2.2", true},
		{"binary a minor behind", "1.4.0", "1.2.9", true},

		// Different majors, either direction.
		{"document major behind", "1.0.0", "2.0.0", true},
		{"document major ahead", "2.0.0", "1.9.9", true},

		// Unknown on either side is never judged.
		{"doc empty skips", "", "2.0.0", false},
		{"binary empty skips", "1.0.0", "", false},
		{"dev build skips", "1.0.0", "dev", false},
		{"non-semver doc skips", "not-a-version", "1.0.0", false},

		// Pre-release and build metadata are ignored, not rejected.
		{"binary prerelease of the same version", "1.2.3", "1.2.3-rc.1", false},
		{"binary prerelease ahead", "1.2.3", "1.3.0-rc.1", false},
		{"binary build metadata", "1.2.3", "1.2.3+deadbeef", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionProblem("spec", tc.doc, tc.binary)
			if (got != nil) != tc.wantProblem {
				t.Errorf("versionProblem(%q,%q) problem=%v, want %v", tc.doc, tc.binary, got != nil, tc.wantProblem)
			}
			if got == nil {
				return
			}
			if got.loc != "version" {
				t.Errorf("version problem loc = %q, want \"version\"", got.loc)
			}
			// The message has to say which way the mismatch runs and what to do about
			// it — "they do not match" leaves the reader to guess which side to change.
			for _, want := range []string{tc.doc, tc.binary} {
				if !strings.Contains(got.msg, strings.TrimPrefix(want, "v")) {
					t.Errorf("message %q does not name %q", got.msg, want)
				}
			}
			if !strings.Contains(got.msg, "upgrade") && !strings.Contains(got.msg, "install") {
				t.Errorf("message %q does not say what to do: %q", tc.name, got.msg)
			}
		})
	}
}

// TestParseSemver covers the shapes versionProblem must not choke on.
func TestParseSemver(t *testing.T) {
	ok := map[string]semver{
		"1.2.3":          {1, 2, 3},
		"v1.2.3":         {1, 2, 3},
		"0.0.0":          {0, 0, 0},
		"10.20.30":       {10, 20, 30},
		"1.2.3-rc.1":     {1, 2, 3},
		"1.2.3+build.99": {1, 2, 3},
		" 1.2.3 ":        {1, 2, 3},
	}
	for in, want := range ok {
		got, valid := parseSemver(in)
		if !valid || got != want {
			t.Errorf("parseSemver(%q) = %v,%v; want %v,true", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "dev", "1.2", "1.2.3.4", "1.2.x", "a.b.c", "1..3", "-1.2.3"} {
		if got, valid := parseSemver(in); valid {
			t.Errorf("parseSemver(%q) = %v,true; want not ok", in, got)
		}
	}
}

func TestFailFast(t *testing.T) {
	confFail := func(mode string) *reconciledConf {
		return &reconciledConf{conf: &Conf{Validate: &ValidateConfig{Fail: mode}}}
	}
	cases := []struct {
		name     string
		failMode string
		rc       *reconciledConf
		want     bool
	}{
		{"flag override fast wins", "fast", confFail("collect"), true},
		{"flag override collect", "collect", confFail("fast"), false},
		{"conf fast", "", confFail("fast"), true},
		{"conf collect", "", confFail("collect"), false},
		{"no override, no conf validate", "", &reconciledConf{conf: &Conf{}}, false},
		{"defaulted conf", "", &reconciledConf{conf: &Conf{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failFast(tc.failMode, tc.rc); got != tc.want {
				t.Errorf("failFast(%q, ...) = %v, want %v", tc.failMode, got, tc.want)
			}
		})
	}
}
