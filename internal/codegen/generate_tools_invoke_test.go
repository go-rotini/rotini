package codegen

import (
	"encoding/json"
	"testing"
)

// TestToolInvoke_facts pins the invoke facts a server writes argv from: secret and from on each
// parameter, passthrough only for a passthrough command, the identifier rotini.ArgvOf picks
// (never a deprecated one), and whether a count has a default.
func TestToolInvoke_facts(t *testing.T) {
	yes := true
	cmd := agentContractCmd{
		Path: []string{"run"},
		Arguments: []agentInput{
			{Name: "files", Kind: "list", Variadic: true, Passthrough: true, From: []string{"file"}, Agent: &yes},
		},
		Flags: []agentInput{
			{Name: "token", Identifiers: []string{"--token"}, Kind: "scalar", Type: "string", Secret: true, From: []string{"file", "stdin"}, Agent: &yes},
			{Name: "color", Identifiers: []string{"--colour", "-c", "--color"}, DeprecatedIdentifiers: []string{"--colour"}, Kind: "scalar", Type: "string"},
			{Name: "level", Identifiers: []string{"--lvl", "-l"}, DeprecatedIdentifiers: []string{"--lvl"}, Kind: "scalar", Type: "string"},
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Kind: "count", Type: "count", Schema: map[string]any{"type": "integer", "default": 1}},
			{Name: "quiet", Identifiers: []string{"-q"}, Kind: "count", Type: "count"},
		},
	}
	a := &agentProgram{doc: agentDoc{Name: "app"}}
	tl, skip := buildTool(a, agentCommand{c: cmd, leaf: true, offered: true, invocation: "app run"}, false)
	if skip != "" {
		t.Fatal(skip)
	}
	got, err := json.Marshal(tl.invoke)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"path":["run"],` +
		`"arguments":[{"name":"files","kind":"list","variadic":true,"passthrough":true,"from":["file"]}],` +
		`"flags":{` +
		`"color":{"flag":"--color","kind":"scalar","type":"string"},` +
		`"level":{"flag":"-l","kind":"scalar","type":"string"},` +
		`"quiet":{"flag":"-q","kind":"count","type":"count"},` +
		`"token":{"flag":"--token","kind":"scalar","type":"string","secret":true,"from":["file","stdin"]},` +
		`"verbose":{"flag":"--verbose","kind":"count","type":"count","has_default":true}}}`
	if string(got) != want {
		t.Errorf("invoke =\n%s\nwant\n%s", got, want)
	}

	cmd.Passthrough = true
	tl, _ = buildTool(a, agentCommand{c: cmd, leaf: true, offered: true, invocation: "app run"}, false)
	if !tl.invoke.Passthrough {
		t.Error("a passthrough command's invoke facts lack passthrough")
	}
}
