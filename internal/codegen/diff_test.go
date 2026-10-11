package codegen

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/contractdiff"
)

// diffSpecs builds the contracts of two specs in memory, as generate writes them, and
// compares them.
func diffSpecs(t *testing.T, before, after string, opts contractdiff.Options) contractdiff.Report {
	t.Helper()
	r, err := contractdiff.Diff(specContract(t, before), specContract(t, after), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// specContract builds a spec's contract in memory, with the root-level facts (multicall,
// response files) generate reads from the whole spec, and checks it against
// schema-contract.json.
func specContract(t *testing.T, spec string) []byte {
	t.Helper()
	decoded := decodeSpecYAML(t, spec)
	gp, err := resolveTree(decoded, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = decoded
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

// findingLines renders each finding as "severity RULE where", sorted.
func findingLines(r contractdiff.Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, string(f.Severity)+" "+f.Rule+" "+f.Where)
	}
	slices.Sort(out)
	return out
}

// appSpec is a spec for the program "app" whose command block holds body, indented two spaces.
func appSpec(body string) string {
	return "version: 0.0.0\ncommand:\n  name: app\n" + body
}

// diffCase is one rule's fixture pair: the command block before and after, and every finding
// the change gives.
type diffCase struct {
	name     string
	old, new string
	release  string
	want     []string
}

func runDiffCases(t *testing.T, cases []diffCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := diffSpecs(t, appSpec(tc.old), appSpec(tc.new), contractdiff.Options{Release: tc.release})
			got := findingLines(r)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("findings:\n  got  %s\n  want %s", strings.Join(got, "\n       "), strings.Join(want, "\n       "))
			}
			for _, f := range r.Findings {
				if f.Message == "" {
					t.Errorf("%s at %s has no message", f.Rule, f.Where)
				}
				if !contractdiff.KnownRule(f.Rule) {
					t.Errorf("%s is not in the rule catalog", f.Rule)
				}
			}
		})
	}
}

func TestDiff_documentRules(t *testing.T) {
	runDiffCases(t, []diffCase{
		{
			name: "multicall added", old: "", new: "  multicall: true\n  commands:\n    - name: sub\n",
			want: []string{"safe MULTICALL_ADDED multicall", "safe COMMAND_ADDED app sub"},
		},
		{
			name: "multicall removed", old: "  multicall: true\n", new: "",
			want: []string{"breaking MULTICALL_NO_DELETE multicall"},
		},
		{
			name: "multicall prefix changed", old: "  multicall: {prefix: app-}\n", new: "  multicall: {prefix: ap-}\n",
			want: []string{"breaking MULTICALL_CHANGED multicall"},
		},
		{
			name: "topic removed and added",
			old:  "  topics:\n    - {name: env, summary: the environment, body: x}\n",
			new:  "  topics:\n    - {name: files, summary: the files, body: x}\n",
			want: []string{"possibly_breaking TOPIC_REMOVED app help env", "safe TOPIC_ADDED app help files"},
		},
		{
			name: "topic text changed", old: "  topics:\n    - {name: env, summary: a, body: x}\n",
			new:  "  topics:\n    - {name: env, summary: b, body: y}\n",
			want: nil,
		},
		{
			name: "response files added", old: "", new: "  response_files: {prefix: \"@\"}\n",
			want: []string{"breaking RESPONSE_FILES_ADDED response_files"},
		},
		{
			name: "response files prefix changed", old: "  response_files: {prefix: \"@\"}\n", new: "  response_files: {prefix: \"+\"}\n",
			want: []string{"breaking RESPONSE_FILES_PREFIX_CHANGED response_files"},
		},
		{
			name: "response files removed", old: "  response_files: {prefix: \"@\"}\n", new: "",
			want: []string{"possibly_breaking RESPONSE_FILES_REMOVED response_files"},
		},
	})
}

func TestDiff_rootRenamed(t *testing.T) {
	r := diffSpecs(t, appSpec(""), "version: 0.0.0\ncommand:\n  name: tool\n", contractdiff.Options{})
	if got := findingLines(r); !slices.Equal(got, []string{"breaking ROOT_RENAMED app"}) {
		t.Errorf("findings = %v", got)
	}
}

