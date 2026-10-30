package codegen

import (
	"strings"
	"testing"
)

// The cases below pin, through a spec's generated contract, the ratings the rules table gives
// for each variant of a change: hidden items, items under a command with arguments, planned
// removals, and output declared on an exit status.

func TestDiffMatrix_hiddenAndArgumentCommands(t *testing.T) {
	withArg := "  arguments:\n    - {name: target, schema: {type: string}}\n"
	sub := func(extra string) string { return "  commands:\n    - name: run\n" + extra }
	runDiffCases(t, []diffCase{
		{
			name: "hidden command added under a command with arguments", old: withArg, new: withArg + sub("      hidden: true\n"),
			want: []string{"possibly_breaking COMMAND_ADDED app run"},
		},
		{
			name: "plugin added under a command with arguments", old: withArg,
			new:  withArg + "  plugins:\n    - {name: lint, summary: lints}\n",
			want: []string{"possibly_breaking PLUGIN_ADDED app lint"},
		},
		{
			name: "hidden alias added under a command with arguments",
			old:  withArg + sub(""), new: withArg + sub("      hidden_aliases: [r]\n"),
			want: []string{"possibly_breaking HIDDEN_ALIAS_ADDED app run alias r"},
		},
		{
			name: "plugin discovery added on a command with arguments", old: withArg, new: withArg + "  plugin_discovery: {}\n",
			want: []string{"breaking PLUGIN_DISCOVERY_ADDED app plugin_discovery"},
		},
		{
			name: "hidden alias removed as planned", release: "2.0.0",
			old:  sub("      hidden_aliases: [r]\n      deprecated_identifiers: [r]\n      deprecated_identifiers_removed_in: {r: 2.0.0}\n"),
			new:  sub(""),
			want: []string{"expected HIDDEN_ALIAS_NO_DELETE app run alias r", "safe LIFECYCLE_CHANGED app run"},
		},
		{
			name: "hidden command replaced before its planned release", release: "2.0.0",
			old: "  commands:\n    - name: compact\n      hidden: true\n      deprecated: use purge\n      replaced_by: purge\n      removed_in: 3.0.0\n" +
				"    - name: purge\n",
			new:  "  commands:\n    - name: purge\n",
			want: []string{"possibly_breaking COMMAND_REPLACED app compact"},
		},
	})
}

func TestDiffMatrix_hiddenRequiredInputs(t *testing.T) {
	files := "  config_files:\n    - {name: main, path: ~/.app.yaml}\n"
	runDiffCases(t, []diffCase{
		{
			name: "hidden required flag added", old: "",
			new:  "  flags:\n    - {name: t, identifiers: [--t], hidden: true, schema: {type: string, required: true}}\n",
			want: []string{"breaking FLAG_REQUIRED_ADDED app --t"},
		},
		{
			name: "hidden optional argument added", old: "",
			new:  "  arguments:\n    - {name: a, hidden: true, schema: {type: string}}\n",
			want: nil,
		},
		{
			name: "hidden required argument added", old: "",
			new:  "  arguments:\n    - {name: a, hidden: true, schema: {type: string, required: true}}\n",
			want: []string{"breaking ARGUMENT_REQUIRED_ADDED app <a>"},
		},
		{
			name: "hidden required env added", old: "",
			new:  "  env:\n    - {name: a, hidden: true, schema: {type: string, variable: APP_A, required: true}}\n",
			want: []string{"breaking ENV_REQUIRED_ADDED app $APP_A"},
		},
		{
			name: "hidden optional env added", old: "",
			new:  "  env:\n    - {name: a, hidden: true, schema: {type: string, variable: APP_A}}\n",
			want: nil,
		},
		{
			name: "hidden config key removed", old: files + "  config:\n    - {name: a, hidden: true, schema: {type: string, key: a, file: main}}\n",
			new:  files,
			want: []string{"possibly_breaking CONFIG_NO_DELETE app config a"},
		},
		{
			name: "hidden env removed", old: "  env:\n    - {name: a, hidden: true, schema: {type: string, variable: APP_A}}\n", new: "",
			want: []string{"possibly_breaking ENV_NO_DELETE app $APP_A"},
		},
		{
			name: "hidden argument removed", old: "  arguments:\n    - {name: a, hidden: true, schema: {type: string}}\n", new: "",
			want: []string{"possibly_breaking ARGUMENT_NO_DELETE app <a>"},
		},
	})
}

