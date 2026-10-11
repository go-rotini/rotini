package rotini

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// resultOf runs one completion request on p and returns its CompletionResult.
func resultOf(t *testing.T, p *Program, words ...string) CompletionResult {
	t.Helper()
	var got CompletionResult
	if _, err := p.Complete(words, func(_ io.Writer, r CompletionResult) error { got = r; return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

// values returns the candidates' values.
func values(r CompletionResult) []string {
	var out []string
	for _, c := range r.Candidates {
		out = append(out, c.Value)
	}
	return out
}

// specFlagsDef exercises what the spec says about flags: repeatable kinds, clusters, negated
// forms, object fields, cascading and redeclared flags, groups and deprecation.
func specFlagsDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"},
			{Name: "x", Identifiers: []string{"-x"}, Type: "bool"},
			{Name: "out", Identifiers: []string{"-o"}, Type: "string"},
			{Name: "file", Identifiers: []string{"-f"}, Type: "bool"},
			{Name: "color", Identifiers: []string{"--color"}, Type: "bool", Negatable: true},
			{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string"},
			{Name: "label", Identifiers: []string{"--label"}, Type: "map[string]string"},
			{Name: "debug", Identifiers: []string{"--debug"}, Type: "count"},
			{Name: "db", Identifiers: []string{"--db"}, Type: "DB", ObjectSchema: `{"type":"object","properties":{"host":{"type":"string"}}}`},
			{Name: "region", Identifiers: []string{"--region"}, Type: "string"},
		},
		Commands: []CommandDef{{
			Name: "sub", Handler: "AppSub",
			Flags:     []FlagDef{{Name: "region", Identifiers: []string{"--region"}, Type: "string"}},
			Arguments: []ArgDef{{Name: "rest", Type: "[]string", Variadic: true}},
		}},
	}
}

// TestComplete_hidesFlagsAlreadySet pins that a single-value flag already on the line is not
// offered again, read the way the parser reads it, while list, map, count and object flags
// still are.
func TestComplete_hidesFlagsAlreadySet(t *testing.T) {
	def := specFlagsDef()
	cases := []struct {
		name    string
		words   []string
		hidden  []string
		offered []string
	}{
		{"short and long of one flag", []string{"-v", "-"}, []string{"-v", "--verbose"}, []string{"-x"}},
		{"a bundle sets each letter", []string{"-vx", "-"}, []string{"-v", "-x"}, []string{"-o"}},
		{"-ofile sets only -o", []string{"-ofile", "-"}, []string{"-o"}, []string{"-f", "-v", "-x"}},
		{"a negated form sets its flag", []string{"--no-color", "-"}, []string{"--color", "--no-color"}, []string{"-v"}},
		{"list, map and count flags repeat", []string{"--tag", "a", "--label", "k=v", "--debug", "-"}, nil, []string{"--tag", "--label", "--debug"}},
		{"an object field still repeats", []string{"--db.host=h", "-"}, nil, []string{"--db"}},
		{"an empty inline value counts as set", []string{"--region=", "-"}, []string{"--region"}, nil},
		{"bash's = split", []string{"--region", "=", "eu", "-"}, []string{"--region"}, []string{"-v"}},
		{"set before the sub-command, on its frame", []string{"--region", "eu", "sub", "-"}, nil, []string{"--region"}},
		{"after -- nothing is set", []string{"sub", "--", "-v", ""}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := complete(def, c.words, nil, nil)
			for _, h := range c.hidden {
				if slices.Contains(got, h) {
					t.Errorf("complete(%q) offers %s, which is already set: %v", c.words, h, got)
				}
			}
			for _, o := range c.offered {
				if !slices.Contains(got, o) {
					t.Errorf("complete(%q) lost %s: %v", c.words, o, got)
				}
			}
		})
	}

	// A sub-command that redeclares --region is a different flag: setting the root's leaves the
	// sub-command's offered, and setting the sub-command's hides it.
	if got := complete(def, []string{"sub", "--region", "eu", "--r"}, nil, nil); len(got) != 0 {
		t.Errorf("redeclared and set on its own frame: %v, want nothing", got)
	}
	if cc := walkContext(def, []string{"sub", "--", "-v"}); len(cc.setAt(0)) != 0 || cc.positionals != 1 {
		t.Errorf("after --: set %v, positionals %d; want nothing set, one positional", cc.setAt(0), cc.positionals)
	}
}

