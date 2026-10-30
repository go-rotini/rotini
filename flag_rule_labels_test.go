package rotini

import "testing"

// TestFlagRules_nameTypedSpellings pins that flag-group and dependency errors name a set flag
// the way the user typed it, a short cluster's members included, and any other flag by its
// canonical identifier.
func TestFlagRules_nameTypedSpellings(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "ipv4", Identifiers: []string{"--ipv4", "-4"}, Type: "bool"},
			{Name: "ipv6", Identifiers: []string{"--ipv6", "-6"}, Type: "bool"},
			{Name: "user", Identifiers: []string{"--user", "-u"}, Type: "string"},
			{Name: "pass", Identifiers: []string{"--pass", "-p"}, Type: "string"},
			{Name: "fmt", Identifiers: []string{"--format", "-f"}, Type: "string"},
			{Name: "out", Identifiers: []string{"--out", "-o"}, Type: "string"},
			{Name: "quiet", Identifiers: []string{"--quiet", "-q"}, Type: "bool"},
		},
		FlagGroups: []FlagGroup{
			{Kind: FlagGroupMutuallyExclusive, Flags: []string{"ipv4", "ipv6"}},
			{Kind: FlagGroupRequiredTogether, Flags: []string{"user", "pass"}},
		},
		FlagDependencies: []FlagDependency{
			{When: "fmt", Requires: []string{"out"}},
			{When: "out", Forbids: []string{"quiet"}},
		},
	}
	var in struct{}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"-46"}, "flags -4 and -6 are mutually exclusive"},
		{[]string{"--ipv6", "-4"}, "flags -4 and --ipv6 are mutually exclusive"},
		{[]string{"-u", "ada"}, "flags -u, --pass must be used together"},
		{[]string{"-f", "csv"}, "flag --out is required when -f is set"},
		{[]string{"-o", "x", "-q"}, "flag -q can't be used when -o is set"},
	} {
		err := NewParser().Parse(NewContextFor(def, c.argv), &in)
		assertConstraint(t, c.argv[0], err, c.want)
	}
}