func TestDiffMatrix_enumValueVariants(t *testing.T) {
	cmd := func(schema string) string {
		return "  flags:\n    - {name: v, identifiers: [--v], schema: " + schema + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "hidden enum value removed",
			old:  cmd("{type: string, enum: [a, {value: b, hidden: true}]}"), new: cmd("{type: string, enum: [a]}"),
			want: []string{"possibly_breaking ENUM_VALUE_NO_DELETE app --v value b"},
		},
		{
			name: "hidden enum value replaced before its planned release", release: "2.0.0",
			old:  cmd("{type: string, enum: [a, {value: b, hidden: true, deprecated: use a, replaced_by: a, removed_in: 3.0.0}]}"),
			new:  cmd("{type: string, enum: [a]}"),
			want: []string{"possibly_breaking ENUM_VALUE_REPLACED app --v value b"},
		},
		{
			name: "enum value replaced before its planned release", release: "2.0.0",
			old:  cmd("{type: string, enum: [a, {value: b, deprecated: use a, replaced_by: a, removed_in: 3.0.0}]}"),
			new:  cmd("{type: string, enum: [a]}"),
			want: []string{"breaking ENUM_VALUE_REPLACED app --v value b"},
		},
		{
			name: "enum value renamed",
			old:  cmd("{type: string, enum: [json, yml]}"), new: cmd("{type: string, enum: [json, yaml]}"),
			want: []string{"breaking ENUM_VALUE_NO_DELETE app --v value yml", "safe ENUM_VALUE_ADDED app --v value yaml"},
		},
		{
			name: "enum narrowed on an env input",
			old:  "  env:\n    - {name: m, schema: {type: string, variable: APP_M, enum: [a, b]}}\n",
			new:  "  env:\n    - {name: m, schema: {type: string, variable: APP_M, enum: [a]}}\n",
			want: []string{"breaking ENUM_VALUE_NO_DELETE app $APP_M value b"},
		},
		{
			name: "enum narrowed on an argument",
			old:  "  arguments:\n    - {name: m, schema: {type: string, enum: [a, b]}}\n",
			new:  "  arguments:\n    - {name: m, schema: {type: string, enum: [a]}}\n",
			want: []string{"breaking ENUM_VALUE_NO_DELETE app <m> value b"},
		},
	})
}