// TestComplete_negatedFormsOffered pins that a negatable flag offers its --no- form.
func TestComplete_negatedFormsOffered(t *testing.T) {
	got := complete(specFlagsDef(), []string{"--c"}, nil, nil)
	if !slices.Equal(got, []string{"--color"}) {
		t.Errorf("--c = %v, want [--color]", got)
	}
	if got := complete(specFlagsDef(), []string{"--no"}, nil, nil); !slices.Equal(got, []string{"--no-color"}) {
		t.Errorf("--no = %v, want [--no-color]", got)
	}
}

// groupsDef has an exclusive pair, a one_of group, a required flag, a required_together pair
// and a dependency.
func groupsDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "json", Identifiers: []string{"--json"}, Type: "bool"},
			{Name: "yaml", Identifiers: []string{"--yaml"}, Type: "bool"},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
			{Name: "user", Identifiers: []string{"--user"}, Type: "string"},
			{Name: "pass", Identifiers: []string{"--pass"}, Type: "string"},
			{Name: "plan", Identifiers: []string{"--plan"}, Type: "string"},
			{Name: "dry", Identifiers: []string{"--dry"}, Type: "bool"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true, Required: true},
		},
		FlagGroups: []FlagGroup{
			{Kind: FlagGroupMutuallyExclusive, Flags: []string{"json", "yaml"}},
			{Kind: FlagGroupRequiredTogether, Flags: []string{"user", "pass"}},
		},
		FlagDependencies: []FlagDependency{{When: "plan", Requires: []string{"dry"}}},
	}
}

// TestComplete_flagGroups pins that a set member of an exclusive group hides the others, and
// that unset required flags, group-implied ones included, come first.
func TestComplete_flagGroups(t *testing.T) {
	p := NewProgram(groupsDef(), nil)
	r := resultOf(t, p, "--json", "--")
	if slices.Contains(values(r), "--yaml") {
		t.Errorf("--json set: %v still offers --yaml", values(r))
	}
	if want := []string{"--token", "--dry", "--help", "--pass", "--plan", "--user"}; !slices.Equal(values(r), want) || !r.KeepOrder {
		t.Errorf("required first: %v (keep-order %v), want %v with keep-order", values(r), r.KeepOrder, want)
	}

	r = resultOf(t, p, "--token", "t", "--user", "u", "--plan", "p", "--")
	if want := []string{"--dry", "--pass", "--help", "--json", "--yaml"}; !slices.Equal(values(r), want) {
		t.Errorf("group-implied: %v, want %v", values(r), want)
	}

	r = resultOf(t, p, "--token", "t", "--")
	if want := []string{"--dry", "--help", "--json", "--pass", "--plan", "--user", "--yaml"}; !slices.Equal(values(r), want) || r.KeepOrder {
		t.Errorf("nothing required: %v (keep-order %v), want %v sorted", values(r), r.KeepOrder, want)
	}

	oneOf := groupsDef()
	oneOf.FlagGroups = []FlagGroup{{Kind: FlagGroupOneOf, Flags: []string{"json", "yaml"}}}
	if got := complete(oneOf, []string{"--token", "t", "--yaml", "--"}, nil, nil); slices.Contains(got, "--json") {
		t.Errorf("one_of with --yaml set still offers --json: %v", got)
	}
	if got := complete(oneOf, []string{"--token", "t", "--"}, nil, nil); !slices.Equal(got[:2], []string{"--json", "--yaml"}) {
		t.Errorf("one_of unset: %v, want its members first", got)
	}
}

