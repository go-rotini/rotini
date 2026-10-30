package rotini

import (
	"testing"
)

// TestParse_repeatedFlagChecksWinnerOnly pins that a single-value flag given more than once is
// checked on its winning (last) occurrence only: an overridden earlier value is never
// type-checked, enum-checked, bounded or matched against a pattern.
func TestParse_repeatedFlagChecksWinnerOnly(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "lim", Identifiers: []string{"--lim"}, Type: "int", Maximum: new(9.0)},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"a", "b"}},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", Pattern: "^[a-z]+$", MaxLength: new(3)},
			{Name: "rate", Identifiers: []string{"--rate"}, Type: "float64"},
			{Name: "wait", Identifiers: []string{"--wait"}, Type: "time.Duration"},
			{Name: "on", Identifiers: []string{"--on"}, Type: "bool"},
		},
	}
	type in struct {
		App struct {
			Flags struct {
				Lim  int     `rotini:"lim"`
				Mode string  `rotini:"mode"`
				Name string  `rotini:"name"`
				Rate float64 `rotini:"rate"`
			}
			Arguments struct{}
		}
	}
	for _, argv := range [][]string{
		{"--lim", "99", "--lim", "4"},
		{"--lim", "x", "--lim", "4"},
		{"--mode", "zzz", "--mode", "a"},
		{"--name", "TOOLONG", "--name", "ab"},
		{"--rate", "x", "--rate", "1.5"},
		{"--wait", "x", "--wait", "1s"},
		{"--on=maybe", "--on=true"},
	} {
		var got in
		if err := NewParser().Parse(NewContextFor(def, argv), &got); err != nil {
			t.Errorf("%q: %v", argv, err)
		}
	}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"--lim", "4", "--lim", "99"}, "must be <= 9"},
		{[]string{"--lim", "4", "--lim", "x"}, "--lim"},
		{[]string{"--mode", "a", "--mode", "zzz"}, "zzz"},
	} {
		var got in
		err := NewParser().Parse(NewContextFor(def, c.argv), &got)
		assertConstraint(t, c.argv[3], err, c.want)
	}
}
