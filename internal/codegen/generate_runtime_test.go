package codegen

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const runtimeLaneSpec = `version: 0.0.0
command:
  name: busybox
  display_name: bb tool
  multicall:
    prefix: bb-
    complete: kubectl_complete-
  flags:
    - name: dir
      identifiers: [-C, --dir]
      role: chdir
      cascading: true
      schema:
        type: existingdir
  commands:
    - name: ls
      aliases: [dir]
      arguments:
        - name: path
          schema:
            type: string
    - name: cat
      usage: "bb tool cat FILE..."
      help: "a verbatim page\n"
    - name: admin
      hidden: true
      commands:
        - name: purge
`

func runtimeLaneProgram(t *testing.T, body string) *program {
	t.Helper()
	spec := decodeSpecYAML(t, body)
	gp, err := resolveTree(spec, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = spec // set by the generate pipeline; root-level settings read it
	return gp
}

// runtimeLaneContract renders body's contract, checked against schema-contract.json.
func runtimeLaneContract(t *testing.T, body string) []byte {
	t.Helper()
	gp := runtimeLaneProgram(t, body)
	raw, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Fatalf("the contract does not match schema-contract.json: %v\n%s", problems, raw)
	}
	return raw
}

// The Definition carries each command's usage line: the declared one when set (even beside a
// verbatim help page), else the derived one, with the display name's invocation.
func TestDefinitionLiteral_usage(t *testing.T) {
	t.Parallel()
	def := renderDefinition(runtimeLaneProgram(t, runtimeLaneSpec))
	for _, want := range []string{
		`Usage: "bb tool [flags] <command>",`,
		`Usage: "bb tool ls [path]",`,
		`Usage: "bb tool cat FILE...",`,
		`Usage: "bb tool admin <command>",`,
		`Usage: "bb tool admin purge",`,
		`Multicall: &rotini.MulticallDef{Prefix: "bb-", Complete: "kubectl_complete-"},`,
		`Role: "chdir"`,
	} {
		if !strings.Contains(def, want) {
			t.Errorf("definition lacks %s\n%s", want, def)
		}
	}
}

// The generated Usage function answers for every command, hidden ones and aliases included.
func TestUsageFuncDecl(t *testing.T) {
	t.Parallel()
	src := usageFuncDecl(runtimeLaneProgram(t, runtimeLaneSpec))
	for _, want := range []string{
		"case \"\":\n\t\treturn \"bb tool [flags] <command>\", nil",
		"case \"ls\", \"dir\":\n\t\treturn \"bb tool ls [path]\", nil",
		"case \"admin purge\":\n\t\treturn \"bb tool admin purge\", nil",
		`return "", fmt.Errorf("no usage for command %q", strings.Join(path, " "))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Usage function lacks %q\n%s", want, src)
		}
	}
}

func TestMulticallOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value            any
		on               bool
		prefix, complete string
	}{
		{nil, false, "", ""},
		{false, false, "", ""},
		{true, true, "", ""},
		{map[string]any{"prefix": "a-"}, true, "a-", ""},
		{map[string]any{"complete": "c-"}, true, "", "c-"},
	}
	for _, c := range cases {
		on, prefix, complete := multicallOf(&Command{Multicall: c.value})
		if on != c.on || prefix != c.prefix || complete != c.complete {
			t.Errorf("multicallOf(%v) = %v %q %q", c.value, on, prefix, complete)
		}
	}
	if lit := multicallLiteral(&program{spec: &Spec{Command: Command{Multicall: true}}}); lit != "Multicall: &rotini.MulticallDef{},\n" {
		t.Errorf("multicall: true renders %q", lit)
	}
}

func TestContract_multicallAndChdirRole(t *testing.T) {
	t.Parallel()
	raw := runtimeLaneContract(t, runtimeLaneSpec)
	_, commands := contractJSON(t, runtimeLaneSpec)
	var doc struct {
		Multicall map[string]string `json:"multicall"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Multicall["prefix"] != "bb-" || doc.Multicall["complete"] != "kubectl_complete-" {
		t.Errorf("contract multicall = %v", doc.Multicall)
	}
	if role := entry(t, commands["busybox"], "flags", "dir")["role"]; role != "chdir" {
		t.Errorf("dir role = %v, want chdir", role)
	}

	raw = runtimeLaneContract(t, strings.Replace(runtimeLaneSpec, "  multicall:\n    prefix: bb-\n    complete: kubectl_complete-\n", "  multicall: true\n", 1))
	if !strings.Contains(string(raw), `"multicall": {}`) {
		t.Errorf("multicall: true should be an empty object in the contract:\n%s", raw)
	}
}

// multicall is a root key; a sub-command setting it is reported there.
func TestLintMulticall_rootOnly(t *testing.T) {
	t.Parallel()
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: app
  commands:
    - name: sub
      multicall: true
`)
	problems := lintMulticall(spec)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "root-command-level key") {
		t.Errorf("problems = %v", problems)
	}
}
