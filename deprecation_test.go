package rotini

import (
	"slices"
	"testing"
)

// TestDeprecations_needsNoParserBound pins that Deprecations works with nothing registered on
// the context.
func TestDeprecations_needsNoParserBound(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "compile", Handler: "AppCompile",
			Aliases:               []string{"build"},
			DeprecatedIdentifiers: []string{"build"},
		}},
	}

	rtx := NewContextFor(def, []string{"build"})
	if len(rtx.services) != 0 {
		t.Fatal("something is bound; this test is meaningless unless the registry is empty")
	}

	deps := Deprecations(rtx)
	if len(deps) != 1 {
		t.Fatalf("Deprecations = %v, want the one deprecated alias", deps)
	}
	if deps[0].Kind != "command" || deps[0].Identifier != "build" || deps[0].Name != "compile" {
		t.Errorf("Deprecations = %+v, want command compile via \"build\"", deps[0])
	}
}

// TestDeprecations_nilContext pins that a nil context yields no deprecations.
func TestDeprecations_nilContext(t *testing.T) {
	if got := Deprecations(nil); got != nil {
		t.Errorf("Deprecations(nil) = %v, want nil", got)
	}
}

// Using a deprecated command, flag or argument reports a Deprecation carrying the spec's
// `deprecated:` message; deprecated identifiers alone report without a message.
func TestDeprecations_carryTheMessage(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "build", Aliases: []string{"b", "mk"}, Handler: "AppBuild", Deprecated: "use app make",
			DeprecatedIdentifiers: []string{"mk"},
			Flags: []FlagDef{
				{Name: "conf", Identifiers: []string{"-c", "--conf"}, Type: "string", Deprecated: "use --config"},
				{Name: "out", Identifiers: []string{"-o", "--out", "--output"}, Type: "string", DeprecatedIdentifiers: []string{"--out"}},
			},
			Arguments: []ArgDef{{Name: "target", Type: "string"}, {Name: "legacy", Type: "string", Deprecated: "no longer read"}},
		}},
	}
	got := Deprecations(NewContextFor(def, []string{"mk", "-c", "x", "--out", "y", "t1", "t2"}))
	want := []Deprecation{
		{Kind: "command", Name: "build", Identifier: "mk", Message: "use app make"},
		{Kind: "flag", Name: "conf", Identifier: "-c", Message: "use --config"},
		{Kind: "flag", Name: "out", Identifier: "--out"},
		{Kind: "argument", Name: "legacy", Identifier: "<legacy>", Message: "no longer read"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Deprecations =\n %+v\nwant\n %+v", got, want)
	}
	if got := got[1].Error(); got != `flag "-c" is deprecated: use --config` {
		t.Errorf("Error() = %q", got)
	}
	// When some spellings are listed as deprecated, only those report: the command by a live
	// alias and --output report nothing, nor does an argument given no value.
	if got := Deprecations(NewContextFor(def, []string{"b", "--output", "y", "t1"})); len(got) != 0 {
		t.Errorf("got %+v, want nothing: only the listed spellings are deprecated", got)
	}
}

// A token counts against the flag the parser bound it to: with a deprecated root --store and a
// sub-command's own --store, `app set --store x` reports nothing.
func TestDeprecations_attributeTokensToTheBoundFlag(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "store", Identifiers: []string{"--store"}, Type: "string", Deprecated: "use a context"}},
		Commands: []CommandDef{{
			Name: "set", Handler: "AppSet",
			Flags: []FlagDef{{Name: "store", Identifiers: []string{"--store"}, Type: "string"}},
		}},
	}
	if got := Deprecations(NewContextFor(def, []string{"set", "--store", "x"})); len(got) != 0 {
		t.Errorf("the sub-command's own --store was reported: %+v", got)
	}
	got := Deprecations(NewContextFor(def, []string{"--store", "x", "set"}))
	if len(got) != 1 || got[0].Name != "store" || got[0].Message != "use a context" {
		t.Errorf("the root's --store: %+v", got)
	}
}

// TestDeprecations_lifecycle pins that a Deprecation carries the spec's planned releases: the
// item's, or a deprecated identifier's own removal where it has one.
func TestDeprecations_lifecycle(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "build", Aliases: []string{"mk"}, Handler: "AppBuild",
			DeprecatedIdentifiers: []string{"mk"}, DeprecatedIdentifiersRemovedIn: map[string]string{"mk": "2.0.0"},
			Flags: []FlagDef{
				{Name: "conf", Identifiers: []string{"--conf"}, Type: "string", Deprecated: "use --config", DeprecatedSince: "1.4.0", RemovedIn: "3.0.0"},
				{Name: "out", Identifiers: []string{"--out", "--output"}, Type: "string", DeprecatedIdentifiers: []string{"--out"}, DeprecatedIdentifiersRemovedIn: map[string]string{"--out": "2.1.0"}},
			},
			Arguments: []ArgDef{{Name: "legacy", Type: "string", Deprecated: "unused", DeprecatedSince: "1.1.0", RemovedIn: "1.9.0"}},
		}},
	}
	got := Deprecations(NewContextFor(def, []string{"mk", "--conf", "x", "--out", "y", "z"}))
	want := []Deprecation{
		{Kind: "command", Name: "build", Identifier: "mk", RemovedIn: "2.0.0"},
		{Kind: "flag", Name: "conf", Identifier: "--conf", Message: "use --config", Since: "1.4.0", RemovedIn: "3.0.0"},
		{Kind: "flag", Name: "out", Identifier: "--out", RemovedIn: "2.1.0"},
		{Kind: "argument", Name: "legacy", Identifier: "<legacy>", Message: "unused", Since: "1.1.0", RemovedIn: "1.9.0"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Deprecations =\n%+v\nwant\n%+v", got, want)
	}
}
