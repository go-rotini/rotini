package codegen

import (
	"slices"
	"strings"
	"testing"
)

// A flag-set flag with values_from takes its values from each using command's own output,
// since sets are expanded into their commands before values are derived.
func TestResolveValuesFrom_flagSet(t *testing.T) {
	t.Parallel()
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: taskr
  summary: track tasks
  flag_sets:
    Fields:
      flags:
        - name: sort-by
          summary: sort by this field
          identifiers: [--sort-by]
          role: sort
          schema: { type: string, values_from: output }
  commands:
    - name: list
      summary: list the tasks
      use: [Fields]
      output: { type: array, items: { type: object, properties: { id: { type: string }, title: { type: string } } } }
    - name: users
      summary: list the users
      use: [Fields]
      output: { type: array, items: { type: object, properties: { login: { type: string } } } }
`)
	list, users := spec.Command.Commands[0], spec.Command.Commands[1]
	if got := enumStrings(list.Flags[0].Schema.Enum); !slices.Equal(got, []string{"id", "title"}) {
		t.Errorf("list --sort-by enum = %v", got)
	}
	if got := enumStrings(users.Flags[0].Schema.Enum); !slices.Equal(got, []string{"login"}) {
		t.Errorf("users --sort-by enum = %v", got)
	}
	if set := spec.Command.FlagSets["Fields"].Flags[0].Schema; len(set.Enum) != 0 {
		t.Errorf("the set's own declaration was changed: enum = %v", set.Enum)
	}
	for _, problems := range [][]error{lintValuesFrom(spec), lintFieldRoles(spec)} {
		for _, p := range problems {
			t.Errorf("lint: %v", p)
		}
	}
}

// The generated Usage function accepts a hidden alias wherever a listed one is accepted, as
// the command line and Help do.
func TestUsageFuncDecl_hiddenAliases(t *testing.T) {
	t.Parallel()
	src := usageFuncDecl(runtimeLaneProgram(t, `version: 0.0.0
command:
  name: acme
  summary: s
  commands:
    - name: remove
      summary: remove a thing
      aliases: [rm]
      hidden_aliases: [del]
`))
	if want := "case \"remove\", \"rm\", \"del\":\n\t\treturn \"acme remove\", nil"; !strings.Contains(src, want) {
		t.Errorf("Usage function lacks %q\n%s", want, src)
	}
}

// An argument's help row lists its variable_file beside its environment variable, as a flag's
// row does.
func TestArgumentRow_variableFile(t *testing.T) {
	t.Parallel()
	arg := ArgumentInput{Name: "token", Schema: &InputSchema{Type: "string", Variable: "APP_TOKEN", VariableFile: "APP_TOKEN_FILE"}}
	row := argumentRow(arg, "", false)
	if want := []string{"APP_TOKEN", "APP_TOKEN_FILE (a file)"}; !slices.Equal(row.Env, want) {
		t.Errorf("Env = %q, want %q", row.Env, want)
	}
	env, _ := argumentFallback(arg, "", false)
	if !slices.Equal(env, []string{"APP_TOKEN"}) {
		t.Errorf("argumentFallback env = %q; the contract lists the variable alone", env)
	}
}

// The directory flag stays out of every kind of flag dependency, value rules included.
func TestFlagGroupsNaming_dependencyRules(t *testing.T) {
	t.Parallel()
	c := &Command{FlagDependencies: []FlagDependency{
		{When: "a", Requires: []string{"b"}},
		{Unless: []string{"dir"}, Requires: []string{"b"}},
		{When: "a", Forbids: []string{"other"}},
	}}
	if got := flagGroupsNaming(c, "dir"); got != "flag dependency 1" {
		t.Errorf("unless: got %q", got)
	}
	if got := flagGroupsNaming(c, "other"); got != "flag dependency 2" {
		t.Errorf("forbids: got %q", got)
	}
}