// deprecatedDef has a deprecated flag, a deprecated identifier, a deprecated command and a
// deprecated alias.
func deprecatedDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "old", Identifiers: []string{"--old"}, Type: "bool", Negatable: true, Deprecated: "use --new"},
			{Name: "new", Identifiers: []string{"--new", "--renamed"}, Type: "bool", Negatable: true, DeprecatedIdentifiers: []string{"--renamed"}},
		},
		Commands: []CommandDef{
			{Name: "ship", Handler: "AppShip", Aliases: []string{"s", "push"}, DeprecatedIdentifiers: []string{"push"}},
			{Name: "legacy", Handler: "AppLegacy", Deprecated: "use ship", Commands: []CommandDef{{Name: "run", Handler: "AppLegacyRun"}}},
		},
	}
}

// TestComplete_leavesDeprecatedOut pins that deprecated flags, identifiers, commands and
// aliases are not offered, while a typed deprecated command still completes below it.
func TestComplete_leavesDeprecatedOut(t *testing.T) {
	def := deprecatedDef()
	if got := complete(def, []string{"--"}, nil, nil); !slices.Equal(got, []string{"--new", "--no-new"}) {
		t.Errorf("flags = %v, want [--new --no-new]", got)
	}
	if got := complete(def, []string{""}, nil, nil); !slices.Equal(got, []string{"s", "ship"}) {
		t.Errorf("commands = %v, want [s ship]", got)
	}
	if got := complete(def, []string{"legacy", ""}, nil, nil); !slices.Equal(got, []string{"run"}) {
		t.Errorf("below a typed deprecated command = %v, want [run]", got)
	}
}

// helpDef has a help command whose argument completes command paths, a composed-looking
// child with its own help, hidden, deprecated and plugin children.
func helpDef() Definition {
	helpArg := []ArgDef{{Name: "command", Type: "[]string", Variadic: true, Complete: Completion{Kind: "command"}}}
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{Name: "help", Handler: "AppHelp", Summary: "print help", Arguments: helpArg},
			{Name: "remote", Handler: "AppRemote", Summary: "manage remotes", Aliases: []string{"r"}, Commands: []CommandDef{
				{Name: "add", Handler: "AppRemoteAdd", Summary: "add one"},
				{Name: "rm", Handler: "AppRemoteRm"},
				{Name: "secret", Handler: "AppRemoteSecret", Hidden: true},
			}},
			{Name: "old", Handler: "AppOld", Deprecated: "gone"},
			{Name: "child", Handler: "AppChild", Commands: []CommandDef{
				{Name: "help", Handler: "AppChildHelp", Arguments: helpArg},
				{Name: "inner", Handler: "AppChildInner"},
			}},
		},
		Plugins: []PluginDef{{Name: "ext", Binary: "app-ext"}},
	}
}

// TestComplete_commandPaths pins `help <TAB>`: the words already given are a root-relative
// path, and the candidates are its visible sub-commands, aliases included, plugins not.
func TestComplete_commandPaths(t *testing.T) {
	def := helpDef()
	cases := []struct {
		words []string
		want  []string
	}{
		{[]string{"help", ""}, []string{"child", "help", "r", "remote"}},
		{[]string{"help", "remote", ""}, []string{"add", "rm"}},
		{[]string{"help", "r", "a"}, []string{"add"}},
		{[]string{"help", "nope", ""}, nil},
		{[]string{"child", "help", ""}, []string{"child", "help", "r", "remote"}},
	}
	for _, c := range cases {
		var got []string
		for _, cand := range complete(def, c.words, nil, nil) {
			name, _, _ := strings.Cut(cand, "\t")
			got = append(got, name)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("complete(%q) = %v, want %v", c.words, got, c.want)
		}
	}
	p := NewProgram(def, nil)
	if got := completeIn(t, nil, def, nil, "help", "remote", ""); got != "add\tadd one\nrm\n:rotini:none\n" {
		t.Errorf("__complete help remote '' = %q, want the children then :rotini:none", got)
	}
	if got := completeIn(t, PluginCompletion, def, nil, "help", ""); !strings.HasSuffix(got, ":4\n") {
		t.Errorf("PluginCompletion help '' = %q, want :4", got)
	}
	if r := resultOf(t, p, "help", ""); r.Hint.Kind != "command" {
		t.Errorf("CompletionResult.Hint.Kind = %q, want command", r.Hint.Kind)
	}
}

