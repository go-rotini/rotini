package codegen

import (
	"testing"
)

// TestConstraintRendering pins the EXACT struct-tag + Go-literal output of
// constraintTags/constraintsLiteral before they are deduped behind eachConstraint —
// the emitted bytes (not just compilation) must stay identical.
func TestConstraintRendering(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	s := &InputSchema{
		Minimum: f(1), Maximum: f(10),
		ExclusiveMinimum: f(0), ExclusiveMaximum: f(100),
		MultipleOf: f(2),
		MinLength:  3, MaxLength: 20, MinItems: 1, MaxItems: 5,
		Pattern: "^x$"}
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
