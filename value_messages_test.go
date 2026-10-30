package rotini

import "testing"

// TestBounds_printExactly pins that a bound in an error message is written in plain decimal,
// never with an exponent.
func TestBounds_printExactly(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "n", Identifiers: []string{"--n"}, Type: "int64", Maximum: new(9007199254740992.0)},
			{Name: "r", Identifiers: []string{"--r"}, Type: "float64", Minimum: new(0.0000001)},
		},
	}
	var in struct{}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"--n", "9007199254740993"}, "--n must be <= 9007199254740992 (got 9007199254740993)"},
		{[]string{"--r", "0"}, "--r must be >= 0.0000001 (got 0)"},
	} {
		err := NewParser().Parse(NewContextFor(def, c.argv), &in)
		assertConstraint(t, c.argv[1], err, c.want)
	}
}

// TestFileValue_bareAt pins that a bare "@" on a flag reading `@file` values says it names no
// file, rather than reporting a file named "".
func TestFileValue_bareAt(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "token", Identifiers: []string{"--token"}, Type: "string", From: []string{"file"}}}}
	var in struct{}
	for _, argv := range [][]string{{"--token", "@"}, {"--token=@"}} {
		err := NewParser().Parse(NewContextFor(def, argv).WithDir(t.TempDir()), &in)
		assertConstraint(t, "@", err, `--token: "@" names no file; write @path to read a file, or @@ for a literal @`)
	}
}