func TestDiff_commandRules(t *testing.T) {
	sub := func(extra string) string { return "  commands:\n    - name: compact\n" + extra }
	runDiffCases(t, []diffCase{
		{
			name: "removed", old: sub(""), new: "",
			want: []string{"breaking COMMAND_NO_DELETE app compact"},
		},
		{
			name: "removed as planned, no release", old: sub("      deprecated: use purge\n      removed_in: 2.0.0\n"), new: "",
			want: []string{"breaking COMMAND_NO_DELETE app compact"},
		},
		{
			name: "removed as planned", old: sub("      deprecated: use purge\n      removed_in: 2.0.0\n"), new: "", release: "2.0.0",
			want: []string{"expected COMMAND_NO_DELETE app compact"},
		},
		{
			name: "removed before its planned release", old: sub("      deprecated: use purge\n      removed_in: 3.0.0\n"), new: "", release: "2.0.0",
			want: []string{"breaking COMMAND_NO_DELETE app compact"},
		},
		{
			name: "removed while hidden", old: sub("      hidden: true\n"), new: "",
			want: []string{"possibly_breaking COMMAND_NO_DELETE app compact"},
		},
		{
			name:    "replaced",
			old:     "  commands:\n    - name: compact\n      deprecated: use purge\n      replaced_by: purge\n    - name: purge\n",
			new:     "  commands:\n    - name: purge\n",
			want:    []string{"expected COMMAND_REPLACED app compact"},
			release: "",
		},
		{
			name: "renamed, old name kept as an alias", old: sub(""), new: "  commands:\n    - name: squash\n      aliases: [compact]\n",
			want: []string{"safe COMMAND_RENAMED_ALIAS_KEPT app compact"},
		},
		{
			name: "added", old: "", new: sub(""),
			want: []string{"safe COMMAND_ADDED app compact"},
		},
		{
			name: "added hidden", old: "", new: sub("      hidden: true\n"),
			want: nil,
		},
		{
			name: "hidden and unhidden", old: sub("") + "    - name: other\n      hidden: true\n", new: sub("      hidden: true\n") + "    - name: other\n",
			want: []string{"possibly_breaking COMMAND_HIDDEN app compact", "safe COMMAND_UNHIDDEN app other"},
		},
		{
			name: "alias removed and added", old: sub("      aliases: [c]\n"), new: sub("      aliases: [k]\n"),
			want: []string{"breaking ALIAS_NO_DELETE app compact alias c", "safe ALIAS_ADDED app compact alias k"},
		},
		{
			name: "deprecated alias removed as planned",
			old:  sub("      aliases: [c]\n      deprecated_identifiers: [c]\n      deprecated_identifiers_removed_in: {c: 2.0.0}\n"),
			new:  sub(""), release: "2.0.0",
			want: []string{"expected ALIAS_NO_DELETE app compact alias c", "safe LIFECYCLE_CHANGED app compact"},
		},
		{
			name: "hidden alias removed and added", old: sub("      hidden_aliases: [c]\n"), new: sub("      hidden_aliases: [k]\n"),
			want: []string{"breaking HIDDEN_ALIAS_NO_DELETE app compact alias c", "safe HIDDEN_ALIAS_ADDED app compact alias k"},
		},
		{
			name: "alias moved to hidden aliases", old: sub("      aliases: [c]\n"), new: sub("      hidden_aliases: [c]\n"),
			want: []string{"safe ALIAS_MOVED app compact alias c"},
		},
		{
			name: "deprecation added", old: sub(""), new: sub("      deprecated: use purge\n"),
			want: []string{"safe DEPRECATION_ADDED app compact"},
		},
		{
			name: "deprecation changed", old: sub("      deprecated: use purge\n"), new: sub("      deprecated: use squash\n"),
			want: []string{"safe DEPRECATION_CHANGED app compact"},
		},
		{
			name: "deprecation removed", old: sub("      deprecated: use purge\n"), new: sub(""),
			want: []string{"safe DEPRECATION_REMOVED app compact"},
		},
		{
			name: "planned removal changed", old: sub("      deprecated: x\n      removed_in: 2.0.0\n"), new: sub("      deprecated: x\n      removed_in: 3.0.0\n"),
			want: []string{"safe LIFECYCLE_CHANGED app compact"},
		},
		{
			name: "replacement named", old: sub("      deprecated: x\n") + "    - name: purge\n", new: sub("      deprecated: x\n      replaced_by: purge\n") + "    - name: purge\n",
			want: []string{"safe REPLACED_BY_CHANGED app compact"},
		},
		{
			name: "stability promoted", old: sub("      stability: beta\n"), new: sub(""),
			want: []string{"safe STABILITY_PROMOTED app compact"},
		},
		{
			name: "stability demoted", old: sub(""), new: sub("      stability: experimental\n"),
			want: []string{"possibly_breaking STABILITY_DEMOTED app compact"},
		},
		{
			name: "plugin removed and added",
			old:  "  plugins:\n    - {name: lint, summary: lints}\n",
			new:  "  plugins:\n    - {name: fmt, summary: formats}\n",
			want: []string{"breaking PLUGIN_NO_DELETE app lint", "safe PLUGIN_ADDED app fmt"},
		},
		{
			name: "options_first added", old: sub("      arguments: [{name: a, schema: {type: string}}]\n"), new: sub("      options_first: true\n      arguments: [{name: a, schema: {type: string}}]\n"),
			want: []string{"breaking OPTIONS_FIRST_CHANGED app compact"},
		},
		{
			name: "passthrough added", old: sub(""), new: sub("      passthrough: true\n"),
			want: []string{"breaking COMMAND_PASSTHROUGH_CHANGED app compact"},
		},
		{
			name: "plugin discovery added", old: "", new: "  plugin_discovery: {}\n",
			want: []string{"safe PLUGIN_DISCOVERY_ADDED app plugin_discovery"},
		},
		{
			name: "plugin discovery removed", old: "  plugin_discovery: {}\n", new: "",
			want: []string{"breaking PLUGIN_DISCOVERY_NO_DELETE app plugin_discovery"},
		},
		{
			name: "plugin discovery prefix changed", old: "  plugin_discovery: {}\n", new: "  plugin_discovery: {prefix: app-x-}\n",
			want: []string{"breaking PLUGIN_DISCOVERY_PREFIX_CHANGED app plugin_discovery"},
		},
		{
			name: "plugin discovery hidden", old: "  plugin_discovery: {}\n", new: "  plugin_discovery: {hidden: true}\n",
			want: nil,
		},
	})
}

// A finding on an experimental item is safe, and a breaking one on a beta item is possibly
// breaking. An item is at most as stable as its command and that command's ancestors.
func TestDiff_stabilityAdjustsSeverity(t *testing.T) {
	tree := func(stability, flag string) string {
		return "  commands:\n    - name: remote\n" + stability + "      commands:\n        - name: add\n" + flag
	}
	flag := "          flags:\n            - name: force\n              identifiers: [--force]\n              schema: {type: bool}\n"
	runDiffCases(t, []diffCase{
		{
			name: "experimental ancestor", old: tree("      stability: experimental\n", flag), new: tree("      stability: experimental\n", ""),
			want: []string{"safe FLAG_NO_DELETE app remote add --force"},
		},
		{
			name: "beta ancestor", old: tree("      stability: beta\n", flag), new: tree("      stability: beta\n", ""),
			want: []string{"possibly_breaking FLAG_NO_DELETE app remote add --force"},
		},
		{
			name: "experimental command removed", old: tree("      stability: experimental\n", ""), new: "",
			want: []string{"safe COMMAND_NO_DELETE app remote", "safe COMMAND_NO_DELETE app remote add"},
		},
		{
			name: "experimental flag", old: tree("", flag+"              stability: experimental\n"), new: tree("", ""),
			want: []string{"safe FLAG_NO_DELETE app remote add --force"},
		},
	})
	r := diffSpecs(t, appSpec(tree("      stability: beta\n", flag)), appSpec(tree("      stability: beta\n", "")), contractdiff.Options{})
	if r.Findings[0].Note != "beta" {
		t.Errorf("note = %q, want beta", r.Findings[0].Note)
	}
}