func TestDiffMatrix_boundVariants(t *testing.T) {
	cmd := func(schema string) string {
		return "  flags:\n    - {name: v, identifiers: [--v], schema: " + schema + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "minimum widened", old: cmd("{type: int, minimum: 5}"), new: cmd("{type: int, minimum: 1}"),
			want: []string{"safe INPUT_BOUND_WIDENED app --v"},
		},
		{
			name: "minimum narrowed", old: cmd("{type: int, minimum: 1}"), new: cmd("{type: int, minimum: 5}"),
			want: []string{"breaking INPUT_BOUND_NARROWED app --v"},
		},
		{
			name: "both bounds widened", old: cmd("{type: int, minimum: 1, maximum: 5}"), new: cmd("{type: int, minimum: 0, maximum: 10}"),
			want: []string{"safe INPUT_BOUND_WIDENED app --v"},
		},
		{
			name: "one bound widened, the other narrowed", old: cmd("{type: int, minimum: 1, maximum: 5}"), new: cmd("{type: int, minimum: 0, maximum: 4}"),
			want: []string{"safe INPUT_BOUND_WIDENED app --v", "breaking INPUT_BOUND_NARROWED app --v"},
		},
		{
			name: "exclusive minimum added", old: cmd("{type: float64}"), new: cmd("{type: float64, exclusiveMinimum: 0}"),
			want: []string{"breaking INPUT_BOUND_ADDED app --v"},
		},
		{
			name: "multipleOf narrowed", old: cmd("{type: int, multipleOf: 5}"), new: cmd("{type: int, multipleOf: 10}"),
			want: []string{"breaking INPUT_BOUND_NARROWED app --v"},
		},
		{
			name: "multipleOf widened", old: cmd("{type: int, multipleOf: 10}"), new: cmd("{type: int, multipleOf: 5}"),
			want: []string{"safe INPUT_BOUND_WIDENED app --v"},
		},
		{
			name: "length narrowed", old: cmd("{type: string, maxLength: 10}"), new: cmd("{type: string, maxLength: 5}"),
			want: []string{"breaking INPUT_BOUND_NARROWED app --v"},
		},
		{
			name: "item count added", old: cmd("{type: \"[]string\"}"), new: cmd("{type: \"[]string\", minItems: 1}"),
			want: []string{"breaking INPUT_BOUND_ADDED app --v"},
		},
		{
			name: "uniqueItems removed", old: cmd("{type: \"[]string\", uniqueItems: true}"), new: cmd("{type: \"[]string\"}"),
			want: []string{"safe INPUT_BOUND_REMOVED app --v"},
		},
	})
}

func TestDiffMatrix_defaultVariants(t *testing.T) {
	files := "  config_files:\n    - {name: main, path: ~/.app.yaml}\n"
	runDiffCases(t, []diffCase{
		{
			name: "env default changed",
			old:  "  env:\n    - {name: a, schema: {type: int, variable: APP_A, default: 1}}\n",
			new:  "  env:\n    - {name: a, schema: {type: int, variable: APP_A, default: 2}}\n",
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app $APP_A"},
		},
		{
			name: "config default changed",
			old:  files + "  config:\n    - {name: a, schema: {type: string, key: a, file: main, default: x}}\n",
			new:  files + "  config:\n    - {name: a, schema: {type: string, key: a, file: main, default: y}}\n",
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app config a"},
		},
		{
			name: "argument default changed",
			old:  "  arguments:\n    - {name: a, schema: {type: string, default: x}}\n",
			new:  "  arguments:\n    - {name: a, schema: {type: string, default: y}}\n",
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app <a>"},
		},
		{
			name: "list default changed",
			old:  "  flags:\n    - {name: v, identifiers: [--v], schema: {type: \"[]string\", default: [a, b]}}\n",
			new:  "  flags:\n    - {name: v, identifiers: [--v], schema: {type: \"[]string\", default: [a]}}\n",
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app --v"},
		},
		{
			name: "bool default changed",
			old:  "  flags:\n    - {name: v, identifiers: [--v], schema: {type: bool, default: false}}\n",
			new:  "  flags:\n    - {name: v, identifiers: [--v], schema: {type: bool, default: true}}\n",
			want: []string{"possibly_breaking INPUT_DEFAULT_CHANGED app --v"},
		},
	})
}

