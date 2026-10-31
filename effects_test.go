package rotini

import (
	"slices"
	"testing"
)

// effectsDef is a program whose `rm` command writes, with flags that raise or qualify that,
// under a root with a cascading destructive flag.
func effectsDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "purge", Identifiers: []string{"--purge"}, Type: "bool", Effects: &Effects{Kind: "destructive"}},
			{Name: "remote", Identifiers: []string{"--remote"}, Type: "string", Effects: &Effects{Kind: "read", OpenWorld: new(true)}},
		},
		Effects: &Effects{Kind: "read", Idempotent: new(true)},
		Commands: []CommandDef{
			{
				Name: "rm", Handler: "AppRm",
				Effects: &Effects{Kind: "write", Idempotent: new(true), OpenWorld: new(false)},
				Flags: []FlagDef{
					{Name: "force", Identifiers: []string{"--force", "-f"}, Type: "bool", Negatable: true, Effects: &Effects{Kind: "destructive", Idempotent: new(false)}},
					{Name: "again", Identifiers: []string{"--again"}, Type: "count", Effects: &Effects{Kind: "write", Idempotent: new(false)}},
					{Name: "plain", Identifiers: []string{"--plain"}, Type: "bool"},
				},
			},
			{Name: "ls", Handler: "AppLs"},
		},
	}
}

func TestRunEffects(t *testing.T) {
	yes, no := new(true), new(false)
	for _, tc := range []struct {
		name string
		argv []string
		want Effects
	}{
		{"the command alone", []string{"rm"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: no}},
		{"a flag without effects", []string{"rm", "--plain"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: no}},
		{"a destructive flag raises the kind and ends idempotence", []string{"rm", "--force"}, Effects{Kind: "destructive", Idempotent: no, OpenWorld: no}},
		{"by its short identifier", []string{"rm", "-f"}, Effects{Kind: "destructive", Idempotent: no, OpenWorld: no}},
		{"a bool flag set false adds nothing", []string{"rm", "--no-force"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: no}},
		{"a bool flag set false inline adds nothing", []string{"rm", "--force=false"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: no}},
		{"a count flag counts", []string{"rm", "--again", "--again"}, Effects{Kind: "write", Idempotent: no, OpenWorld: no}},
		{"a cascading flag of an ancestor", []string{"rm", "--purge"}, Effects{Kind: "destructive", Idempotent: yes, OpenWorld: no}},
		{"any open-world value reaches outside", []string{"rm", "--remote", "x"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: yes}},
		{"the root's own effects when it is invoked", nil, Effects{Kind: "read", Idempotent: yes}},
		{"nothing declared", []string{"ls"}, Effects{}},
		{"a flag on an undeclared command", []string{"ls", "--purge"}, Effects{Kind: "destructive"}},
		{"a command line that doesn't parse", []string{"rm", "--force", "--nope"}, Effects{Kind: "write", Idempotent: yes, OpenWorld: no}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RunEffects(NewContextFor(effectsDef(), tc.argv))
			if !sameEffects(got, tc.want) {
				t.Errorf("RunEffects(%v) = %s, want %s", tc.argv, effectsString(got), effectsString(tc.want))
			}
		})
	}
	if got := RunEffects(nil); !sameEffects(got, Effects{}) {
		t.Errorf("RunEffects(nil) = %s, want zero", effectsString(got))
	}
}

// A flag's effects are read from the command line only: an environment variable setting it
// adds nothing.
func TestRunEffects_commandLineOnly(t *testing.T) {
	def := effectsDef()
	rtx := NewContextFor(def, []string{"rm"}).WithEnviron([]string{"APP_FORCE=true"})
	if got := RunEffects(rtx); got.Kind != "write" {
		t.Errorf("Kind = %q, want write", got.Kind)
	}
}

func sameEffects(a, b Effects) bool {
	eq := func(x, y *bool) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Kind == b.Kind && eq(a.Idempotent, b.Idempotent) && eq(a.OpenWorld, b.OpenWorld)
}

func effectsString(e Effects) string {
	b := func(p *bool) string {
		if p == nil {
			return "nil"
		}
		if *p {
			return "true"
		}
		return "false"
	}
	return "{" + e.Kind + " idempotent=" + b(e.Idempotent) + " open_world=" + b(e.OpenWorld) + "}"
}

// The runtime Command carries the definition's effects, exit statuses and stability, with
// stability inherited down the chain.
func TestCommand_effectsExitStatusStability(t *testing.T) {
	exits := []ExitStatusDef{{Code: 3, Name: "not_found"}}
	def := Definition{
		Name: "app", Handler: "App", Stability: "beta",
		Effects:    &Effects{Kind: "read"},
		ExitStatus: []ExitStatusDef{{Code: 2, Name: "usage"}},
		Commands: []CommandDef{{
			Name: "db", Handler: "AppDb", ExitStatus: exits,
			Commands: []CommandDef{
				{Name: "drop", Handler: "AppDbDrop", Stability: "experimental", Effects: &Effects{Kind: "destructive"}},
				{Name: "ls", Handler: "AppDbLs"},
			},
		}},
	}
	chain := NewContextFor(def, []string{"db", "drop"}).CommandChain()
	if got := []string{chain[0].Stability, chain[1].Stability, chain[2].Stability}; !slices.Equal(got, []string{"beta", "beta", "experimental"}) {
		t.Errorf("stability = %v", got)
	}
	if chain[0].Effects.Kind != "read" || chain[1].Effects != nil || chain[2].Effects.Kind != "destructive" {
		t.Errorf("effects = %v %v %v", chain[0].Effects, chain[1].Effects, chain[2].Effects)
	}
	if chain[0].ExitStatus[0].Code != 2 || chain[1].ExitStatus[0].Code != 3 || chain[2].ExitStatus != nil {
		t.Errorf("exit statuses = %v %v %v", chain[0].ExitStatus, chain[1].ExitStatus, chain[2].ExitStatus)
	}

	// A sub-command never becomes more stable than its parent, and an unknown marker counts as
	// experimental.
	def.Stability = ""
	def.Commands[0].Stability = "experimental"
	def.Commands[0].Commands[1].Stability = "beta"
	if got := NewContextFor(def, []string{"db", "ls"}).Command().Stability; got != "experimental" {
		t.Errorf("db ls stability = %q, want experimental", got)
	}
	if got := leastStable("beta", "preview"); got != "preview" {
		t.Errorf("leastStable(beta, preview) = %q", got)
	}
}