func TestDiff_flagRules(t *testing.T) {
	cmd := func(flags string) string { return "  flags:\n" + flags }
	f := func(lines ...string) string { return "    - " + strings.Join(lines, "\n      ") + "\n" }
	plain := f("name: level", "identifiers: [-l, --level]", "schema: {type: int}")
	runDiffCases(t, []diffCase{
		{
			name: "identifier removed", old: cmd(plain), new: cmd(f("name: level", "identifiers: [--level]", "schema: {type: int}")),
			want: []string{"breaking FLAG_IDENTIFIER_NO_DELETE app -l"},
		},
		{
			name: "deprecated identifier removed as planned", release: "2.0.0",
			old:  cmd(f("name: level", "identifiers: [-l, --level]", "deprecated_identifiers: [-l]", "deprecated_identifiers_removed_in: {-l: 2.0.0}", "schema: {type: int}")),
			new:  cmd(f("name: level", "identifiers: [--level]", "schema: {type: int}")),
			want: []string{"expected FLAG_IDENTIFIER_NO_DELETE app -l", "safe LIFECYCLE_CHANGED app --level"},
		},
		{
			name: "identifier added", old: cmd(f("name: level", "identifiers: [--level]", "schema: {type: int}")), new: cmd(plain),
			want: []string{"safe FLAG_IDENTIFIER_ADDED app -l"},
		},
		{
			name: "hidden identifier removed and added",
			old:  cmd(f("name: level", "identifiers: [--level]", "hidden_identifiers: [--lvl]", "schema: {type: int}")),
			new:  cmd(f("name: level", "identifiers: [--level]", "hidden_identifiers: [--lv]", "schema: {type: int}")),
			want: []string{"breaking FLAG_HIDDEN_IDENTIFIER_NO_DELETE app --lvl", "safe FLAG_HIDDEN_IDENTIFIER_ADDED app --lv"},
		},
		{
			name: "identifier moved to hidden identifiers", old: cmd(plain),
			new:  cmd(f("name: level", "identifiers: [--level]", "hidden_identifiers: [-l]", "schema: {type: int}")),
			want: []string{"safe FLAG_IDENTIFIER_MOVED app -l"},
		},
		{
			name: "negated form lost", old: cmd(f("name: color", "identifiers: [--color]", "schema: {type: bool, negatable: true}")),
			new:  cmd(f("name: color", "identifiers: [--color]", "schema: {type: bool}")),
			want: []string{"breaking FLAG_NEGATED_NO_DELETE app --no-color"},
		},
		{
			name: "negated form gained", old: cmd(f("name: color", "identifiers: [--color]", "schema: {type: bool}")),
			new:  cmd(f("name: color", "identifiers: [--color]", "schema: {type: bool, negatable: true}")),
			want: []string{"safe FLAG_NEGATED_ADDED app --no-color"},
		},
		{
			name: "removed", old: cmd(plain + f("name: x", "identifiers: [--x]", "schema: {type: bool}")), new: cmd(f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			want: []string{"breaking FLAG_NO_DELETE app --level"},
		},
		{
			name: "removed as planned", release: "2.0.0",
			old:  cmd(f("name: level", "identifiers: [--level]", "deprecated: unused", "removed_in: 2.0.0", "schema: {type: int}") + f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			new:  cmd(f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			want: []string{"expected FLAG_NO_DELETE app --level"},
		},
		{
			name: "removed while hidden",
			old:  cmd(f("name: level", "identifiers: [--level]", "hidden: true", "schema: {type: int}") + f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			new:  cmd(f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			want: []string{"possibly_breaking FLAG_NO_DELETE app --level"},
		},
		{
			name: "replaced",
			old:  cmd(f("name: out", "identifiers: [--out]", "deprecated: use --output", "replaced_by: --output", "schema: {type: string}") + f("name: output", "identifiers: [--output]", "schema: {type: string}")),
			new:  cmd(f("name: output", "identifiers: [--output]", "schema: {type: string}")),
			want: []string{"expected FLAG_REPLACED app --out"},
		},
		{
			name: "required added", old: cmd(plain + f("name: x", "identifiers: [--x]", "schema: {type: bool}")),
			new:  cmd(plain + f("name: x", "identifiers: [--x]", "schema: {type: bool}") + f("name: y", "identifiers: [--y]", "schema: {type: string, required: true}")),
			want: []string{"breaking FLAG_REQUIRED_ADDED app --y"},
		},
		{
			name: "required added, short-circuit", old: cmd(plain),
			new:  cmd(plain + f("name: y", "identifiers: [--y]", "short_circuit: true", "schema: {type: string, required: true}")),
			want: []string{"safe FLAG_ADDED app --y"},
		},
		{
			name: "optional added", old: cmd(plain), new: cmd(plain + f("name: y", "identifiers: [--y]", "schema: {type: string}")),
			want: []string{"safe FLAG_ADDED app --y"},
		},
		{
			name: "hidden added", old: cmd(plain), new: cmd(plain + f("name: y", "identifiers: [--y]", "hidden: true", "schema: {type: string}")),
			want: nil,
		},
		{
			name: "now required", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int, required: true}")),
			want: []string{"breaking FLAG_REQUIRED_ADDED app --level"},
		},
		{
			name: "no longer required", old: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int, required: true}")), new: cmd(plain),
			want: []string{"safe FLAG_REQUIRED_REMOVED app --level"},
		},
		{
			name: "logical name changed", old: cmd(plain), new: cmd(f("name: verbosity", "identifiers: [-l, --level]", "schema: {type: int}")),
			want: []string{"possibly_breaking FLAG_NAME_CHANGED app --level"},
		},
		{
			name: "type narrowed", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int8}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app --level"},
		},
		{
			name: "type widened", old: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int8}")), new: cmd(plain),
			want: []string{"safe INPUT_TYPE_WIDENED app --level"},
		},
		{
			name: "list narrowed to one value", old: cmd(f("name: level", "identifiers: [--level]", "schema: {type: \"[]int\"}")),
			new:  cmd(f("name: level", "identifiers: [--level]", "schema: {type: int}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app --level", "breaking INPUT_KIND_CHANGED app --level"},
		},
		{
			name: "existingfile to inputfile", old: cmd(f("name: in", "identifiers: [--in]", "schema: {type: existingfile}")),
			new:  cmd(f("name: in", "identifiers: [--in]", "schema: {type: inputfile}")),
			want: []string{"safe INPUT_TYPE_WIDENED app --in"},
		},
		{
			name: "inputfile to existingfile", old: cmd(f("name: in", "identifiers: [--in]", "schema: {type: inputfile}")),
			new:  cmd(f("name: in", "identifiers: [--in]", "schema: {type: existingfile}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app --in"},
		},
		{
			name: "to outputfile", old: cmd(f("name: out", "identifiers: [--out]", "schema: {type: string}")),
			new:  cmd(f("name: out", "identifiers: [--out]", "schema: {type: outputfile}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app --out"},
		},
		{
			name: "kind changed", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: count}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app --level", "breaking INPUT_KIND_CHANGED app --level", "breaking INPUT_BOUND_ADDED app --level"},
		},
		{
			name: "no longer cascades", old: cmd(f("name: v", "identifiers: [--v]", "cascading: true", "schema: {type: bool}")) + "  commands:\n    - name: sub\n",
			new:  cmd(f("name: v", "identifiers: [--v]", "schema: {type: bool}")) + "  commands:\n    - name: sub\n",
			want: []string{"breaking FLAG_NO_LONGER_CASCADES app --v"},
		},
		{
			name: "now cascades", old: cmd(f("name: v", "identifiers: [--v]", "schema: {type: bool}")) + "  commands:\n    - name: sub\n",
			new:  cmd(f("name: v", "identifiers: [--v]", "cascading: true", "schema: {type: bool}")) + "  commands:\n    - name: sub\n",
			want: []string{"safe FLAG_CASCADES app --v"},
		},
		{
			name: "short_circuit removed", old: cmd(f("name: v", "identifiers: [--v]", "short_circuit: true", "schema: {type: bool}")),
			new:  cmd(f("name: v", "identifiers: [--v]", "schema: {type: bool}")),
			want: []string{"possibly_breaking FLAG_SHORT_CIRCUIT_REMOVED app --v"},
		},
		{
			name: "short_circuit added", old: cmd(f("name: v", "identifiers: [--v]", "schema: {type: bool}")),
			new:  cmd(f("name: v", "identifiers: [--v]", "short_circuit: true", "schema: {type: bool}")),
			want: []string{"safe FLAG_SHORT_CIRCUIT_ADDED app --v"},
		},
		{
			name: "repeat forbidden", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int, repeatable: false}")),
			want: []string{"breaking FLAG_REPEAT_FORBIDDEN app --level"},
		},
		{
			name: "repeat allowed", old: cmd(f("name: level", "identifiers: [-l, --level]", "schema: {type: int, repeatable: false}")), new: cmd(plain),
			want: []string{"safe FLAG_REPEAT_ALLOWED app --level"},
		},
		{
			name: "role added", old: cmd(f("name: force", "identifiers: [--force]", "schema: {type: bool}")),
			new:  cmd(f("name: force", "identifiers: [--force]", "role: force", "schema: {type: bool}")),
			want: []string{"safe FLAG_ROLE_ADDED app --force"},
		},
		{
			name: "hidden", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "hidden: true", "schema: {type: int}")),
			want: []string{"possibly_breaking INPUT_HIDDEN app --level"},
		},
		{
			name: "unhidden", old: cmd(f("name: level", "identifiers: [-l, --level]", "hidden: true", "schema: {type: int}")), new: cmd(plain),
			want: []string{"safe INPUT_UNHIDDEN app --level"},
		},
		{
			name: "secret", old: cmd(f("name: t", "identifiers: [--t]", "schema: {type: string}")),
			new:  cmd(f("name: t", "identifiers: [--t]", "schema: {type: string, secret: true}")),
			want: []string{"safe INPUT_SECRET_CHANGED app --t"},
		},
		{
			name: "description changed", old: cmd(f("name: t", "identifiers: [--t]", "summary: a", "schema: {type: string}")),
			new:  cmd(f("name: t", "identifiers: [--t]", "summary: b", "description: longer", "schema: {type: string}")),
			want: nil,
		},
		{
			name: "replacement named", old: cmd(f("name: t", "identifiers: [--t]", "deprecated: x", "schema: {type: string}") + f("name: u", "identifiers: [--u]", "schema: {type: string}")),
			new:  cmd(f("name: t", "identifiers: [--t]", "deprecated: x", "replaced_by: --u", "schema: {type: string}") + f("name: u", "identifiers: [--u]", "schema: {type: string}")),
			want: []string{"safe REPLACED_BY_CHANGED app --t"},
		},
		{
			name: "stability demoted", old: cmd(plain), new: cmd(f("name: level", "identifiers: [-l, --level]", "stability: beta", "schema: {type: int}")),
			want: []string{"possibly_breaking STABILITY_DEMOTED app --level"},
		},
		{
			name: "digit identifier on a command with arguments",
			old:  cmd(plain) + "  arguments:\n    - {name: n, schema: {type: int}}\n",
			new:  cmd(plain+f("name: four", "identifiers: [\"-4\"]", "schema: {type: bool}")) + "  arguments:\n    - {name: n, schema: {type: int}}\n",
			want: []string{"possibly_breaking DIGIT_FLAG_ADDED app -4", "safe FLAG_ADDED app -4"},
		},
	})
}

