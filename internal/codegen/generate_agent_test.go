package codegen

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestContract_agentFacts pins the contract fields for effects, roles and `agent`, and that the
// contract carrying them still matches schema-contract.json.
func TestContract_agentFacts(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("testdata", "program", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, cmds := contractJSON(t, string(spec))
	purge := cmds["taskr purge"]
	if !reflect.DeepEqual(purge["effects"], map[string]any{"kind": "destructive", "idempotent": true}) {
		t.Errorf("purge effects = %v", purge["effects"])
	}
	if got := entry(t, purge, "flags", "dry-run")["role"]; got != "dry-run" {
		t.Errorf("dry-run role = %v", got)
	}
	format := entry(t, cmds["taskr list"], "flags", "format")
	if format["role"] != "machine-output" || format["role_value"] != "json" {
		t.Errorf("format = %v", format)
	}
	prune := entry(t, cmds["taskr remote sync"], "flags", "prune")
	if !reflect.DeepEqual(prune["effects"], map[string]any{"kind": "destructive"}) {
		t.Errorf("prune effects = %v", prune["effects"])
	}
	if cmds["taskr admin"]["agent"] != false {
		t.Errorf("admin agent = %v, want false", cmds["taskr admin"]["agent"])
	}
	if _, ok := cmds["taskr add"]["agent"]; ok {
		t.Error("agent is written when it isn't declared")
	}
}

// TestWorstEffects pins how a command's effects combine with its flags' for consumers that
// can't see the command line.
func TestWorstEffects(t *testing.T) {
	yes, no := new(true), new(false)
	for _, tt := range []struct {
		name  string
		cmd   *Effects
		flags []*Effects
		want  *Effects
	}{
		{"undeclared", nil, []*Effects{{Kind: "destructive"}}, nil},
		{"no flags", &Effects{Kind: "read"}, nil, &Effects{Kind: "read"}},
		{"raised", &Effects{Kind: "write", Idempotent: yes}, []*Effects{{Kind: "destructive"}}, &Effects{Kind: "destructive", Idempotent: yes}},
		{"not idempotent wins", &Effects{Kind: "write", Idempotent: yes}, []*Effects{{Kind: "write", Idempotent: no}}, &Effects{Kind: "write", Idempotent: no}},
		{"open world wins", &Effects{Kind: "read", OpenWorld: no}, []*Effects{{Kind: "read", OpenWorld: yes}}, &Effects{Kind: "read", OpenWorld: yes}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := worstEffects(tt.cmd, tt.flags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("worstEffects = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestEffectsText pins the line help, man and markdown show: only the facts stated.
func TestEffectsText(t *testing.T) {
	for _, tt := range []struct {
		e    *Effects
		want string
	}{
		{nil, ""},
		{&Effects{Kind: "read"}, "read"},
		{&Effects{Kind: "destructive", Idempotent: new(false), OpenWorld: new(true)}, "destructive, not idempotent, open world"},
		{&Effects{Kind: "write", Idempotent: new(true), OpenWorld: new(false)}, "write, idempotent, local only"},
	} {
		if got := effectsText(tt.e); got != tt.want {
			t.Errorf("effectsText(%+v) = %q, want %q", tt.e, got, tt.want)
		}
	}
}

// TestMCPAnnotations pins the mapping from effects to MCP tool annotations; an explicit
// open_world: false is written, since MCP assumes open world.
func TestMCPAnnotations(t *testing.T) {
	if mcpAnnotations(nil) != nil {
		t.Error("undeclared effects should write no annotations")
	}
	got := mcpAnnotations(&Effects{Kind: "write", OpenWorld: new(false)})
	want := map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("write = %v, want %v", got, want)
	}
	if got := mcpAnnotations(&Effects{Kind: "read"}); !reflect.DeepEqual(got, map[string]any{"readOnlyHint": true}) {
		t.Errorf("read = %v", got)
	}
}

// TestExplainKey pins key lookup through $ref, lists and maps, and the nearest-key message.
func TestExplainKey(t *testing.T) {
	infos, err := ExplainKey("command.flags.schema.from")
	if err != nil || len(infos) != 1 || infos[0].Document != "spec" || infos[0].Type == "" {
		t.Fatalf("command.flags.schema.from = %+v, %v", infos, err)
	}
	if infos, err := ExplainKey("command.commands.flags.effects"); err != nil || infos[0].Type != "Effects" || !reflect.DeepEqual(infos[0].Keys, []string{"idempotent", "kind", "open_world"}) {
		t.Errorf("a nested $ref key = %+v, %v", infos, err)
	}
	if infos, err := ExplainKey("$schema"); err != nil || len(infos) != 2 {
		t.Errorf("$schema = %+v, %v; want one per document", infos, err)
	}
	if _, err := ExplainKey("generate.featurs"); err == nil || err.Error() != `no key "featurs" under generate; did you mean "generate.features"?` {
		t.Errorf("err = %v", err)
	}
	if got := ExplainCandidates("command.effects.k"); !reflect.DeepEqual(got, []string{"command.effects.kind"}) {
		t.Errorf("candidates = %q", got)
	}
}

// TestProblemRecordOf pins the structured form of a positioned lint problem.
func TestProblemRecordOf(t *testing.T) {
	p := &problem{kind: "spec", loc: "command demo/add", ptr: "/command/commands/0/flags/1/role", pos: "dir/.rotini.spec.yaml:12:7",
		msg: `flag "x": has role page but is a bool; the page role marks a page number`, sev: severityWarning}
	got := ProblemRecordOf(p, true)
	want := ProblemRecord{Severity: "warning", Document: "spec", File: "dir/.rotini.spec.yaml", Line: 12, Col: 7,
		Pointer: "/command/commands/0/flags/1/role", Message: `command demo/add: flag "x": has role page but is a bool`, Hint: "the page role marks a page number"}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	schemaErr := &problem{kind: "conf", loc: "/generate/features/0/targets", pos: "c.yaml", msg: "unknown key"}
	if r := ProblemRecordOf(schemaErr, false); r.Pointer != "/generate/features/0/targets" || r.File != "c.yaml" || r.Line != 0 || r.Message != "unknown key" {
		t.Errorf("schema problem = %+v", r)
	}
}