// mapDef has a map flag with declared keys and a sub-command.
func mapDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "set", Identifiers: []string{"--set"}, Type: "map[string]string", KeyPaths: []string{"image", "replicas"}},
			{Name: "env", Identifiers: []string{"--env"}, Type: "string", Enum: []string{"prod", "dev"}},
		},
		Commands: []CommandDef{{Name: "deploy", Handler: "AppDeploy"}, {Name: "status", Handler: "AppStatus"}},
	}
}

// TestComplete_mapKeysNoSpace pins that map keys ask for no space, and that the value after a
// key, as bash splits it, offers nothing rather than sub-commands.
func TestComplete_mapKeysNoSpace(t *testing.T) {
	p := NewProgram(mapDef(), nil)
	if r := resultOf(t, p, "--set", ""); !r.NoSpace || !slices.Equal(values(r), []string{"image=", "replicas="}) {
		t.Errorf("--set '': %v nospace=%v, want the keys with nospace", values(r), r.NoSpace)
	}
	if r := resultOf(t, p, "--set=i"); !r.NoSpace {
		t.Errorf("--set=i: nospace=%v, want true", r.NoSpace)
	}
	if r := resultOf(t, p, "--env", ""); r.NoSpace {
		t.Error("an enum asks for nospace")
	}
	for _, words := range [][]string{
		{"--set", "image", "=", ""},
		{"--set", "=", "image", "=", ""},
		{"--set", "image", "=", "w"},
	} {
		r := resultOf(t, p, words...)
		if len(r.Candidates) != 0 || r.Hint.Kind != "none" {
			t.Errorf("%q: %v hint %q, want nothing and none", words, values(r), r.Hint.Kind)
		}
	}
	// The entry's value is consumed: the next word completes again.
	if got := complete(mapDef(), []string{"--set", "image", "=", "web", ""}, nil, nil); !slices.Equal(got, []string{"deploy", "status"}) {
		t.Errorf("after a split entry: %v, want the sub-commands", got)
	}
	if got := completeIn(t, nil, mapDef(), nil, "--set", ""); got != "image=\nreplicas=\n:rotini:option nospace\n" {
		t.Errorf("__complete --set '' = %q", got)
	}
	if got := completeIn(t, PluginCompletion, mapDef(), nil, "--set", ""); !strings.HasSuffix(got, "\n:2\n") {
		t.Errorf("PluginCompletion --set '' = %q, want :2", got)
	}
}

// orderHandlers' completer returns its candidates out of alphabetical order, and may ask for
// options.
type orderHandlers struct{ opts CompletionOptions }

type orderDeploy struct {
	dynStub
	opts CompletionOptions
}

func (o orderDeploy) CompleteArgValue(rtx *Context, _, _ string) []string {
	rtx.SetCompletionOptions(o.opts)
	return []string{"zeta", "alpha", "mid"}
}

func (h orderHandlers) AppDeploy() Handler { return orderDeploy{opts: h.opts} }

