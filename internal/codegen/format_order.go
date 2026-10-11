package codegen

// commandKeyOrder is the order `rotini fmt` writes a command's keys in, the order a reader
// wants them: what the command is called and does, then what it reads, then what it writes,
// then what it is made of.
var commandKeyOrder = []string{
	"$schema",
	"name",

	// What it is called, and its documentation.
	"aliases",
	"hidden_aliases",
	"deprecated_identifiers",
	"deprecated_identifiers_removed_in",
	"display_name",
	"summary",
	"description",
	"usage",
	"examples",
	"group",
	"groups",
	"header",
	"footer",
	"headings",
	"help",
	"man",
	"markdown",
	"topics",
	"see_also",
	"exit_status",
	"stability",
	"deprecated",
	"deprecated_since",
	"removed_in",
	"replaced_by",
	"effects",
	"agent",
	"hidden",

	// What it reads.
	"env_prefix",
	"schemas",
	"flag_sets",
	"use",
	"flags",
	"arguments",
	"env",
	"config",
	"stdin",
	"config_files",
	"flag_groups",
	"flag_dependencies",
	"response_files",
	"options_first",
	"passthrough",

	// What it writes.
	"output",
	"output_stream",

	// What it is made of.
	"commands",
	"$ref",
	"handler",
	"filename",
	"plugins",
	"plugin_discovery",
	"plugin_path",
	"multicall",
	"timeout",
}

// inputKeyOrder is the order `rotini fmt` writes a flag's, argument's, env or config input's
// keys in: its names and documentation, then how it behaves, then its value's schema.
var inputKeyOrder = []string{
	"name",
	"identifiers",
	"hidden_identifiers",
	"deprecated_identifiers",
	"summary",
	"description",

	"group",
	"hidden",
	"cascading",
	"short_circuit",
	"passthrough",
	"role",
	"role_value",
	"effects",
	"agent",
	"stability",
	"deprecated",
	"deprecated_since",
	"removed_in",
	"deprecated_identifiers_removed_in",
	"replaced_by",

	"schema",
}

// fmtKeyOrders maps a schema definition to the key order that replaces its declared order.
var fmtKeyOrders = map[string][]string{
	"Command":       commandKeyOrder,
	"FlagInput":     inputKeyOrder,
	"ArgumentInput": inputKeyOrder,
	"EnvInput":      inputKeyOrder,
	"ConfigInput":   inputKeyOrder,
}

// fmtTypeFirst names the definitions whose `type` is written first: value schemas, and the
// conf's features, where `type` says what the entry is.
var fmtTypeFirst = map[string]bool{"BaseSchema": true, "InputSchema": true, "Schema": true, "Feature": true}

// fmtRanks returns each key's position in the canonical order of an object with schema sch.
// A command or input takes its reading order from fmtKeyOrders. Any other object keeps the
// schema's declared order, with "$schema" and "name" first, or `type` first for fmtTypeFirst.
func fmtRanks(sch *fmtSchema) map[string]int {
	rank := map[string]int{}
	if order, ok := fmtKeyOrders[sch.kind]; ok {
		for i, key := range order {
			rank[key] = i
		}
		return rank
	}
	hoisted := []string{"$schema", "name"}
	if fmtTypeFirst[sch.kind] {
		hoisted = []string{"type"}
	}
	for i, key := range hoisted {
		rank[key] = i - len(hoisted)
	}
	for i, key := range sch.props {
		if _, ok := rank[key]; !ok {
			rank[key] = i
		}
	}
	return rank
}