func TestDiffMatrix_renames(t *testing.T) {
	f := func(lines ...string) string { return "  flags:\n    - " + strings.Join(lines, "\n      ") + "\n" }
	runDiffCases(t, []diffCase{
		{
			name: "flag renamed outright", old: f("name: out", "identifiers: [--out]", "schema: {type: string}"),
			new:  f("name: output", "identifiers: [--output]", "schema: {type: string}"),
			want: []string{"breaking FLAG_NO_DELETE app --out", "safe FLAG_ADDED app --output"},
		},
		{
			name: "flag renamed, old spelling kept hidden", old: f("name: out", "identifiers: [--out]", "schema: {type: string}"),
			new:  f("name: out", "identifiers: [--output]", "hidden_identifiers: [--out]", "schema: {type: string}"),
			want: []string{"safe FLAG_IDENTIFIER_MOVED app --out", "safe FLAG_IDENTIFIER_ADDED app --output"},
		},
		{
			name: "flag env name changed", old: f("name: t", "identifiers: [--t]", "schema: {type: string, variable: APP_TOKEN}"),
			new:  f("name: t", "identifiers: [--t]", "schema: {type: string, variable: APP_KEY}"),
			want: []string{"breaking INPUT_ENV_NO_DELETE app --t $APP_TOKEN", "safe INPUT_ENV_ADDED app --t $APP_KEY"},
		},
		{
			name: "env input gains a second variable",
			old:  "  env:\n    - {name: a, schema: {type: string, variable: APP_A}}\n",
			new:  "  env:\n    - {name: a, schema: {type: string, variable: [APP_A, APP_ALT]}}\n",
			want: []string{"safe ENV_VARIABLE_ADDED app $APP_ALT"},
		},
		{
			name: "env input loses one of its variables",
			old:  "  env:\n    - {name: a, schema: {type: string, variable: [APP_A, APP_ALT]}}\n",
			new:  "  env:\n    - {name: a, schema: {type: string, variable: APP_A}}\n",
			want: []string{"breaking ENV_VARIABLE_NO_DELETE app $APP_ALT"},
		},
		{
			name: "every value source removed", old: f("name: t", "identifiers: [--t]", "schema: {type: string, from: [file, stdin]}"),
			new:  f("name: t", "identifiers: [--t]", "schema: {type: string}"),
			want: []string{"breaking INPUT_FROM_NO_DELETE app --t"},
		},
		{
			name: "a secret gains literals and stdin", old: f("name: t", "identifiers: [--t]", "schema: {type: string, secret: true, from: [file]}"),
			new:  f("name: t", "identifiers: [--t]", "schema: {type: string, secret: true, from: [file, value, stdin]}"),
			want: []string{"possibly_breaking INPUT_FROM_ADDED app --t"},
		},
		{
			name: "argument inserted before another", old: "  arguments:\n    - {name: b, schema: {type: string}}\n",
			new:  "  arguments:\n    - {name: a, schema: {type: string}}\n    - {name: b, schema: {type: string}}\n",
			want: []string{"breaking ARGUMENT_MOVED app <b>", "safe ARGUMENT_ADDED app <a>"},
		},
	})
}

func TestDiffMatrix_outputVariants(t *testing.T) {
	sub := func(extra string) string { return "  commands:\n    - name: run\n" + extra }
	out := func(props string) string {
		return sub("      output:\n        type: object\n        properties: " + props + "\n")
	}
	exit := func(output string) string {
		return sub("      exit_status:\n        - code: 3\n          summary: x\n" + output)
	}
	runDiffCases(t, []diffCase{
		{
			name: "output field now nullable", old: out("{a: {type: string}}"), new: out("{a: {type: string, nullable: true}}"),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app run output.a"},
		},
		{
			name: "output field no longer nullable", old: out("{a: {type: string, nullable: true}}"), new: out("{a: {type: string}}"),
			want: nil,
		},
		{
			name: "output integer widened to number", old: out("{a: {type: integer}}"), new: out("{a: {type: number}}"),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app run output.a"},
		},
		{
			name: "output number narrowed to integer", old: out("{a: {type: number}}"), new: out("{a: {type: integer}}"),
			want: nil,
		},
		{
			name: "output list item field removed", old: out("{items: {type: array, items: {type: object, properties: {id: {type: string}, n: {type: integer}}}}}"),
			new:  out("{items: {type: array, items: {type: object, properties: {id: {type: string}}}}}"),
			want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE app run output.items[].n"},
		},
		{
			name: "output enum added", old: out("{s: {type: string}}"), new: out("{s: {type: string, enum: [a]}}"),
			want: []string{"safe OUTPUT_ENUM_ADDED app run output.s"},
		},
		{
			name: "output enum removed", old: out("{s: {type: string, enum: [a]}}"), new: out("{s: {type: string}}"),
			want: []string{"possibly_breaking OUTPUT_ENUM_REMOVED app run output.s"},
		},
		{
			name: "output field gains a bound", old: out("{n: {type: integer}}"), new: out("{n: {type: integer, minimum: 0}}"),
			want: nil,
		},
		{
			name: "exit status output added", old: exit(""), new: exit("          output: {type: object, properties: {why: {type: string}}}\n"),
			want: []string{"safe OUTPUT_ADDED app run exit 3 output"},
		},
		{
			name: "exit status output removed", old: exit("          output: {type: object, properties: {why: {type: string}}}\n"), new: exit(""),
			want: []string{"breaking OUTPUT_NO_DELETE app run exit 3 output"},
		},
		{
			name: "exit status output field type changed",
			old:  exit("          output: {type: object, properties: {why: {type: string}}}\n"),
			new:  exit("          output: {type: object, properties: {why: {type: integer}}}\n"),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app run exit 3 output.why"},
		},
		{
			name: "exit status removed with its output",
			old:  exit("          output: {type: object, properties: {why: {type: string}}}\n"),
			new:  sub(""),
			want: []string{"possibly_breaking EXIT_STATUS_NO_DELETE app run exit 3"},
		},
	})
}