// TestComplete_keepsOrder pins that a completer's candidates and an enum's declared order are
// kept, sub-command names sorted first, and that options a completer sets reach the result.
func TestComplete_keepsOrder(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "level", Identifiers: []string{"--level"}, Type: "string", Enum: []string{"low", "high"},
			EnumValues: []EnumValue{{Value: "low", Summary: "quiet"}, {Value: "high"}}}},
		Commands: []CommandDef{{Name: "deploy", Handler: "AppDeploy",
			Arguments: []ArgDef{{Name: "service", Type: "string"}},
			Commands:  []CommandDef{{Name: "sub", Handler: "AppDeploySub"}}}},
	}
	p := NewProgram(def, orderHandlers{})
	r := resultOf(t, p, "deploy", "")
	if want := []string{"sub", "zeta", "alpha", "mid"}; !slices.Equal(values(r), want) || !r.KeepOrder || r.NoSpace {
		t.Errorf("deploy '': %v keep-order=%v nospace=%v, want %v with keep-order", values(r), r.KeepOrder, r.NoSpace, want)
	}
	r = resultOf(t, p, "--level", "")
	if !slices.Equal(values(r), []string{"low", "high"}) || !r.KeepOrder || r.Candidates[0].Description != "quiet" {
		t.Errorf("enum: %+v keep-order=%v, want declared order, the summary, keep-order", r.Candidates, r.KeepOrder)
	}
	if got := completeIn(t, nil, def, orderHandlers{}, "deploy", "z"); got != "zeta\n" {
		t.Errorf("one candidate = %q, want no option line", got)
	}
	r = resultOf(t, NewProgram(def, orderHandlers{opts: CompletionOptions{NoSpace: true}}), "deploy", "")
	if !r.NoSpace {
		t.Error("SetCompletionOptions(NoSpace) did not reach the result")
	}
	if got := completeIn(t, PluginCompletion, def, orderHandlers{opts: CompletionOptions{NoSpace: true}}, "deploy", ""); !strings.HasSuffix(got, "\n:34\n") {
		t.Errorf("PluginCompletion = %q, want :34 (nospace and keep-order)", got)
	}
	if got := completeIn(t, nil, def, orderHandlers{}, "deploy", ""); !strings.HasSuffix(got, "mid\n:rotini:option keep-order\n") {
		t.Errorf("__complete deploy '' = %q, want the option line last", got)
	}

	// Outside a request, SetCompletionOptions does nothing.
	newContext().SetCompletionOptions(CompletionOptions{NoSpace: true})
}

// TestCompletionDescriptions_switch pins the off switch: the declared variable and the
// program's own rule clear every description, in every format.
func TestCompletionDescriptions_switch(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands:               []CommandDef{{Name: "deploy", Handler: "AppDeploy", Summary: "deploy it"}, {Name: "drop", Handler: "AppDrop", Summary: "drop it"}},
		CompletionDescriptions: &CompletionDescriptionsDef{Env: "APP_DESCRIPTIONS"},
	}
	described := func(r CompletionResult) bool {
		return slices.ContainsFunc(r.Candidates, func(c CompletionCandidate) bool { return c.Description != "" })
	}
	for _, v := range []string{"0", "false", "OFF"} {
		p := NewProgram(def, nil).WithEnviron([]string{"APP_DESCRIPTIONS=" + v})
		if described(resultOf(t, p, "d")) {
			t.Errorf("APP_DESCRIPTIONS=%s: descriptions still shown", v)
		}
	}
	for _, env := range [][]string{nil, {"APP_DESCRIPTIONS=yes"}} {
		if !described(resultOf(t, NewProgram(def, nil).WithEnviron(env), "d")) {
			t.Errorf("env %v: descriptions hidden", env)
		}
	}
	p := NewProgram(def, nil).WithCompletionDescriptions(func(*Context) bool { return false })
	if described(resultOf(t, p, "d")) {
		t.Error("WithCompletionDescriptions(false): descriptions still shown")
	}
	out := &bytes.Buffer{}
	p.stdout = out
	if _, err := p.Complete([]string{"d"}, PluginCompletion); err != nil || strings.Contains(out.String(), "\t") {
		t.Errorf("PluginCompletion with descriptions off = %q, %v", out.String(), err)
	}
	if !described(resultOf(t, p.WithCompletionDescriptions(nil), "d")) {
		t.Error("WithCompletionDescriptions(nil) did not restore the variable check")
	}
	noSwitch := def
	noSwitch.CompletionDescriptions = nil
	if !described(resultOf(t, NewProgram(noSwitch, nil).WithEnviron([]string{"APP_DESCRIPTIONS=off"}), "d")) {
		t.Error("without descriptions_env the environment changed something")
	}
}