func TestDiff_valueRules(t *testing.T) {
	cmd := func(schema string, extra ...string) string {
		lines := append([]string{"name: v", "identifiers: [--v]"}, extra...)
		return "  flags:\n    - " + strings.Join(append(lines, "schema: "+schema), "\n      ") + "\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "separator added", old: cmd("{type: \"[]string\"}"), new: cmd("{type: \"[]string\", separator: \",\"}"),
			want: []string{"breaking INPUT_SEPARATOR_CHANGED app --v"},
		},
		{
			name: "from value removed", old: cmd("{type: string, from: [file, stdin]}"), new: cmd("{type: string, from: [file]}"),
			want: []string{"breaking INPUT_FROM_NO_DELETE app --v"},
		},
		{
			name: "from value added", old: cmd("{type: string}"), new: cmd("{type: string, from: [file]}"),
			want: []string{"possibly_breaking INPUT_FROM_ADDED app --v"},
		},
		{
			name: "secret value dropped", old: cmd("{type: string, secret: true, from: [file, value]}"), new: cmd("{type: string, secret: true, from: [file]}"),
			want: []string{"breaking INPUT_FROM_NO_DELETE app --v"},
		},
		{
			name: "secret value added", old: cmd("{type: string, secret: true, from: [file]}"), new: cmd("{type: string, secret: true, from: [file, value]}"),
			want: []string{"safe INPUT_FROM_ADDED app --v"},
		},
		{
			name: "implicit value added", old: cmd("{type: string, enum: [auto, never]}"), new: cmd("{type: string, enum: [auto, never], implicit_value: auto}"),
			want: []string{"breaking INPUT_IMPLICIT_VALUE_ADDED app --v"},
		},
		{
			name: "implicit value removed", old: cmd("{type: string, enum: [auto, never], implicit_value: auto}"), new: cmd("{type: string, enum: [auto, never]}"),
			want: []string{"breaking INPUT_IMPLICIT_VALUE_REMOVED app --v"},
		},
		{
			name: "implicit value changed", old: cmd("{type: string, enum: [auto, never], implicit_value: auto}"), new: cmd("{type: string, enum: [auto, never], implicit_value: never}"),
			want: []string{"possibly_breaking INPUT_IMPLICIT_VALUE_CHANGED app --v"},
		},
		{
			name: "ignore_case removed", old: cmd("{type: string, enum: [a, b], ignore_case: true}"), new: cmd("{type: string, enum: [a, b]}"),
			want: []string{"breaking INPUT_IGNORE_CASE_REMOVED app --v"},
		},
		{
			name: "ignore_case added", old: cmd("{type: string, enum: [a, b]}"), new: cmd("{type: string, enum: [a, b], ignore_case: true}"),
			want: []string{"safe INPUT_IGNORE_CASE_ADDED app --v"},
		},
		{
			name: "layout removed", old: cmd("{type: time, layout: [\"2006-01-02\", unix]}"), new: cmd("{type: time, layout: [\"2006-01-02\"]}"),
			want: []string{"breaking INPUT_LAYOUT_NO_DELETE app --v"},
		},
		{
			name: "layout added", old: cmd("{type: time, layout: [\"2006-01-02\"]}"), new: cmd("{type: time, layout: [\"2006-01-02\", unix]}"),
			want: []string{"safe INPUT_LAYOUT_ADDED app --v"},
		},
		{
			name: "first layout changed", old: cmd("{type: time, layout: [\"2006-01-02\", unix]}"), new: cmd("{type: time, layout: [unix, \"2006-01-02\"]}"),
			want: []string{"possibly_breaking INPUT_LAYOUT_FIRST_CHANGED app --v"},
		},
		{
			name: "relative added", old: cmd("{type: time}"), new: cmd("{type: time, relative: past}"),
			want: []string{"safe INPUT_RELATIVE_ADDED app --v"},
		},
		{
			name: "relative changed", old: cmd("{type: time, relative: past}"), new: cmd("{type: time, relative: both}"),
			want: []string{"breaking INPUT_RELATIVE_CHANGED app --v"},
		},
		{
			name: "expand added", old: cmd("{type: string}"), new: cmd("{type: string, expand: [home]}"),
			want: []string{"possibly_breaking INPUT_EXPAND_ADDED app --v"},
		},
		{
			name: "expand removed", old: cmd("{type: string, expand: [home, env]}"), new: cmd("{type: string, expand: [home]}"),
			want: []string{"breaking INPUT_EXPAND_REMOVED app --v"},
		},
		{
			name: "enum value removed", old: cmd("{type: string, enum: [a, b]}"), new: cmd("{type: string, enum: [a]}"),
			want: []string{"breaking ENUM_VALUE_NO_DELETE app --v value b"},
		},
		{
			name: "enum value added", old: cmd("{type: string, enum: [a]}"), new: cmd("{type: string, enum: [a, b]}"),
			want: []string{"safe ENUM_VALUE_ADDED app --v value b"},
		},
		{
			name: "enum added", old: cmd("{type: string}"), new: cmd("{type: string, enum: [a]}"),
			want: []string{"breaking INPUT_ENUM_ADDED app --v"},
		},
		{
			name: "enum removed", old: cmd("{type: string, enum: [a]}"), new: cmd("{type: string}"),
			want: []string{"safe INPUT_ENUM_REMOVED app --v"},
		},
		{
			name: "list enum value removed", old: cmd("{type: \"[]string\", enum: [a, b]}"), new: cmd("{type: \"[]string\", enum: [a]}"),
			want: []string{"breaking ENUM_VALUE_NO_DELETE app --v[] value b"},
		},
		{
			name:    "enum value removed as planned",
			old:     cmd("{type: string, enum: [a, {value: b, deprecated: use a, removed_in: 2.0.0}]}"),
			new:     cmd("{type: string, enum: [a]}"),
			release: "2.0.0",
			want:    []string{"expected ENUM_VALUE_NO_DELETE app --v value b"},
		},
		{
			name: "enum value replaced",
			old:  cmd("{type: string, enum: [a, {value: b, deprecated: use a, replaced_by: a}]}"),
			new:  cmd("{type: string, enum: [a]}"),
			want: []string{"expected ENUM_VALUE_REPLACED app --v value b"},
		},
		{
			name: "enum alias removed and added",
			old:  cmd("{type: string, enum: [{value: yaml, aliases: [yml]}]}"),
			new:  cmd("{type: string, enum: [{value: yaml, aliases: [yl]}]}"),
			want: []string{"breaking ENUM_ALIAS_NO_DELETE app --v value yaml alias yml", "safe ENUM_ALIAS_ADDED app --v value yaml alias yl"},
		},
		{
			name: "enum value hidden",
			old:  cmd("{type: string, enum: [a, b]}"),
			new:  cmd("{type: string, enum: [a, {value: b, hidden: true}]}"),
			want: []string{"possibly_breaking ENUM_VALUE_HIDDEN app --v value b"},
		},
		{
			name: "enum value unhidden",
			old:  cmd("{type: string, enum: [a, {value: b, hidden: true}]}"),
			new:  cmd("{type: string, enum: [a, b]}"),
			want: []string{"safe ENUM_VALUE_UNHIDDEN app --v value b"},
		},
		{
			name: "enum value deprecated",
			old:  cmd("{type: string, enum: [a, b]}"),
			new:  cmd("{type: string, enum: [a, {value: b, deprecated: use a}]}"),
			want: []string{"safe DEPRECATION_ADDED app --v value b"},
		},
		{
			name: "enum summary changed",
			old:  cmd("{type: string, enum: [{value: a, summary: one}]}"),
			new:  cmd("{type: string, enum: [{value: a, summary: two}]}"),
			want: nil,
		},
		{
			name: "bound added", old: cmd("{type: int}"), new: cmd("{type: int, minimum: 1}"),
			want: []string{"breaking INPUT_BOUND_ADDED app --v"},
		},
		{
			name: "bound narrowed", old: cmd("{type: int, maximum: 10}"), new: cmd("{type: int, maximum: 5}"),
			want: []string{"breaking INPUT_BOUND_NARROWED app --v"},
		},
		{
			name: "bound widened", old: cmd("{type: int, maximum: 5}"), new: cmd("{type: int, maximum: 10}"),
			want: []string{"safe INPUT_BOUND_WIDENED app --v"},
		},
		{
			name: "bound removed", old: cmd("{type: string, maxLength: 5}"), new: cmd("{type: string}"),
			want: []string{"safe INPUT_BOUND_REMOVED app --v"},
		},
		{
			name: "uniqueItems added", old: cmd("{type: \"[]string\"}"), new: cmd("{type: \"[]string\", uniqueItems: true}"),
			want: []string{"breaking INPUT_BOUND_ADDED app --v"},
		},
		{
			name: "pattern added", old: cmd("{type: string}"), new: cmd("{type: string, pattern: \"^a\"}"),
			want: []string{"breaking INPUT_PATTERN_ADDED app --v"},
		},
		{
			name: "pattern changed", old: cmd("{type: string, pattern: \"^a\"}"), new: cmd("{type: string, pattern: \"^b\"}"),
			want: []string{"possibly_breaking INPUT_PATTERN_CHANGED app --v"},
		},
		{
			name: "pattern removed", old: cmd("{type: string, pattern: \"^a\"}"), new: cmd("{type: string}"),
			want: []string{"safe INPUT_PATTERN_REMOVED app --v"},
		},
		{
			name: "default changed", old: cmd("{type: int, default: 1}"), new: cmd("{type: int, default: 2}"),
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app --v"},
		},
		{
			name: "default removed", old: cmd("{type: int, default: 1}"), new: cmd("{type: int}"),
			want: []string{"possibly_breaking INPUT_DEFAULT_REMOVED app --v"},
		},
		{
			name: "default added", old: cmd("{type: int}"), new: cmd("{type: int, default: 1}"),
			want: []string{"safe INPUT_DEFAULT_ADDED app --v"},
		},
		{
			name: "env fallback removed and added", old: cmd("{type: string, variable: APP_V}"), new: cmd("{type: string, variable: APP_W}"),
			want: []string{"breaking INPUT_ENV_NO_DELETE app --v $APP_V", "safe INPUT_ENV_ADDED app --v $APP_W"},
		},
	})
}