func TestDiffMatrix_lessTravelledBranches(t *testing.T) {
	sub := func(extra string) string { return "  commands:\n    - name: run\n" + extra }
	flag := func(schema string) string {
		return "  flags:\n    - {name: v, identifiers: [--v], schema: " + schema + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "exit name removed", old: sub("      exit_status:\n        - {code: 3, name: locked, summary: x}\n"),
			new:  sub("      exit_status:\n        - {code: 3, summary: x}\n"),
			want: []string{"possibly_breaking EXIT_NAME_CHANGED app run exit 3"},
		},
		{
			name: "sub-command of a renamed parent removed",
			old:  "  commands:\n    - name: old\n      commands:\n        - name: leaf\n        - name: keep\n",
			new:  "  commands:\n    - name: new\n      aliases: [old]\n      commands:\n        - name: keep\n",
			want: []string{"safe COMMAND_RENAMED_ALIAS_KEPT app old", "breaking COMMAND_NO_DELETE app old leaf"},
		},
		{
			name: "deprecated enum alias removed", old: flag("{type: string, enum: [{value: yaml, aliases: [yml], deprecated_aliases: [yl]}]}"),
			new:  flag("{type: string, enum: [{value: yaml, aliases: [yml]}]}"),
			want: []string{"breaking ENUM_ALIAS_NO_DELETE app --v value yaml alias yl"},
		},
		{
			name: "multipleOf added", old: flag("{type: int}"), new: flag("{type: int, multipleOf: 5}"),
			want: []string{"breaking INPUT_BOUND_ADDED app --v"},
		},
		{
			name: "multipleOf removed", old: flag("{type: int, multipleOf: 5}"), new: flag("{type: int}"),
			want: []string{"safe INPUT_BOUND_REMOVED app --v"},
		},
		{name: "uint8 to uint16", old: flag("{type: uint8}"), new: flag("{type: uint16}"), want: []string{"safe INPUT_TYPE_WIDENED app --v"}},
		{name: "uint32 to int64", old: flag("{type: uint32}"), new: flag("{type: int64}"), want: []string{"safe INPUT_TYPE_WIDENED app --v"}},
		{name: "uint to int", old: flag("{type: uint}"), new: flag("{type: int}"), want: []string{"breaking INPUT_TYPE_CHANGED app --v"}},
		{name: "int32 to float64", old: flag("{type: int32}"), new: flag("{type: float64}"), want: []string{"safe INPUT_TYPE_WIDENED app --v"}},
		{name: "int64 to float64", old: flag("{type: int64}"), new: flag("{type: float64}"), want: []string{"possibly_breaking INPUT_TYPE_CHANGED app --v"}},
		{name: "float64 to float32", old: flag("{type: float64}"), new: flag("{type: float32}"), want: []string{"breaking INPUT_TYPE_CHANGED app --v"}},
	})
}

