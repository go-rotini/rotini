package codegen

import (
	"errors"
	"testing"
)

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
