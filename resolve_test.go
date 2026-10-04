package rotini

import (
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func chainNames(chain []Command) []string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.Name
	}
	return names
}

func TestResolveChain_descendsAndSkipsFlagValues(t *testing.T) {
	// --count takes a value ("3"); it must not be mistaken for a command, and the
	// first positional ("alice") stops descent at run.
	chain, plugin := resolveChain(testDef(), []string{"--verbose", "run", "--count", "3", "alice", "x"})
	if plugin != nil {
		t.Fatalf("unexpected plugin dispatch: %+v", plugin)
	}
	if got := chainNames(chain); len(got) != 2 || got[0] != "app" || got[1] != "run" {
		t.Errorf("chain = %v, want [app run]", got)
	}
}

// A command tree deeper than two levels resolves to the leaf, with flags (and their separate
// values) interleaved at every level and a trailing positional stopping descent.
func TestResolveChain_deepThreeLevels(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "v", Identifiers: []string{"--v"}, Type: "bool"}},
		Commands: []CommandDef{{
			Name: "a", Handler: "A",
			Flags: []FlagDef{{Name: "o", Identifiers: []string{"--o"}, Type: "string"}},
			Commands: []CommandDef{{
				Name: "b", Handler: "AB",
				Commands: []CommandDef{{
					Name:    "c",
					Handler: "ABC",
					Flags:   []FlagDef{{Name: "n", Identifiers: []string{"--n"}, Type: "int"}},
				}},
			}},
		}},
	}
	// --v (bool, root) · a · --o x (string value at level a, x is not command b) ·
	// b · c · --n 5 (int value at leaf c) · pos (first positional → stop).
	chain, plugin := resolveChain(def, []string{"--v", "a", "--o", "x", "b", "c", "--n", "5", "pos"})
	if plugin != nil {
		t.Fatalf("unexpected plugin dispatch: %+v", plugin)
	}
	if got := chainNames(chain); !reflect.DeepEqual(got, []string{"app", "a", "b", "c"}) {
		t.Errorf("chain = %v, want [app a b c]", got)
	}
}

// "--" terminates command descent: every following token is positional, so neither a
// declared sub-command, a declared plugin, nor plugin discovery fires after it.
func TestResolveChain_doubleDashTerminator(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands:        []CommandDef{{Name: "run", Handler: "AppRun"}},
		Plugins:         []PluginDef{{Name: "ext", Binary: "app-ext"}},
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"},
	}
	for _, name := range []string{"run", "ext", "anything"} {
		chain, plugin := resolveChain(def, []string{"--", name})
		if plugin != nil {
			t.Errorf("%q after -- triggered a dispatch: %+v", name, plugin)
		}
		if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
			t.Errorf("chain after [-- %s] = %v, want [app]", name, got)
		}
	}
	// A "--" after a descended command stops further descent at that command.
	chain, _ := resolveChain(def, []string{"run", "--", "sub"})
	if got := chainNames(chain); !reflect.DeepEqual(got, []string{"app", "run"}) {
		t.Errorf("chain = %v, want [app run] (sub after -- is positional)", got)
	}
}

func TestResolveChain_flagValueNotMistakenForCommand(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:    []FlagDef{{Name: "out", Identifiers: []string{"--out"}, Type: "string"}},
		Commands: []CommandDef{{Name: "run", Handler: "AppRun"}},
	}
	// "--out run" — "run" is the flag's value, so the chain must stay at the root.
	chain, _ := resolveChain(def, []string{"--out", "run"})
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app] (run was a flag value)", got)
	}
}

func TestResolveChain_detectsPlugin(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Plugins: []PluginDef{{Name: "ext", Binary: "app-ext"}},
	}
	chain, plugin := resolveChain(def, []string{"ext", "a", "b"})
	if plugin == nil {
		t.Fatalf("expected a plugin dispatch")
	}
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app]", got)
	}
	if len(plugin.Args) != 2 || plugin.Args[0] != "a" || plugin.Args[1] != "b" {
		t.Errorf("plugin args = %v, want [a b]", plugin.Args)
	}
}

func TestResolveChain_discoversPlugin(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:        []CommandDef{{Name: "cluster", Handler: "AcmeCluster"}},
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, PluginPath: "/opt/acme/plugins",
	}

	// A declared sub-command still wins over discovery.
	if _, plugin := resolveChain(def, []string{"cluster"}); plugin != nil {
		t.Errorf("declared command should not be a discovery dispatch")
	}

	// An unmatched token at a discovery-enabled command dispatches to <prefix><token>.
	chain, plugin := resolveChain(def, []string{"foo", "x", "y"})
	if plugin == nil {
		t.Fatal("expected a discovery dispatch for an unmatched token")
	}
	if plugin.Def.Name != "foo" || plugin.Def.Binary != "acme-foo" {
		t.Errorf("discovery dispatch = {Name:%q Binary:%q}, want {foo acme-foo}", plugin.Def.Name, plugin.Def.Binary)
	}
	if plugin.Dir != "/opt/acme/plugins" {
		t.Errorf("dispatch dir = %q, want /opt/acme/plugins", plugin.Dir)
	}
	if len(plugin.Args) != 2 || plugin.Args[0] != "x" || plugin.Args[1] != "y" {
		t.Errorf("discovery args = %v, want [x y]", plugin.Args)
	}
	_ = chain

	// Without discovery, an unmatched token is just a positional (no dispatch).
	plain := Definition{Name: "acme", Handler: "App"}
	if _, plugin := resolveChain(plain, []string{"foo"}); plugin != nil {
		t.Errorf("no discovery → unmatched token should not dispatch")
	}
}