func TestDiffMatrix_identifierMoves(t *testing.T) {
	flags := func(list ...string) string { return "  flags:\n    - " + strings.Join(list, "\n    - ") + "\n" }
	envs := func(list ...string) string { return "  env:\n    - " + strings.Join(list, "\n    - ") + "\n" }
	runDiffCases(t, []diffCase{
		{
			name: "identifier moved to another flag",
			old:  flags("{name: a, identifiers: [--a, -x], schema: {type: string}}", "{name: b, identifiers: [--b], schema: {type: string}}"),
			new:  flags("{name: a, identifiers: [--a], schema: {type: string}}", "{name: b, identifiers: [--b, -x], schema: {type: string}}"),
			want: []string{"breaking FLAG_IDENTIFIER_NO_DELETE app -x"},
		},
		{
			name: "identifier listed first moved to another flag",
			old:  flags("{name: a, identifiers: [-x, --a], schema: {type: string}}", "{name: b, identifiers: [--b], schema: {type: string}}"),
			new:  flags("{name: a, identifiers: [--a], schema: {type: string}}", "{name: b, identifiers: [-x, --b], schema: {type: string}}"),
			want: []string{"breaking FLAG_IDENTIFIER_NO_DELETE app -x"},
		},
		{
			name: "identifier moved to a new flag",
			old:  flags("{name: a, identifiers: [--a, -x], schema: {type: string}}"),
			new:  flags("{name: a, identifiers: [--a], schema: {type: string}}", "{name: c, identifiers: [--c, -x], schema: {type: string}}"),
			want: []string{"breaking FLAG_IDENTIFIER_NO_DELETE app -x", "safe FLAG_ADDED app --c"},
		},
		{
			name: "hidden identifier moved to another flag",
			old:  flags("{name: a, identifiers: [--a], hidden_identifiers: [-x], schema: {type: string}}", "{name: b, identifiers: [--b], schema: {type: string}}"),
			new:  flags("{name: a, identifiers: [--a], schema: {type: string}}", "{name: b, identifiers: [--b], hidden_identifiers: [-x], schema: {type: string}}"),
			want: []string{"breaking FLAG_HIDDEN_IDENTIFIER_NO_DELETE app -x"},
		},
		{
			name: "identifier moved, its removal planned", release: "2.0.0",
			old: flags("{name: a, identifiers: [--a, -x], deprecated_identifiers: [-x], deprecated_identifiers_removed_in: {-x: 2.0.0}, schema: {type: string}}",
				"{name: b, identifiers: [--b], schema: {type: string}}"),
			new:  flags("{name: a, identifiers: [--a], schema: {type: string}}", "{name: b, identifiers: [--b, -x], schema: {type: string}}"),
			want: []string{"expected FLAG_IDENTIFIER_NO_DELETE app -x", "safe LIFECYCLE_CHANGED app --a"},
		},
		{
			name: "identifier a command's own flag shadowed now reaches the cascading flag",
			old: "  flags:\n    - {name: verbose, identifiers: [--verbose, -v], cascading: true, schema: {type: bool}}\n" +
				"  commands:\n    - name: run\n      flags:\n        - {name: a, identifiers: [--a, -v], schema: {type: string}}\n",
			new: "  flags:\n    - {name: verbose, identifiers: [--verbose, -v], cascading: true, schema: {type: bool}}\n" +
				"  commands:\n    - name: run\n      flags:\n        - {name: a, identifiers: [--a], schema: {type: string}}\n",
			want: []string{"breaking FLAG_IDENTIFIER_NO_DELETE app run -v"},
		},
		{
			name: "environment variable moved to an input listed first",
			old:  envs("{name: a, schema: {type: string, variable: [APP_X, APP_A]}}", "{name: b, schema: {type: string, variable: APP_B}}"),
			new:  envs("{name: b, schema: {type: string, variable: [APP_B, APP_X]}}", "{name: a, schema: {type: string, variable: APP_A}}"),
			want: []string{"breaking ENV_VARIABLE_NO_DELETE app $APP_X", "safe ENV_VARIABLE_ADDED app $APP_X"},
		},
	})
}

