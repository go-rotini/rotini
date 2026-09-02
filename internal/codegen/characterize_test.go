package codegen

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Characterization tests pinning behavior the structural refactor moves between files
// (the lint registries, and the validate/problem machinery extracted into problem.go).
// These are intentionally about CONTRACTS, not file locations.

// TestLintRegistryCompleteness guards the lint_spec.go / lint_conf.go split: if a rule
// is accidentally dropped while relocating funcs, the count regresses.
func TestLintRegistryCompleteness(t *testing.T) {
	if got := len(specLints); got != 28 {
		t.Errorf("len(specLints) = %d, want 28 (a rule was dropped or added — update intentionally)", got)
	}
	if got := len(confLints); got != 6 {
		t.Errorf("len(confLints) = %d, want 6", got)
	}
}

func TestVersionProblem(t *testing.T) {
	cases := []struct {
		name        string
		doc, binary string
		wantProblem bool
	}{
		{"match", "1.2.3", "1.2.3", false},
		{"match with v prefix", "1.2.3", "v1.2.3", false},
		{"mismatch", "1.0.0", "2.0.0", true},
		{"doc empty skips", "", "2.0.0", false},
		{"binary empty skips", "1.0.0", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionProblem("spec", tc.doc, tc.binary)
			if (got != nil) != tc.wantProblem {
				t.Errorf("versionProblem(%q,%q) problem=%v, want %v", tc.doc, tc.binary, got != nil, tc.wantProblem)
			}
			if got != nil && got.loc != "version" {
				t.Errorf("version problem loc = %q, want \"version\"", got.loc)
			}
		})
	}
}

func TestSplitProblems(t *testing.T) {
	warn := &problem{kind: "spec", loc: "x", msg: "advisory", sev: severityWarning}
	hard := &problem{kind: "spec", loc: "y", msg: "fatal"}
	plain := errors.New("not a *problem")

	errs, warns := splitProblems([]error{warn, hard, plain})
	if len(warns) != 1 || warns[0] != error(warn) {
		t.Errorf("warnings = %v, want exactly the severityWarning problem", warns)
	}
	if len(errs) != 2 {
		t.Errorf("errors = %d, want 2 (the hard problem + the non-*problem error)", len(errs))
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

// TestConstraintRendering pins the EXACT struct-tag + Go-literal output of
// constraintTags/constraintsLiteral before they are deduped behind eachConstraint —
// the emitted bytes (not just compilation) must stay identical.
func TestConstraintRendering(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := &InputSchema{BaseSchema: BaseSchema{
		Minimum: f(1), Maximum: f(10),
		ExclusiveMinimum: f(0), ExclusiveMaximum: f(100),
		MultipleOf: f(2),
		MinLength:  3, MaxLength: 20, MinItems: 1, MaxItems: 5,
		Pattern: "^x$",
	}}
	const wantTags = `min:"1" max:"10" xmin:"0" xmax:"100" multipleof:"2" minlen:"3" maxlen:"20" minitems:"1" maxitems:"5" pattern:"^x$"`
	const wantLit = `rotini.Constraints{Minimum: rotini.Ptr[float64](1), Maximum: rotini.Ptr[float64](10), ExclusiveMinimum: rotini.Ptr[float64](0), ExclusiveMaximum: rotini.Ptr[float64](100), MultipleOf: rotini.Ptr[float64](2), MinLength: 3, MaxLength: 20, MinItems: 1, MaxItems: 5, Pattern: "^x$"}`
	if got := constraintTags(s); got != wantTags {
		t.Errorf("constraintTags:\n got=%s\nwant=%s", got, wantTags)
	}
	if got := constraintsLiteral(s); got != wantLit {
		t.Errorf("constraintsLiteral:\n got=%s\nwant=%s", got, wantLit)
	}
	if got := constraintTags(&InputSchema{}); got != "" {
		t.Errorf("constraintTags(empty) = %q, want empty", got)
	}
	if got := constraintsLiteral(&InputSchema{}); got != "" {
		t.Errorf("constraintsLiteral(empty) = %q, want empty", got)
	}
}

// TestConstraintNumericFamilyMatchesRuntime guards the hand-copied constraintNumericFamily
// (codegen cannot import the runtime's unexported numericFamily) against silent drift by
// extracting the runtime's set from its source. The runtime is the module's root package,
// two levels up from internal/codegen.
func TestConstraintNumericFamilyMatchesRuntime(t *testing.T) {
	parser, err := os.ReadFile(filepath.Join("..", "..", "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(parser)
	start := strings.Index(body, "var numericFamily = map[string]bool{")
	if start < 0 {
		t.Fatal("numericFamily literal not found in runtime parser.go")
	}
	block := body[start : start+strings.Index(body[start:], "}")]
	runtimeSet := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(\w+)":\s*true`).FindAllStringSubmatch(block, -1) {
		runtimeSet[m[1]] = true
	}
	if len(runtimeSet) == 0 {
		t.Fatal("extracted no keys from runtime numericFamily")
	}
	if !maps.Equal(runtimeSet, constraintNumericFamily) {
		t.Errorf("constraintNumericFamily drifted from runtime numericFamily:\n runtime=%v\n codegen=%v", runtimeSet, constraintNumericFamily)
	}
}