func TestResolveChain_negativeNumberStopsDescent(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{{Name: "n", Type: "int"}},
		Commands:  []CommandDef{{Name: "sub", Handler: "AppSub"}},
	}
	// "-5" is a negative-number positional, so it stops descent — the following "sub"
	// is a second positional, not a sub-command.
	chain, plugin := resolveChain(def, []string{"-5", "sub"})
	if plugin != nil {
		t.Fatalf("unexpected dispatch: %+v", plugin)
	}
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app] (-5 is a positional)", got)
	}
}

func TestResolveChain_negativeNumberNotPluginDispatch(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"},
	}
	// A negative number at a discovery-enabled command is a positional, not a plugin
	// token — it must not dispatch acme--5.
	if _, plugin := resolveChain(def, []string{"-5"}); plugin != nil {
		t.Errorf("negative number triggered discovery dispatch: %+v", plugin)
	}
}

func TestResolveChain_negativeNumberAsFlagValue(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags:    []FlagDef{{Name: "offset", Identifiers: []string{"--offset"}, Type: "int"}},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub"}},
	}
	// "--offset -5" — -5 is the flag's value; descent continues to "sub".
	chain, _ := resolveChain(def, []string{"--offset", "-5", "sub"})
	if got := chainNames(chain); len(got) != 2 || got[1] != "sub" {
		t.Errorf("chain = %v, want [app sub] (-5 was --offset's value)", got)
	}
}

// TestResolve_pluginPathExpandsHome pins that a declared plugin_path expands a leading ~ to the
// home directory and $VAR from the environment.
func TestResolve_pluginPathExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APP_PLUGINS", "/opt/app/plugins")
	for decl, want := range map[string]string{
		"~/.app/plugins": filepath.Join(home, ".app", "plugins"),
		"~":              home,
		"$APP_PLUGINS":   "/opt/app/plugins",
		"./plugins":      "./plugins",
		"":               "",
	} {
		root := rootFrame(Definition{Name: "app", PluginPath: decl})
		sub := cmdFrame(CommandDef{Name: "sub", PluginPath: decl})
		if root.PluginPath != want || sub.PluginPath != want {
			t.Errorf("plugin_path %q resolved to %q (root) / %q (sub), want %q", decl, root.PluginPath, sub.PluginPath, want)
		}
	}
}

// TestResolver_definitionIsTheProgramsOwnTree pins the documented Resolver convention: the
// Definition is passed by value, but its slices are the program's own, so an edit through one
// persists into later runs.
func TestResolver_definitionIsTheProgramsOwnTree(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).WithStdout(io.Discard).WithStderr(io.Discard)

	var secondSaw string
	first := true
	p.WithResolver(func(def Definition, argv []string) (Resolution, error) {
		if first {
			def.Name = "value-field"                   // a value field: local to this copy
			def.Commands[0].Name = "through-the-slice" // a slice element: the program's own
			first = false
		} else {
			secondSaw = def.Name + "/" + def.Commands[0].Name
		}
		return DefaultResolver(def, argv)
	})

	p.Run([]string{"run", "x"})
	p.Run([]string{"run", "x"})

	if !strings.HasPrefix(secondSaw, "app/") {
		t.Errorf("a value field leaked across runs: %q", secondSaw)
	}
	if !strings.HasSuffix(secondSaw, "through-the-slice") {
		t.Fatalf("the fixture no longer demonstrates the hazard: %q", secondSaw)
	}
}

// TestResolver_emptyChainIsReportedNotCrashed pins that a custom resolver returning an empty
// chain is reported as a failure and runs no handler.
func TestResolver_emptyChainIsReportedNotCrashed(t *testing.T) {
	var ran []string
	p := NewProgram(testDef(), seamProgram{ran: &ran}).
		WithResolver(func(Definition, []string) (Resolution, error) { return Resolution{}, nil }).
		WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code == 0 || err == nil {
		t.Fatalf("an empty chain gave (%d, %v), want a reported failure", code, err)
	}
	if !strings.Contains(err.Error(), "empty chain") {
		t.Errorf("error = %v, want it to name the empty chain", err)
	}
	if len(ran) != 0 {
		t.Errorf("handlers ran on an empty chain: %v", ran)
	}
}

// TestResolver_errorIsRoutedNotPanicked pins that a resolver error is reported, not panicked.
func TestResolver_errorIsRoutedNotPanicked(t *testing.T) {
	p := NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithResolver(func(Definition, []string) (Resolution, error) {
			return Resolution{}, errors.New("resolver said no")
		}).WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code == 0 || err == nil || !strings.Contains(err.Error(), "resolver said no") {
		t.Errorf("(%d, %v), want the resolver's error routed through the reporter", code, err)
	}
}

// TestResolve_agreesWithTheParserOnFlagValues pins command resolution to the parser's rule for
// which word is a flag's value: an optional value is never a separate word (`app --color sub`),
// and a short cluster's last flag takes the next word (`app -vn 5 sub`).
func TestResolve_agreesWithTheParserOnFlagValues(t *testing.T) {
	def := Definition{Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "color", Identifiers: []string{"--color"}, Type: "string", ImplicitValue: "always"},
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "num", Identifiers: []string{"-n"}, Type: "int"},
			{Name: "db", Identifiers: []string{"--db"}, Type: "DB", ObjectSchema: `{"type":"object"}`},
		},
		Commands: []CommandDef{{Name: "sub", Handler: "AppSub"}}}
	for _, argv := range [][]string{
		{"--color", "sub"},
		{"--color=never", "sub"},
		{"-vn", "5", "sub"},
		{"-n", "5", "sub"},
		{"-v", "sub"},
		{"--db.host", "h", "sub"},
	} {
		chain, _ := resolveChain(def, argv)
		if leaf := chain[len(chain)-1].Name; leaf != "sub" {
			t.Errorf("resolve %q → %q, want sub", argv, leaf)
		}
	}
}
