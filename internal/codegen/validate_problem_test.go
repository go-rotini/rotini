package codegen

import (
	"errors"
	"testing"
)

func TestQuotePath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"cmd/app/embed", `"cmd/app/embed"`},
		{`cmd\app\embed`, `"cmd\app\embed"`},
		{`C:\abs\file.json`, `"C:\abs\file.json"`},
		{`C:\a"b`, `"C:\\a\"b"`},
		{"C:\\a\tb", `"C:\\a\tb"`},
		{"C:\\a\xffb", `"C:\\a\xffb"`},
	} {
		if got := quotePath(tc.in); got != tc.want {
			t.Errorf("quotePath(%q) = %s, want %s", tc.in, got, tc.want)
		}
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