func TestDiff_argumentRules(t *testing.T) {
	args := func(list ...string) string {
		return "  arguments:\n" + "    - " + strings.Join(list, "\n    - ") + "\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "removed", old: args("{name: a, schema: {type: string}}", "{name: b, schema: {type: string}}"), new: args("{name: a, schema: {type: string}}"),
			want: []string{"breaking ARGUMENT_NO_DELETE app <b>"},
		},
		{
			name: "removed as planned", release: "2.0.0",
			old: args("{name: a, schema: {type: string}}", "{name: b, deprecated: x, removed_in: 2.0.0, schema: {type: string}}"), new: args("{name: a, schema: {type: string}}"),
			want: []string{"expected ARGUMENT_NO_DELETE app <b>"},
		},
		{
			name: "required added", old: args("{name: a, schema: {type: string}}"), new: args("{name: a, schema: {type: string}}", "{name: b, schema: {type: string, required: true}}"),
			want: []string{"breaking ARGUMENT_REQUIRED_ADDED app <b>"},
		},
		{
			name: "optional added at the end", old: args("{name: a, schema: {type: string}}"), new: args("{name: a, schema: {type: string}}", "{name: b, schema: {type: string}}"),
			want: []string{"safe ARGUMENT_ADDED app <b>"},
		},
		{
			name: "now required", old: args("{name: a, schema: {type: string}}"), new: args("{name: a, schema: {type: string, required: true}}"),
			want: []string{"breaking ARGUMENT_REQUIRED_ADDED app <a>"},
		},
		{
			name: "renamed", old: args("{name: a, schema: {type: string}}"), new: args("{name: b, schema: {type: string}}"),
			want: []string{"possibly_breaking ARGUMENT_NAME_CHANGED app <a>"},
		},
		{
			name: "no longer variadic", old: args("{name: a, schema: {type: \"[]string\"}}"), new: args("{name: a, schema: {type: string}}"),
			want: []string{"breaking ARGUMENT_VARIADIC_REMOVED app <a>", "breaking INPUT_TYPE_CHANGED app <a>", "breaking INPUT_KIND_CHANGED app <a>"},
		},
		{
			name: "became variadic", old: args("{name: a, schema: {type: string}}"), new: args("{name: a, schema: {type: \"[]string\"}}"),
			want: []string{"possibly_breaking ARGUMENT_VARIADIC_ADDED app <a>", "safe INPUT_TYPE_WIDENED app <a>", "breaking INPUT_KIND_CHANGED app <a>"},
		},
		{
			name: "passthrough added", old: args("{name: a, schema: {type: \"[]string\"}}"), new: args("{name: a, passthrough: true, schema: {type: \"[]string\"}}"),
			want: []string{"breaking ARGUMENT_PASSTHROUGH_CHANGED app <a>"},
		},
		{
			name: "glob added", old: args("{name: a, schema: {type: \"[]string\"}}"), new: args("{name: a, schema: {type: \"[]string\", glob: true}}"),
			want: []string{"possibly_breaking ARGUMENT_GLOB_CHANGED app <a>"},
		},
		{
			name: "env fallback added", old: args("{name: a, schema: {type: string}}"), new: args("{name: a, schema: {type: string, variable: APP_A}}"),
			want: []string{"safe INPUT_ENV_ADDED app <a> $APP_A"},
		},
	})
}