// TestCompletionHint_nativeKinds pins the shell-completed kinds on the wire and for the plugin
// hosts.
func TestCompletionHint_nativeKinds(t *testing.T) {
	for _, kind := range []string{"executable", "user", "group", "host"} {
		def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
			{Name: "who", Identifiers: []string{"--who"}, Type: "string", Complete: Completion{Kind: kind}},
		}}
		if got := completionHint(def, []string{"--who", ""}); got != ":rotini:"+kind {
			t.Errorf("%s: hint = %q", kind, got)
		}
		if got := completeIn(t, PluginCompletion, def, nil, "--who", ""); got != ":4\n" {
			t.Errorf("%s: PluginCompletion = %q, want :4", kind, got)
		}
	}
}

// TestCompletionHint_afterTerminator pins that the argument's hint still applies after "--".
func TestCompletionHint_afterTerminator(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{
		{Name: "files", Type: "[]string", Variadic: true, Complete: Completion{Kind: "file", Extensions: []string{"go"}}},
	}}
	if got := completionHint(def, []string{"--", ""}); got != ":rotini:file go" {
		t.Errorf("after --: hint = %q, want :rotini:file go", got)
	}
}

// passHandlers' exec completer answers for the passthrough argument's raw words.
type passHandlers struct{}

type passExec struct{ dynStub }

func (passExec) CompleteArgValue(_ *Context, arg, partial string) []string {
	return []string{partial + "la", arg}
}

func (passHandlers) AppExec() Handler { return passExec{} }

// TestComplete_passthroughArgument pins that the raw words of a passthrough argument get its
// completer and hint, never flags or sub-commands.
func TestComplete_passthroughArgument(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{
		Name: "exec", Handler: "AppExec",
		Flags:     []FlagDef{{Name: "quiet", Identifiers: []string{"-q"}, Type: "bool"}},
		Arguments: []ArgDef{{Name: "cmd", Type: "[]string", Variadic: true, Passthrough: true, Complete: Completion{Kind: "executable"}}},
		Commands:  []CommandDef{{Name: "sub", Handler: "AppExecSub"}},
	}}}
	lookup := reflectLookup(passHandlers{})
	if got := complete(def, []string{"exec", "ls", "-"}, lookup, newContext()); !slices.Equal(got, []string{"-la"}) {
		t.Errorf("exec ls -: %v, want the completer's answer only", got)
	}
	if got := completionHint(def, []string{"exec", "ls", "-"}); got != ":rotini:executable" {
		t.Errorf("exec ls - hint = %q", got)
	}
	if got := complete(def, []string{"exec", "-"}, nil, nil); !slices.Equal(got, []string{"-q"}) {
		t.Errorf("exec -: %v, want the flags before the boundary", got)
	}
}

// ctxHandlers' build completer reports what its rtx carries.
type ctxHandlers struct{ seen *[]string }

type ctxBuild struct {
	dynStub
	seen *[]string
}

type completionCtxKey struct{}

