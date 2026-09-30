package rotini

import (
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func chainNames(chain []ResolvedCommand) []string {
	names := make([]string, len(chain))
	for i, f := range chain {
		names[i] = f.Name
	}
	return names
}

func TestResolveChain_descendsAndSkipsFlagValues(t *testing.T) {
	// --count takes a value ("3"); it must not be mistaken for a command, and the
	// first positional ("alice") stops descent at run.
	chain, remote := resolveChain(testDef(), []string{"--verbose", "run", "--count", "3", "alice", "x"})
	if remote != nil {
		t.Fatalf("unexpected remote dispatch: %+v", remote)
	}
	if got := chainNames(chain); len(got) != 2 || got[0] != "app" || got[1] != "run" {
		t.Errorf("chain = %v, want [app run]", got)
	}
}

// A command tree deeper than two levels resolves all the way to the leaf, with
// flags (and their separate values) interleaved at every level and a trailing
// positional stopping descent — the recursive descent isn't special-cased to one or
// two levels.
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
	chain, remote := resolveChain(def, []string{"--v", "a", "--o", "x", "b", "c", "--n", "5", "pos"})
	if remote != nil {
		t.Fatalf("unexpected remote dispatch: %+v", remote)
	}
	if got := chainNames(chain); !reflect.DeepEqual(got, []string{"app", "a", "b", "c"}) {
		t.Errorf("chain = %v, want [app a b c]", got)
	}
}

// "--" terminates command descent: every following token is positional, so neither a
// declared sub-command, a remote command, nor plugin discovery fires after it.
func TestResolveChain_doubleDashTerminator(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands:       []CommandDef{{Name: "run", Handler: "AppRun"}},
		RemoteCommands: []RemoteDef{{Name: "ext", Binary: "app-ext"}},
		Discovery:      &RemoteDiscoveryDef{Prefix: "app-"},
	}
	for _, name := range []string{"run", "ext", "anything"} {
		chain, remote := resolveChain(def, []string{"--", name})
		if remote != nil {
			t.Errorf("%q after -- triggered a dispatch: %+v", name, remote)
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

func TestResolveChain_detectsRemote(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		RemoteCommands: []RemoteDef{{Name: "ext", Binary: "app-ext"}},
	}
	chain, remote := resolveChain(def, []string{"ext", "a", "b"})
	if remote == nil {
		t.Fatalf("expected a remote dispatch")
	}
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app]", got)
	}
	if len(remote.Args) != 2 || remote.Args[0] != "a" || remote.Args[1] != "b" {
		t.Errorf("remote args = %v, want [a b]", remote.Args)
	}
}

func TestResolveChain_discoversPlugin(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:  []CommandDef{{Name: "cluster", Handler: "AcmeCluster"}},
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-"}, PluginPath: "/opt/acme/plugins",
	}

	// A declared sub-command still wins over discovery.
	if _, remote := resolveChain(def, []string{"cluster"}); remote != nil {
		t.Errorf("declared command should not be a discovery dispatch")
	}

	// An unmatched token at a discovery-enabled command dispatches to <prefix><token>.
	chain, remote := resolveChain(def, []string{"foo", "x", "y"})
	if remote == nil {
		t.Fatal("expected a discovery dispatch for an unmatched token")
	}
	if remote.Def.Name != "foo" || remote.Def.Binary != "acme-foo" {
		t.Errorf("discovery dispatch = {Name:%q Binary:%q}, want {foo acme-foo}", remote.Def.Name, remote.Def.Binary)
	}
	if remote.Dir != "/opt/acme/plugins" {
		t.Errorf("dispatch dir = %q, want /opt/acme/plugins", remote.Dir)
	}
	if len(remote.Args) != 2 || remote.Args[0] != "x" || remote.Args[1] != "y" {
		t.Errorf("discovery args = %v, want [x y]", remote.Args)
	}
	_ = chain

	// Without discovery, an unmatched token is just a positional (no dispatch).
	plain := Definition{Name: "acme", Handler: "App"}
	if _, remote := resolveChain(plain, []string{"foo"}); remote != nil {
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
	chain, remote := resolveChain(def, []string{"-5", "sub"})
	if remote != nil {
		t.Fatalf("unexpected dispatch: %+v", remote)
	}
	if got := chainNames(chain); len(got) != 1 || got[0] != "app" {
		t.Errorf("chain = %v, want [app] (-5 is a positional)", got)
	}
}

func TestResolveChain_negativeNumberNotPluginDispatch(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-"},
	}
	// A negative number at a discovery-enabled command is a positional, not a plugin
	// token — it must not dispatch acme--5.
	if _, remote := resolveChain(def, []string{"-5"}); remote != nil {
		t.Errorf("negative number triggered discovery dispatch: %+v", remote)
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

// TestResolve_pluginPathExpandsHome proves a declared plugin_path means what a shell would make
// of it: `~/.app/plugins` is under the user's home and `$VAR` reads the environment, as a
// configuration file's path does. Unexpanded, the search looked in a directory literally named
// "~" and the not-found message claimed to have searched "~/.app/plugins" (rubectl R-36).
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

// TestResolver_definitionIsTheProgramsOwnTree is the hazard the Resolver doc now names.
//
// Definition arrives by value, but it is mostly slices, and those are the program's own. An edit
// through one outlives the run — which for a REPL means every line after the
// first sees a tree the previous line rewrote.
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
	// The behaviour is the documented convention, not a bug to fix: deep-copying the tree per
	// run is what the convention exists to avoid. This pins that the doc and the runtime agree.
}

// TestResolver_emptyChainIsReportedNotCrashed: Resolution.Chain is documented non-empty, and a
// custom resolver is the only thing that can break that.
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

// TestResolver_errorIsRoutedNotPanicked.
func TestResolver_errorIsRoutedNotPanicked(t *testing.T) {
	p := NewProgram(testDef(), seamProgram{ran: new([]string)}).
		WithResolver(func(Definition, []string) (Resolution, error) {
			return Resolution{}, errors.New("resolver said no")
		}).WithStdout(io.Discard).WithStderr(io.Discard)

	code, err := p.Run([]string{"run", "x"})
	if code == 0 || err == nil || !strings.Contains(err.Error(), "resolver said no") {
		t.Errorf("(%d, %v), want the resolver's error routed through the funnel", code, err)
	}
}

// TestResolve_agreesWithTheParserOnFlagValues pins command resolution to the parser's rule for
// which word is a flag's value. They used to disagree: the resolver skipped the word after ANY
// value-taking flag, so `app --color sub` (an optional value, which must be attached) never
// reached sub, and it skipped nothing after a short cluster, so in `app -vn 5 sub` the 5 ended
// descent and sub was never reached either.
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