func TestDiffMatrix_configFileSchema(t *testing.T) {
	file := func(schema string) string {
		if schema != "" {
			schema = ", schema: " + schema
		}
		return "  config_files:\n    - {name: main, path: ~/.app.yaml" + schema + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "schema declared", old: file(""), new: file("{type: object, properties: {region: {type: string}}}"),
			want: []string{"possibly_breaking CONFIG_FILE_SCHEMA_ADDED app config_files main"},
		},
		{
			name: "schema dropped", old: file("{type: object, properties: {region: {type: string}}}"), new: file(""),
			want: []string{"safe CONFIG_FILE_SCHEMA_REMOVED app config_files main"},
		},
		{
			name: "key now required", old: file("{type: object, properties: {region: {type: string}}}"),
			new:  file("{type: object, required: [region], properties: {region: {type: string}}}"),
			want: []string{"breaking INPUT_PROPERTY_REQUIRED_ADDED app config_files main.region"},
		},
		{
			name: "key no longer required", old: file("{type: object, required: [region], properties: {region: {type: string}}}"),
			new:  file("{type: object, properties: {region: {type: string}}}"),
			want: nil,
		},
		{
			name: "key type changed", old: file("{type: object, properties: {port: {type: number}}}"),
			new:  file("{type: object, properties: {port: {type: integer}}}"),
			want: []string{"breaking INPUT_TYPE_CHANGED app config_files main.port"},
		},
		{
			name: "key enum narrowed", old: file("{type: object, properties: {mode: {type: string, enum: [a, b]}}}"),
			new:  file("{type: object, properties: {mode: {type: string, enum: [a]}}}"),
			want: []string{"breaking ENUM_VALUE_NO_DELETE app config_files main.mode value b"},
		},
		{
			name: "key bound narrowed", old: file("{type: object, properties: {port: {type: integer, maximum: 65535}}}"),
			new:  file("{type: object, properties: {port: {type: integer, maximum: 1024}}}"),
			want: []string{"breaking INPUT_BOUND_NARROWED app config_files main.port"},
		},
		{
			name: "key bound widened", old: file("{type: object, properties: {port: {type: integer, maximum: 1024}}}"),
			new:  file("{type: object, properties: {port: {type: integer, maximum: 65535}}}"),
			want: []string{"safe INPUT_BOUND_WIDENED app config_files main.port"},
		},
		{
			name: "optional key added", old: file("{type: object, properties: {region: {type: string}}}"),
			new:  file("{type: object, properties: {region: {type: string}, zone: {type: string}}}"),
			want: []string{"safe INPUT_PROPERTY_ADDED app config_files main.zone"},
		},
		{
			name: "named schema gains a required key",
			old:  "  schemas:\n    Cfg: {type: object, properties: {region: {type: string}}}\n" + file(`{$ref: "#/schemas/Cfg"}`),
			new:  "  schemas:\n    Cfg: {type: object, required: [region], properties: {region: {type: string}}}\n" + file(`{$ref: "#/schemas/Cfg"}`),
			want: []string{"breaking INPUT_PROPERTY_REQUIRED_ADDED definitions.Cfg.region"},
		},
	})
}

func TestDiffMatrix_stdinStreamAndPluginTimeout(t *testing.T) {
	sub := func(extra string) string { return "  commands:\n    - name: run\n" + extra }
	plugin := func(timeout string) string {
		return "  plugins:\n    - {name: lint, summary: lints" + timeout + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "stdin now streamed", old: sub("      stdin: {format: lines}\n"), new: sub("      stdin: {format: lines, stream: true}\n"),
			want: []string{"possibly_breaking STDIN_STREAM_CHANGED app run stdin"},
		},
		{
			name: "stdin no longer streamed", old: sub("      stdin: {format: jsonl, stream: true}\n"), new: sub("      stdin: {format: jsonl}\n"),
			want: []string{"possibly_breaking STDIN_STREAM_CHANGED app run stdin"},
		},
		{
			name: "plugin timeout changed", old: plugin(", timeout: 10s"), new: plugin(", timeout: 30s"),
			want: []string{"possibly_breaking PLUGIN_TIMEOUT_CHANGED app lint"},
		},
		{
			name: "plugin timeout added", old: plugin(""), new: plugin(", timeout: 10s"),
			want: []string{"possibly_breaking PLUGIN_TIMEOUT_CHANGED app lint"},
		},
		{
			name: "plugin timeout removed", old: plugin(", timeout: 10s"), new: plugin(""),
			want: []string{"possibly_breaking PLUGIN_TIMEOUT_CHANGED app lint"},
		},
		{
			name: "plugin timeout written another way", old: plugin(", timeout: 90s"), new: plugin(", timeout: 1m30s"),
			want: nil,
		},
	})
}

