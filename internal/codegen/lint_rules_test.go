package codegen

import (
	"strings"
	"testing"
)

// TestBoundsProblems pins how bounds fold into one range: whole-number types round inward,
// floats compare exactly, and measured types print in their own unit.
func TestBoundsProblems(t *testing.T) {
	cases := []struct {
		name string
		b    BaseSchema
		elem string
		want string // a substring of the one problem, or "" for none
	}{
		{"int range with a value", BaseSchema{Minimum: 1.0, Maximum: 1.0}, "int", ""},
		{"int min above max", BaseSchema{Minimum: 2.0, Maximum: 1.0}, "int", "`minimum` 2 is above `maximum` 1"},
		{"int fractional bounds hold no integer", BaseSchema{Minimum: 1.2, Maximum: 1.8}, "int", "leave no integer between them; widen the range"},
		{"int fractional bounds hold an integer", BaseSchema{Minimum: 0.5, Maximum: 1.5}, "int", ""},
		{"int exclusive neighbours", BaseSchema{ExclusiveMinimum: 1.0, ExclusiveMaximum: 2.0}, "int", "leave no integer between them; widen the range or use"},
		{"int exclusive with a gap", BaseSchema{ExclusiveMinimum: 1.0, ExclusiveMaximum: 3.0}, "int", ""},
		{"float exclusive neighbours", BaseSchema{ExclusiveMinimum: 1.0, ExclusiveMaximum: 2.0}, "float64", ""},
		{"float equal inclusive", BaseSchema{Minimum: 1.5, Maximum: 1.5}, "float64", ""},
		{"float equal, one exclusive", BaseSchema{Minimum: 1.5, ExclusiveMaximum: 1.5}, "float64", "leave no number between them"},
		{"the tighter lower bound wins", BaseSchema{Minimum: 0.0, ExclusiveMinimum: 5.0, Maximum: 5.0}, "int", "`exclusiveMinimum` 5 and `maximum` 5"},
		{"duration in its unit", BaseSchema{Minimum: 60e9, Maximum: 30e9}, "time.Duration", "`minimum` 1m0s is above `maximum` 30s"},
		{"duration text bounds", BaseSchema{Minimum: "1h", Maximum: "30m"}, "time.Duration", "`minimum` 1h0m0s is above `maximum` 30m0s"},
		{"size in its unit", BaseSchema{Minimum: float64(2 << 20), Maximum: float64(1 << 20)}, rotiniPkgName + ".ByteSize", "`minimum` 2Mi is above `maximum` 1Mi"},
		{"unconvertible text is left alone", BaseSchema{Minimum: "soon", Maximum: 1.0}, "time.Duration", ""},
		{"not numeric", BaseSchema{Minimum: 2.0, Maximum: 1.0}, "string", ""},
		{"lengths", BaseSchema{MinLength: 3, MaxLength: new(0)}, "string", "`minLength` 3 is above `maxLength` 0"},
		{"lengths unset max", BaseSchema{MinLength: 3}, "string", ""},
		{"items", BaseSchema{MinItems: 2, MaxItems: new(1)}, "[]string", "`minItems` 2 is above `maxItems` 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := boundsProblems(&tc.b, tc.elem, stringValued(tc.elem), strings.HasPrefix(tc.elem, "[]"))
			switch {
			case tc.want == "" && len(got) > 0:
				t.Errorf("got %q, want no problem", got)
			case tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)):
				t.Errorf("got %q, want one problem containing %q", got, tc.want)
			}
		})
	}
}

// TestReadAsCluster pins the bundle simulation against the parser's rules: bool and count
// shorts continue, the first value-taking short ends the bundle, and an undeclared letter
// means the word is no bundle at all.
func TestReadAsCluster(t *testing.T) {
	shorts := map[string]FlagInput{
		"-a": {Name: "all", Schema: &InputSchema{Type: "bool"}},
		"-v": {Name: "verbose", Schema: &InputSchema{Type: "count"}},
		"-n": {Name: "number", Schema: &InputSchema{Type: "int"}},
	}
	cases := []struct {
		id, want string
		ok       bool
	}{
		{"-av", "-a -v", true},
		{"-avn", "-a -v -n", true},
		{"-name", "-n=ame", true},
		{"-vname", "-v -n=ame", true},
		{"-ax", "", false},
		{"-xa", "", false},
	}
	for _, tc := range cases {
		r, ok := readAsCluster(tc.id, shorts)
		got := strings.Join(r.shorts, " ")
		if r.valued && r.value != "" {
			got += "=" + r.value
		}
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("readAsCluster(%q) = %q, %v; want %q, %v", tc.id, got, ok, tc.want, tc.ok)
		}
	}
}

// TestLintPosixNames pins the opt-in rule's name check, and that it is off by default.
func TestLintPosixNames(t *testing.T) {
	on := &Conf{Validate: &ValidateConfig{PosixNames: true}}
	for name, warn := range map[string]bool{
		"MyTool": true, "my_tool": true, "my-tool": true, "averyverylongname": true, "x": true,
		"ok9": false, "kubectl": false, "ls": false,
	} {
		spec := &Spec{Command: Command{Name: name}}
		if got := len(lintPosixNames(spec, on)) > 0; got != warn {
			t.Errorf("lintPosixNames(%q) warned = %v, want %v", name, got, warn)
		}
		if got := lintPosixNames(spec, &Conf{}); got != nil {
			t.Errorf("lintPosixNames(%q) without validate.posix_names = %v, want nothing", name, got)
		}
	}
}

// TestLintFlagsFirstIsOptIn pins that the flags-first warnings need validate.flags_first.
func TestLintFlagsFirstIsOptIn(t *testing.T) {
	spec := &Spec{Command: Command{Name: "demo", Env: []EnvInput{{Name: "region", Schema: &InputSchema{}}}}}
	if got := lintFlagsFirst(spec, &Conf{}); got != nil {
		t.Errorf("without validate.flags_first: %v, want nothing", got)
	}
	if got := lintFlagsFirst(spec, &Conf{Validate: &ValidateConfig{FlagsFirst: true}}); len(got) != 1 {
		t.Errorf("with validate.flags_first: %v, want one warning", got)
	}
}

// TestZeroMaxBoundsAreKept pins that maxLength: 0 and maxItems: 0 are emitted rather than
// dropped as unset. (The lintBoundsSatisfiable fixture pins that they survive decoding.)
func TestZeroMaxBoundsAreKept(t *testing.T) {
	s := &InputSchema{MaxLength: new(0), MaxItems: new(0)}
	if got, want := constraintTags(s), `maxlen:"0" maxitems:"0"`; got != want {
		t.Errorf("constraintTags = %s, want %s", got, want)
	}
	if got := (&InputSchema{}); constraintTags(got) != "" {
		t.Errorf("an unset maxLength/maxItems emitted %s", constraintTags(got))
	}
}