func (c ctxBuild) CompleteFlagValue(rtx *Context, _, _ string) []string {
	v, _ := rtx.Context().Value(completionCtxKey{}).(string)
	*c.seen = append(*c.seen, v, rtx.Command().Name, errString(rtx.Context().Err()))
	return []string{}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (h ctxHandlers) App() Handler { return ctxBuild{seen: h.seen} }

// TestComplete_contextAndAnchoring pins that a completer reads the program's base context and
// runs as the command declaring its flag, an ancestor's included.
func TestComplete_contextAndAnchoring(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:    []FlagDef{{Name: "store", Identifiers: []string{"--store"}, Type: "string"}},
		Commands: []CommandDef{{Name: "show", Handler: "AppShow"}},
	}
	var seen []string
	h := ctxHandlers{seen: &seen}
	ctx := context.WithValue(context.Background(), completionCtxKey{}, "base")

	resultOf(t, NewProgram(def, h).WithContext(ctx), "show", "--store", "")
	if !slices.Equal(seen, []string{"base", "app", ""}) {
		t.Errorf("WithContext: completer saw %q, want base, anchored on app, live", seen)
	}

	seen = nil
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	p := NewProgram(def, h)
	p.stdout = io.Discard
	if code, err := p.RunContext(canceled, []string{"__complete", "show", "--store", ""}); code != 0 || err != nil {
		t.Fatalf("RunContext __complete = %d, %v", code, err)
	}
	if len(seen) != 3 || seen[0] != "base" || !strings.Contains(seen[2], "canceled") {
		t.Errorf("RunContext: completer saw %q, want the caller's canceled context", seen)
	}

	seen = nil
	resultOf(t, NewProgram(def, h), "--store", "")
	if seen[0] != "" || seen[2] != "" {
		t.Errorf("no context: completer saw %q, want a background context", seen)
	}
	if newContext().Context() == nil {
		t.Error("Context() is nil outside a request")
	}
}

// partialInputs is the leaf's inputs shape for partialDef's `deploy`.
type partialInputs struct {
	App struct {
		Flags struct {
			Store   string `rotini:"store" recon:"store" env:"PARTIAL_STORE"`
			Token   string `rotini:"token"`
			Payload string `rotini:"payload"`
		}
		Arguments struct{}
	}
	AppDeploy struct {
		Flags struct {
			Replicas int    `rotini:"replicas"`
			Region   string `rotini:"region"`
		}
		Arguments struct {
			Service string `rotini:"service"`
		}
	}
}

func partialDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "store", Identifiers: []string{"--store"}, Type: "string", Default: "memory"},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", Required: true},
			{Name: "payload", Identifiers: []string{"--payload"}, Type: "string", From: []string{"stdin", "file"}},
		},
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy",
			Flags: []FlagDef{
				{Name: "replicas", Identifiers: []string{"--replicas"}, Type: "int"},
				{Name: "region", Identifiers: []string{"--region"}, Type: "string"},
			},
			Arguments: []ArgDef{{Name: "service", Type: "string"}},
		}},
	}
}

type partialHandlers struct{ got *partialResult }

type partialResult struct {
	in  partialInputs
	set Presence
	err error
}

type partialDeploy struct {
	dynStub
	got *partialResult
}

func (d partialDeploy) CompleteArgValue(rtx *Context, _, _ string) []string {
	d.got.in, d.got.set, d.got.err = rtx.PartialInputs[partialInputs]()
	return []string{}
}

func (d partialDeploy) CompleteFlagValue(rtx *Context, _, _ string) []string {
	return d.CompleteArgValue(rtx, "", "")
}

func (h partialHandlers) AppDeploy() Handler { return partialDeploy{got: h.got} }

// failReader fails the test if anything reads it.
type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read during completion")
	return 0, io.EOF
}