func TestDiffMatrix_hiddenCommandItems(t *testing.T) {
	hidden := func(extra string) string { return "  commands:\n    - name: run\n      hidden: true\n" + extra }
	flag := func(ids, schema string) string {
		return "      flags:\n        - {name: f, identifiers: " + ids + ", schema: " + schema + "}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "hidden command removed", old: hidden(""), new: "",
			want: []string{"possibly_breaking COMMAND_NO_DELETE app run"},
		},
		{
			name: "alias of a hidden command removed", old: hidden("      aliases: [r]\n"), new: hidden(""),
			want: []string{"possibly_breaking ALIAS_NO_DELETE app run alias r"},
		},
		{
			name: "flag of a hidden command removed", old: hidden(flag("[--f]", "{type: string}")), new: hidden(""),
			want: []string{"possibly_breaking FLAG_NO_DELETE app run --f"},
		},
		{
			name: "identifier of a hidden command's flag removed",
			old:  hidden(flag("[--f, -f]", "{type: string}")), new: hidden(flag("[--f]", "{type: string}")),
			want: []string{"possibly_breaking FLAG_IDENTIFIER_NO_DELETE app run -f"},
		},
		{
			name: "enum value of a hidden command's flag removed",
			old:  hidden(flag("[--f]", "{type: string, enum: [a, b]}")), new: hidden(flag("[--f]", "{type: string, enum: [a]}")),
			want: []string{"possibly_breaking ENUM_VALUE_NO_DELETE app run --f value b"},
		},
		{
			name: "argument of a hidden command removed",
			old:  hidden("      arguments:\n        - {name: a, schema: {type: string}}\n"), new: hidden(""),
			want: []string{"possibly_breaking ARGUMENT_NO_DELETE app run <a>"},
		},
		{
			name: "variable of a hidden command's env input removed",
			old:  hidden("      env:\n        - {name: a, schema: {type: string, variable: [APP_A, APP_ALT]}}\n"),
			new:  hidden("      env:\n        - {name: a, schema: {type: string, variable: APP_A}}\n"),
			want: []string{"possibly_breaking ENV_VARIABLE_NO_DELETE app run $APP_ALT"},
		},
		{
			name: "flag of a command under a hidden one removed",
			old:  hidden("      commands:\n        - name: leaf\n          flags:\n            - {name: f, identifiers: [--f], schema: {type: string}}\n"),
			new:  hidden("      commands:\n        - name: leaf\n"),
			want: []string{"possibly_breaking FLAG_NO_DELETE app run leaf --f"},
		},
		{
			name: "identifier of a hidden flag removed",
			old:  "  flags:\n    - {name: f, identifiers: [--f, -f], hidden: true, schema: {type: string}}\n",
			new:  "  flags:\n    - {name: f, identifiers: [--f], hidden: true, schema: {type: string}}\n",
			want: []string{"possibly_breaking FLAG_IDENTIFIER_NO_DELETE app -f"},
		},
		{
			name: "a hidden command's flag narrowed is still rated as is",
			old:  hidden(flag("[--f]", "{type: int}")), new: hidden(flag("[--f]", "{type: int8}")),
			want: []string{"breaking INPUT_TYPE_CHANGED app run --f"},
		},
	})
}
