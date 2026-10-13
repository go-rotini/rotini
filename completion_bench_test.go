package rotini

import "testing"

// BenchmarkCompletionHintDiscovery is one hint for the first positional at a command with
// plugin discovery and sub-commands.
func BenchmarkCompletionHintDiscovery(b *testing.B) {
	def := Definition{
		Name:            "app",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"},
		PluginPath:      b.TempDir(),
		Arguments:       []ArgDef{{Name: "n", Type: "string", Complete: Completion{Kind: "file"}}},
		Commands:        []CommandDef{{Name: "sub"}},
	}
	for b.Loop() {
		completionHintFor(def, []string{""})
	}
}