func TestDiff_envAndConfigRules(t *testing.T) {
	env := func(list ...string) string { return "  env:\n    - " + strings.Join(list, "\n    - ") + "\n" }
	files := "  config_files:\n    - {name: main, path: ~/.app.yaml}\n    - {name: other, path: ~/.other.yaml}\n"
	cfg := func(list ...string) string {
		return files + "  config:\n    - " + strings.Join(list, "\n    - ") + "\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "env removed", old: env("{name: a, schema: {type: string, variable: APP_A}}", "{name: b, schema: {type: string, variable: APP_B}}"),
			new:  env("{name: b, schema: {type: string, variable: APP_B}}"),
			want: []string{"breaking ENV_NO_DELETE app $APP_A"},
		},
		{
			name: "env renamed", old: env("{name: a, schema: {type: string, variable: APP_A}}"), new: env("{name: a, schema: {type: string, variable: APP_Z}}"),
			want: []string{"breaking ENV_NO_DELETE app $APP_A", "safe ENV_ADDED app $APP_Z"},
		},
		{
			name: "env required added", old: env("{name: a, schema: {type: string, variable: APP_A}}"),
			new:  env("{name: a, schema: {type: string, variable: APP_A}}", "{name: b, schema: {type: string, variable: APP_B, required: true}}"),
			want: []string{"breaking ENV_REQUIRED_ADDED app $APP_B"},
		},
		{
			name: "env now required", old: env("{name: a, schema: {type: string, variable: APP_A}}"), new: env("{name: a, schema: {type: string, variable: APP_A, required: true}}"),
			want: []string{"breaking ENV_REQUIRED_ADDED app $APP_A"},
		},
		{
			name: "env nesting changed", old: env("{name: a, schema: {type: map, variable: APP_A, nesting: \"__\"}}"), new: env("{name: a, schema: {type: map, variable: APP_A, nesting: \"_\"}}"),
			want: []string{"breaking ENV_NESTING_CHANGED app $APP_A"},
		},
		{
			name: "env separator made explicit", old: env("{name: a, schema: {type: \"[]string\", variable: APP_A}}"), new: env("{name: a, schema: {type: \"[]string\", variable: APP_A, separator: \",\"}}"),
			want: nil,
		},
		{
			name: "env separator changed", old: env("{name: a, schema: {type: \"[]string\", variable: APP_A}}"), new: env("{name: a, schema: {type: \"[]string\", variable: APP_A, separator: \";\"}}"),
			want: []string{"breaking INPUT_SEPARATOR_CHANGED app $APP_A"},
		},
		{
			name: "variable file added", old: env("{name: a, schema: {type: string, variable: APP_A}}"), new: env("{name: a, schema: {type: string, variable: APP_A, variable_file: APP_A_FILE}}"),
			want: []string{"safe INPUT_VARIABLE_FILE_ADDED app $APP_A"},
		},
		{
			name: "variable file removed", old: env("{name: a, schema: {type: string, variable: APP_A, variable_file: APP_A_FILE}}"), new: env("{name: a, schema: {type: string, variable: APP_A}}"),
			want: []string{"breaking INPUT_VARIABLE_FILE_NO_DELETE app $APP_A"},
		},
		{
			name: "config key removed", old: cfg("{name: a, schema: {type: string, key: a.b, file: main}}", "{name: c, schema: {type: string, key: c, file: main}}"),
			new:  cfg("{name: c, schema: {type: string, key: c, file: main}}"),
			want: []string{"breaking CONFIG_NO_DELETE app config a.b"},
		},
		{
			name: "config key moved to another file", old: cfg("{name: a, schema: {type: string, key: a.b, file: main}}"), new: cfg("{name: a, schema: {type: string, key: a.b, file: other}}"),
			want: []string{"breaking CONFIG_MOVED app config a.b"},
		},
		{
			name: "config key required added", old: cfg("{name: a, schema: {type: string, key: a.b, file: main}}"),
			new:  cfg("{name: a, schema: {type: string, key: a.b, file: main}}", "{name: c, schema: {type: string, key: c, file: main, required: true}}"),
			want: []string{"breaking CONFIG_REQUIRED_ADDED app config c"},
		},
		{
			name: "config key added", old: cfg("{name: a, schema: {type: string, key: a.b, file: main}}"),
			new:  cfg("{name: a, schema: {type: string, key: a.b, file: main}}", "{name: c, schema: {type: string, key: c, file: main}}"),
			want: []string{"safe CONFIG_ADDED app config c"},
		},
	})
}