// TestPartialInputs pins the lenient read of a half-typed line: what is typed, over the
// environment, over defaults, with nothing validated and stdin never read.
func TestPartialInputs(t *testing.T) {
	run := func(env []string, words ...string) partialResult {
		t.Helper()
		var got partialResult
		p := NewProgram(partialDef(), partialHandlers{got: &got}).WithEnviron(env).WithStdin(failReader{t})
		resultOf(t, p, words...)
		if got.err != nil {
			t.Fatalf("%q: PartialInputs error %v", words, got.err)
		}
		return got
	}

	got := run(nil, "--token", "t", "deploy", "--replicas", "3", "--bogus", "api", "--region", "")
	if got.in.AppDeploy.Flags.Replicas != 3 || got.in.AppDeploy.Flags.Region != "" || got.in.AppDeploy.Arguments.Service != "api" || got.in.App.Flags.Token != "t" {
		t.Errorf("typed so far: %+v", got.in)
	}
	if got.in.App.Flags.Store != "memory" {
		t.Errorf("default: store = %q, want memory", got.in.App.Flags.Store)
	}
	if src, ok := got.set["App.Flags.Store"]; !ok || src.Layer != "defaults" {
		t.Errorf("presence of the default: %+v", got.set)
	}

	got = run([]string{"PARTIAL_STORE=disk"}, "deploy", "--replicas", "lots", "--unknown", "--region", "")
	if got.in.App.Flags.Store != "disk" || got.in.AppDeploy.Flags.Replicas != 0 {
		t.Errorf("env over default, bad value skipped: %+v", got.in)
	}

	got = run(nil, "--payload", "-", "deploy", "--store", "@cfg", "")
	if got.in.App.Flags.Payload != "-" || got.in.App.Flags.Store != "@cfg" {
		t.Errorf("acquisition kept as typed: %+v", got.in)
	}

	got = run(nil, "deploy", "--region", "=", "us", "")
	if got.in.AppDeploy.Flags.Region != "us" {
		t.Errorf("bash split: region = %q, want us", got.in.AppDeploy.Flags.Region)
	}

	// Outside a completion request the whole Argv is read.
	rtx := NewContextFor(partialDef(), []string{"deploy", "--region", "eu"})
	in, _, err := rtx.PartialInputs[partialInputs]()
	if err != nil || in.AppDeploy.Flags.Region != "eu" {
		t.Errorf("outside a request: %+v, %v", in.AppDeploy.Flags, err)
	}

	// A type that doesn't describe the running command is the error.
	type wrong struct{ A, B, C struct{} }
	if _, _, err := rtx.PartialInputs[wrong](); err == nil {
		t.Error("a misfit type: no error")
	} else if pe, ok := errors.AsType[*ParseError](err); !ok || pe.Kind != ParseKindInternal {
		t.Errorf("a misfit type: %v, want an internal ParseError", err)
	}
}

// TestComplete_lenientRecipeWithPartialInputs is the documented recipe: an argument
// completer reads an ancestor's flag from the line, else its environment fallback, though a
// required flag is missing.
func TestComplete_lenientRecipeWithPartialInputs(t *testing.T) {
	var got partialResult
	p := NewProgram(partialDef(), partialHandlers{got: &got}).WithEnviron([]string{"PARTIAL_STORE=env"})
	resultOf(t, p, "deploy", "")
	if got.in.App.Flags.Store != "env" {
		t.Errorf("env fallback: store = %q", got.in.App.Flags.Store)
	}
	resultOf(t, p, "--store", "line", "deploy", "")
	if got.in.App.Flags.Store != "line" {
		t.Errorf("argv over env: store = %q", got.in.App.Flags.Store)
	}
	if !reflect.DeepEqual(got.in.AppDeploy, partialInputs{}.AppDeploy) {
		t.Errorf("nothing typed for deploy: %+v", got.in.AppDeploy)
	}
}

// TestComplete_bashGlueAsTheWord pins bash's split when the cursor sits right after "=": the
// word is "=" itself, so nothing is offered and the hint is the value's.
func TestComplete_bashGlueAsTheWord(t *testing.T) {
	def := mapDef()
	def.Flags = append(def.Flags, FlagDef{Name: "out", Identifiers: []string{"--out"}, Type: "string", Complete: Completion{Kind: "directory"}})
	for _, c := range []struct {
		words []string
		hint  string
	}{
		{[]string{"--set", "image", "="}, ":rotini:none"},
		{[]string{"--out", "="}, ":rotini:directory"},
	} {
		if got := complete(def, c.words, nil, nil); len(got) != 0 {
			t.Errorf("%q: %v, want nothing", c.words, got)
		}
		if got := completionHint(def, c.words); got != c.hint {
			t.Errorf("%q: hint %q, want %q", c.words, got, c.hint)
		}
	}
}
