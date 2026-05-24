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
