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
	tl, skip, _ := buildTool(a, agentCommand{c: cmd, leaf: true, offered: true, invocation: "app run"}, false)
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
	tl, _, _ = buildTool(a, agentCommand{c: cmd, leaf: true, offered: true, invocation: "app run"}, false)
	if !tl.invoke.Passthrough {
		t.Error("a passthrough command's invoke facts lack passthrough")
	}
}

// TestToolInvoke_argvQuoting pins the invoke facts that match how rotini.ArgvOf spells values:
// the response-file prefix on every tool, a variadic's separator only when ArgvOf quotes its
// items, and dotted keys on a map flag.
func TestToolInvoke_argvQuoting(t *testing.T) {
	build := func(doc agentDoc, cmd agentContractCmd) toolInvoke {
		t.Helper()
		tl, skip, err := buildTool(&agentProgram{doc: doc}, agentCommand{c: cmd, leaf: true, offered: true, invocation: "app run"}, false)
		if err != nil || skip != "" {
			t.Fatal(err, skip)
		}
		return tl.invoke
	}
	cmd := agentContractCmd{Path: []string{"run"}}
	if got := build(agentDoc{Name: "app"}, cmd).ResponsePrefix; got != "" {
		t.Errorf("response_prefix without response files = %q", got)
	}
	if got := build(agentDoc{Name: "app", ResponseFiles: &contractResponseFiles{Prefix: "@"}}, cmd).ResponsePrefix; got != "@" {
		t.Errorf("response_prefix = %q, want @", got)
	}

	tags := agentInput{Name: "tags", Kind: "list", Variadic: true, Separator: ","}
	for _, tc := range []struct {
		name string
		cmd  agentContractCmd
		want string
	}{
		{"quoted", agentContractCmd{Arguments: []agentInput{tags}}, ","},
		{"nul", agentContractCmd{Arguments: []agentInput{{Name: "tags", Kind: "list", Variadic: true, Separator: "nul"}}}, "\x00"},
		{"no separator", agentContractCmd{Arguments: []agentInput{{Name: "tags", Kind: "list", Variadic: true}}}, ""},
		{"not variadic", agentContractCmd{Arguments: []agentInput{{Name: "tags", Kind: "scalar", Separator: ","}}}, ""},
		{"passthrough argument", agentContractCmd{Arguments: []agentInput{{Name: "tags", Kind: "list", Variadic: true, Passthrough: true, Separator: ","}}}, ""},
		{"passthrough command", agentContractCmd{Passthrough: true, Arguments: []agentInput{tags}}, ""},
		{"fixed tail", agentContractCmd{Arguments: []agentInput{tags, {Name: "dst", Kind: "scalar", Required: true}}}, ""},
	} {
		inv := build(agentDoc{Name: "app"}, tc.cmd)
		if got := inv.Arguments[0].Separator; got != tc.want {
			t.Errorf("%s: separator = %q, want %q", tc.name, got, tc.want)
		}
	}

	inv := build(agentDoc{Name: "app"}, agentContractCmd{Flags: []agentInput{
		{Name: "set", Identifiers: []string{"--set"}, Kind: "map", Type: "map[string]any", DottedKeys: true},
		{Name: "label", Identifiers: []string{"--label"}, Kind: "map", Type: "map[string]string"},
	}})
	if !inv.Flags["set"].DottedKeys || inv.Flags["label"].DottedKeys {
		t.Errorf("dotted_keys: set %v, label %v; want true, false", inv.Flags["set"].DottedKeys, inv.Flags["label"].DottedKeys)
	}
	got, err := json.Marshal(inv.Flags["set"])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"flag":"--set","kind":"map","type":"map[string]any","dotted_keys":true}`; string(got) != want {
		t.Errorf("set = %s, want %s", got, want)
	}
}
