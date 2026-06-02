package rotini

import "testing"

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
	if len(remote.args) != 2 || remote.args[0] != "a" || remote.args[1] != "b" {
		t.Errorf("remote args = %v, want [a b]", remote.args)
	}
}

func TestResolveChain_discoversPlugin(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:  []CommandDef{{Name: "cluster", Handler: "AcmeCluster"}},
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: "/opt/acme/plugins"},
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
	if remote.def.Name != "foo" || remote.def.Binary != "acme-foo" {
		t.Errorf("discovery dispatch = {Name:%q Binary:%q}, want {foo acme-foo}", remote.def.Name, remote.def.Binary)
	}
	if remote.dir != "/opt/acme/plugins" {
		t.Errorf("dispatch dir = %q, want /opt/acme/plugins", remote.dir)
	}
	if len(remote.args) != 2 || remote.args[0] != "x" || remote.args[1] != "y" {
		t.Errorf("discovery args = %v, want [x y]", remote.args)
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