func TestDiff_configFileRules(t *testing.T) {
	files := func(list ...string) string { return "  config_files:\n    - " + strings.Join(list, "\n    - ") + "\n" }
	runDiffCases(t, []diffCase{
		{
			name: "removed", old: files("{name: main, path: ~/.app.yaml}", "{name: x, path: ~/.x.yaml}"), new: files("{name: x, path: ~/.x.yaml}"),
			want: []string{"breaking CONFIG_FILE_NO_DELETE app config_files main"},
		},
		{
			name: "moved", old: files("{name: main, path: ~/.app.yaml}"), new: files("{name: main, path: ~/.config/app.yaml}"),
			want: []string{"breaking CONFIG_FILE_MOVED app config_files main"},
		},
		{
			name: "discovery changed", old: files("{name: main, discover: {strategy: walk-up, file: .app.yaml}}"), new: files("{name: main, discover: {strategy: xdg, app: app, file: config.yaml}}"),
			want: []string{"breaking CONFIG_FILE_MOVED app config_files main"},
		},
		{
			name: "format changed", old: files("{name: main, path: ~/.app}"), new: files("{name: main, path: ~/.app, format: toml}"),
			want: []string{"breaking CONFIG_FILE_MOVED app config_files main"},
		},
		{
			name: "read as variables", old: files("{name: main, path: .env, format: dotenv}"), new: files("{name: main, path: .env, format: dotenv, as: env}"),
			want: []string{"breaking CONFIG_FILE_AS_CHANGED app config_files main"},
		},
		{
			name: "added", old: "", new: files("{name: main, path: ~/.app.yaml}"),
			want: []string{"safe CONFIG_FILE_ADDED app config_files main"},
		},
	})
}

func TestDiff_flagRulesAndDependencies(t *testing.T) {
	flags := "  flags:\n" +
		"    - {name: a, identifiers: [--a], schema: {type: bool}}\n" +
		"    - {name: b, identifiers: [--b], schema: {type: bool}}\n" +
		"    - {name: c, identifiers: [--c], schema: {type: bool}}\n" +
		"    - {name: mode, identifiers: [--mode], schema: {type: string}}\n"
	groups := func(list ...string) string {
		return flags + "  flag_groups:\n    - " + strings.Join(list, "\n    - ") + "\n"
	}
	deps := func(list ...string) string {
		return flags + "  flag_dependencies:\n    - " + strings.Join(list, "\n    - ") + "\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "group added", old: flags, new: groups("{kind: mutually_exclusive, flags: [a, b]}"),
			want: []string{"breaking FLAG_GROUP_ADDED app group mutually_exclusive(a,b)"},
		},
		{
			name: "group removed", old: groups("{kind: mutually_exclusive, flags: [a, b]}"), new: flags,
			want: []string{"safe FLAG_GROUP_REMOVED app group mutually_exclusive(a,b)"},
		},
		{
			name: "flag added to a group", old: groups("{kind: mutually_exclusive, flags: [a, b]}"), new: groups("{kind: mutually_exclusive, flags: [a, b, c]}"),
			want: []string{"breaking FLAG_GROUP_TIGHTENED app group mutually_exclusive(a,b)"},
		},
		{
			name: "flag removed from a group", old: groups("{kind: required_together, flags: [a, b, c]}"), new: groups("{kind: required_together, flags: [a, b]}"),
			want: []string{"safe FLAG_GROUP_LOOSENED app group required_together(a,b,c)"},
		},
		{
			name: "at_least_one gains a flag", old: groups("{kind: at_least_one, flags: [a, b]}"), new: groups("{kind: at_least_one, flags: [a, b, c]}"),
			want: []string{"safe FLAG_GROUP_LOOSENED app group at_least_one(a,b)"},
		},
		{
			name: "kind tightened", old: groups("{kind: mutually_exclusive, flags: [a, b]}"), new: groups("{kind: one_of, flags: [a, b]}"),
			want: []string{"breaking FLAG_GROUP_TIGHTENED app group mutually_exclusive(a,b)"},
		},
		{
			name: "kind loosened", old: groups("{kind: one_of, flags: [a, b]}"), new: groups("{kind: at_least_one, flags: [a, b]}"),
			want: []string{"safe FLAG_GROUP_LOOSENED app group one_of(a,b)"},
		},
		{
			name: "dependency added", old: flags, new: deps("{when: a, requires: [b]}"),
			want: []string{"breaking FLAG_DEPENDENCY_ADDED app dependency a"},
		},
		{
			name: "dependency removed", old: deps("{when: a, requires: [b]}"), new: flags,
			want: []string{"safe FLAG_DEPENDENCY_REMOVED app dependency a"},
		},
		{
			name: "requires grown", old: deps("{when: a, requires: [b]}"), new: deps("{when: a, requires: [b, c]}"),
			want: []string{"breaking FLAG_DEPENDENCY_TIGHTENED app dependency a"},
		},
		{
			name: "forbids shrunk", old: deps("{when: a, forbids: [b, c]}"), new: deps("{when: a, forbids: [b]}"),
			want: []string{"safe FLAG_DEPENDENCY_LOOSENED app dependency a"},
		},
		{
			name: "equals widened", old: deps("{when: mode, equals: [x], requires: [a]}"), new: deps("{when: mode, equals: [x, y], requires: [a]}"),
			want: []string{"breaking FLAG_DEPENDENCY_TIGHTENED app dependency mode"},
		},
		{
			name: "equals narrowed", old: deps("{when: mode, requires: [a]}"), new: deps("{when: mode, equals: [x], requires: [a]}"),
			want: []string{"safe FLAG_DEPENDENCY_LOOSENED app dependency mode"},
		},
		{
			name: "unless shrunk", old: deps("{when: a, unless: [c], requires: [b]}"), new: deps("{when: a, requires: [b]}"),
			want: []string{"safe FLAG_DEPENDENCY_REMOVED app dependency a unless c", "breaking FLAG_DEPENDENCY_ADDED app dependency a"},
		},
	})
}

