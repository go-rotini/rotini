package rotini

import (
	"maps"
	"testing"
)

// TestCommandChain_metadata pins that a Definition's metadata reaches each command of the
// resolved chain, and its flags' and arguments' through their defs, unchanged.
func TestCommandChain_metadata(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Metadata: map[string]string{"owner": "platform"},
		Flags:    []FlagDef{{Name: "force", Identifiers: []string{"--force"}, Type: "bool", Metadata: map[string]string{"audit": "yes"}}},
		Commands: []CommandDef{{
			Name: "rm", Handler: "AppRm",
			Metadata:  map[string]string{"owner": "storage"},
			Arguments: []ArgDef{{Name: "target", Type: "string", Metadata: map[string]string{"kind": "host"}}},
		}},
	}
	rtx := NewContextFor(def, []string{"rm", "x"})
	chain := rtx.CommandChain()
	if len(chain) != 2 {
		t.Fatalf("chain has %d commands, want 2", len(chain))
	}
	for name, c := range map[string]struct{ got, want map[string]string }{
		"root":     {chain[0].Metadata, def.Metadata},
		"rm":       {chain[1].Metadata, def.Commands[0].Metadata},
		"invoked":  {rtx.Command().Metadata, def.Commands[0].Metadata},
		"flag":     {chain[0].Flags[0].Metadata, def.Flags[0].Metadata},
		"argument": {chain[1].Arguments[0].Metadata, def.Commands[0].Arguments[0].Metadata},
	} {
		if !maps.Equal(c.got, c.want) {
			t.Errorf("%s metadata = %v, want %v", name, c.got, c.want)
		}
	}
	if got := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).Command().Metadata; got != nil {
		t.Errorf("undeclared metadata = %v, want nil", got)
	}
}