func TestDiff_stdinOutputExitRules(t *testing.T) {
	sub := func(extra string) string { return "  commands:\n    - name: run\n" + extra }
	schemas := "  schemas:\n    Task:\n      type: object\n      required: [id]\n      properties:\n        id: {type: integer}\n        status: {type: string, enum: [open, done]}\n"
	out := func(body string) string { return sub("      output:\n" + body) }
	runDiffCases(t, []diffCase{
		{
			name: "stdin added", old: sub(""), new: sub("      stdin: {format: text}\n"),
			want: []string{"possibly_breaking STDIN_ADDED app run stdin"},
		},
		{
			name: "stdin removed", old: sub("      stdin: {format: text}\n"), new: sub(""),
			want: []string{"breaking STDIN_NO_DELETE app run stdin"},
		},
		{
			name: "stdin format changed", old: sub("      stdin: {format: lines}\n"), new: sub("      stdin: {format: jsonl}\n"),
			want: []string{"breaking STDIN_FORMAT_CHANGED app run stdin"},
		},
		{
			name: "stdin required", old: sub("      stdin: {format: text, schema: {type: string}}\n"), new: sub("      stdin: {format: text, schema: {type: string, required: true}}\n"),
			want: []string{"breaking STDIN_REQUIRED_ADDED app run stdin"},
		},
		{
			name: "stdin no longer required", old: sub("      stdin: {format: text, schema: {type: string, required: true}}\n"), new: sub("      stdin: {format: text, schema: {type: string}}\n"),
			want: []string{"safe STDIN_REQUIRED_REMOVED app run stdin"},
		},
		{
			name: "stdin separator", old: sub("      stdin: {format: lines}\n"), new: sub("      stdin: {format: lines, separator: nul}\n"),
			want: []string{"breaking STDIN_SEPARATOR_CHANGED app run stdin"},
		},
		{
			name: "stdin property now required",
			old:  "  schemas:\n    In: {type: object, properties: {a: {type: string}}}\n" + sub("      stdin: {format: json, schema: {$ref: \"#/schemas/In\"}}\n"),
			new:  "  schemas:\n    In: {type: object, required: [a], properties: {a: {type: string}}}\n" + sub("      stdin: {format: json, schema: {$ref: \"#/schemas/In\"}}\n"),
			want: []string{"breaking INPUT_PROPERTY_REQUIRED_ADDED definitions.In.a"},
		},
		{
			name: "output removed", old: schemas + out("        $ref: \"#/schemas/Task\"\n"), new: schemas + sub(""),
			want: []string{"breaking OUTPUT_NO_DELETE app run output"},
		},
		{
			name: "output added", old: schemas + sub(""), new: schemas + out("        $ref: \"#/schemas/Task\"\n"),
			want: []string{"safe OUTPUT_ADDED app run output"},
		},
		{
			name: "output property removed and added",
			old:  out("        type: object\n        properties: {a: {type: string}}\n"),
			new:  out("        type: object\n        properties: {b: {type: string}}\n"),
			want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE app run output.a", "safe OUTPUT_PROPERTY_ADDED app run output.b"},
		},
		{
			name: "output property no longer required",
			old:  out("        type: object\n        required: [a]\n        properties: {a: {type: string}}\n"),
			new:  out("        type: object\n        properties: {a: {type: string}}\n"),
			want: []string{"breaking OUTPUT_PROPERTY_REQUIRED_REMOVED app run output.a"},
		},
		{
			name: "output type changed",
			old:  out("        type: object\n        properties: {a: {type: string}}\n"),
			new:  out("        type: object\n        properties: {a: {type: integer}}\n"),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app run output.a"},
		},
		{
			name: "output enum value added and removed",
			old:  out("        type: object\n        properties: {s: {type: string, enum: [a, b]}}\n"),
			new:  out("        type: object\n        properties: {s: {type: string, enum: [a, c]}}\n"),
			want: []string{"possibly_breaking OUTPUT_ENUM_VALUE_ADDED app run output.s value c", "safe OUTPUT_ENUM_VALUE_REMOVED app run output.s value b"},
		},
		{
			name: "shared definition changed once",
			old:  schemas + sub("      output: {$ref: \"#/schemas/Task\"}\n    - name: list\n      output: {$ref: \"#/schemas/Task\"}\n"),
			new: strings.Replace(schemas, "enum: [open, done]", "enum: [open, done, blocked]", 1) +
				sub("      output: {$ref: \"#/schemas/Task\"}\n    - name: list\n      output: {$ref: \"#/schemas/Task\"}\n"),
			want: []string{"possibly_breaking OUTPUT_ENUM_VALUE_ADDED definitions.Task.status value blocked"},
		},
		{
			name: "stream changed", old: out("        type: object\n"), new: sub("      output: {type: object}\n      output_stream: true\n"),
			want: []string{"breaking OUTPUT_STREAM_CHANGED app run output"},
		},
		{
			name: "exit status removed and added",
			old:  sub("      exit_status:\n        - {code: 3, summary: not found}\n"),
			new:  sub("      exit_status:\n        - {code: 4, summary: not found}\n"),
			want: []string{"possibly_breaking EXIT_STATUS_NO_DELETE app run exit 3", "safe EXIT_STATUS_ADDED app run exit 4"},
		},
		{
			name: "exit summary changed",
			old:  sub("      exit_status:\n        - {code: 3, summary: not found}\n"),
			new:  sub("      exit_status:\n        - {code: 3, summary: missing}\n"),
			want: []string{"possibly_breaking EXIT_SUMMARY_CHANGED app run exit 3"},
		},
		{
			name: "exit name added",
			old:  sub("      exit_status:\n        - {code: 3, summary: x}\n"),
			new:  sub("      exit_status:\n        - {code: 3, name: not_found, summary: x}\n"),
			want: []string{"safe EXIT_NAME_ADDED app run exit 3"},
		},
		{
			name: "exit name changed",
			old:  sub("      exit_status:\n        - {code: 3, name: not_found, summary: x}\n"),
			new:  sub("      exit_status:\n        - {code: 3, name: missing, summary: x}\n"),
			want: []string{"possibly_breaking EXIT_NAME_CHANGED app run exit 3"},
		},
		{
			name: "exit no longer retryable",
			old:  sub("      exit_status:\n        - {code: 3, summary: x, retryable: true}\n"),
			new:  sub("      exit_status:\n        - {code: 3, summary: x}\n"),
			want: []string{"possibly_breaking EXIT_RETRYABLE_REMOVED app run exit 3"},
		},
		{
			name: "exit now retryable",
			old:  sub("      exit_status:\n        - {code: 3, summary: x}\n"),
			new:  sub("      exit_status:\n        - {code: 3, summary: x, retryable: true}\n"),
			want: []string{"safe EXIT_RETRYABLE_ADDED app run exit 3"},
		},
		{
			name: "exit docs link added",
			old:  sub("      exit_status:\n        - {code: 3, summary: x}\n"),
			new:  sub("      exit_status:\n        - {code: 3, summary: x, docs_url: \"https://example.com/3\"}\n"),
			want: nil,
		},
		{
			name: "exit output property removed",
			old:  sub("      exit_status:\n        - code: 3\n          summary: x\n          output: {type: object, properties: {why: {type: string}}}\n"),
			new:  sub("      exit_status:\n        - code: 3\n          summary: x\n          output: {type: object}\n"),
			want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE app run exit 3 output.why"},
		},
	})
}

// A spec diffed against itself gives no findings, for every golden spec and the companion's own.
func TestDiff_selfDiffIsEmpty(t *testing.T) {
	specs := []string{
		goldenSpec, factsSpec,
		readTestFile(t, filepath.Join("testdata", "pages", "spec.yaml")),
		readTestFile(t, filepath.Join("..", "..", "cmd", "rotini", ".rotini.spec.yaml")),
	}
	shapes, _ := filepath.Glob(filepath.Join("testdata", "stubshapes", "*", "spec.yaml"))
	for _, path := range shapes {
		specs = append(specs, readTestFile(t, path))
	}
	for i, spec := range specs {
		raw, _ := contractJSON(t, spec)
		r, err := contractdiff.Diff(raw, raw, contractdiff.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Findings) > 0 {
			t.Errorf("spec %d: a self-diff found %v", i, findingLines(r))
		}
	}
}
