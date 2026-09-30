package codegen

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/yaml"
)

// updateSchemaDocs rewrites the generated reference pages:
// `go test ./internal/codegen -run SchemaDocs -update-schema-docs`.
var updateSchemaDocs = flag.Bool("update-schema-docs", false, "rewrite the generated schema reference pages")

// schemaDocPage is one generated Hugo page: the spec page or the conf page. Each shows an
// example file using every key, the JSON Schema itself, and an explanation of every key.
type schemaDocPage struct {
	path     string // where the page is written, from the repository root
	title    string // the Hugo title (the nav and URL name)
	fileName string // the file the page documents, as its heading
	example  string // asset path of the every-key example file (docs/assets/…)
	schema   string // asset path of the JSON Schema, mounted from the repository root
	raw      []byte
}

// schemaDocPages lists the generated pages.
func schemaDocPages() []schemaDocPage {
	return []schemaDocPage{
		{filepath.Join("docs", "content", "specification", "_index.md"), "specification", ".rotini.spec.yaml",
			"examples/rotini.spec.yaml", "schemas/schema-spec.json", schemaSpecFileBytes},
		{filepath.Join("docs", "content", "configuration", "_index.md"), "configuration", ".rotini.conf.yaml",
			"examples/rotini.conf.yaml", "schemas/schema-conf.json", schemaConfFileBytes},
	}
}

// TestSchemaDocsInSync keeps the published reference pages identical to what the schemas say.
//
// rotini's real documentation has always been in the schemas — a paragraph per key, saying
// what it does, what it rejects and why, and test-guarded for accuracy. The published site had
// a hand-written skeleton instead: 974 lines for a 119-key schema, which nobody could keep in
// step by hand and nobody did. Generating the pages means writing a key documents it, and
// there is no second copy to drift.
func TestSchemaDocsInSync(t *testing.T) {
	root := filepath.Join("..", "..")
	table := inputChannelTable(t)
	for _, page := range schemaDocPages() {
		channels := ""
		if strings.HasPrefix(page.title, "spec") {
			channels = table
		}
		got, err := renderSchemaMarkdown(page, channels)
		if err != nil {
			t.Fatalf("render %s: %v", page.path, err)
		}
		path := filepath.Join(root, page.path)

		if *updateSchemaDocs {
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s", page.path)
			continue
		}

		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("generated reference page missing (run with -update-schema-docs): %v", err)
			continue
		}
		if got != string(want) {
			t.Errorf("%s is stale — a schema description changed; re-run with -update-schema-docs", page.path)
		}
	}
}

// TestSchemaDocsAreComplete is the guard that makes the generated pages worth publishing:
// every definition and every key the schema declares has to appear, or the page is a partial
// reference presented as a full one.
func TestSchemaDocsAreComplete(t *testing.T) {
	page, err := renderSchemaMarkdown(schemaDocPages()[0], "")
	if err != nil {
		t.Fatal(err)
	}

	// Every definition gets its own section.
	for _, def := range []string{
		"Command", "FlagInput", "ArgumentInput", "EnvInput", "ConfigInput", "InputSchema",
		"BaseSchema", "Schema", "StdinSpec", "FlagGroup", "FlagDependency",
		"RemoteCommandSpec", "RemoteDiscovery", "HandlerSource", "HelpHeadings",
		"ExitStatusEntry", "ConfigurationFile", "ConfigurationFileDiscover",
	} {
		if !strings.Contains(page, "## "+def+"\n") {
			t.Errorf("no section for definition %s", def)
		}
	}

	// A sample of keys across the channels, including every one added in the schema
	// finishing pass — the ones most likely to be added without documenting.
	for _, key := range []string{
		"name", "$ref", "flags", "arguments", "env", "config", "config_files", "stdin",
		"flag_groups", "flag_dependencies", "passthrough", "handler", "remote_commands",
		"variable", "negatable", "complete", "group", "dotted_keys", "from", "config_source",
		"nesting", "placeholder", "secret", "required", "default", "enum", "pattern",
	} {
		if !strings.Contains(page, "### `"+key+"`") {
			t.Errorf("no entry for key %q", key)
		}
	}

	// Descriptions are the whole point: a page of key names with no prose would pass the
	// checks above and be worthless.
	for _, phrase := range []string{
		"env_prefix",
		"TextUnmarshaler",
		"commands all the way down",
	} {
		if !strings.Contains(page, phrase) {
			t.Errorf("the rendered page does not carry %q — descriptions are missing", phrase)
		}
	}
}

// Rendering the embedded JSON Schemas as Markdown reference pages.
//
// rotini's real documentation has always lived in the schemas: every key carries a paragraph
// saying what it does, what it rejects, and why — and TestReferenceDocsValidate keeps them
// honest by failing the build when they drift. What the published site had instead was a
// hand-written skeleton: 974 lines of prose for a 119-key schema, which no one could keep in
// step by hand and no one did.
//
// So the site's reference pages are GENERATED from the same bytes validation judges. There is
// no second copy to drift, writing a key documents it, and TestSchemaDocsInSync fails until
// the pages are regenerated.

// schemaDoc is the subset of JSON Schema the renderer reads. It is deliberately not a full
// Draft-7 model: these are rotini's own schemas, whose shapes are known.
type schemaDoc struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Type        any                  `json:"type"`
	Required    []string             `json:"required"`
	Properties  map[string]schemaDoc `json:"properties"`
	Definitions map[string]schemaDoc `json:"definitions"`
	Items       *schemaDoc           `json:"items"`
	Ref         string               `json:"$ref"`
	Enum        []any                `json:"enum"`
	Default     any                  `json:"default"`
	Pattern     string               `json:"pattern"`
	AllOf       []schemaDoc          `json:"allOf"`
	AnyOf       []schemaDoc          `json:"anyOf"`
}

// renderSchemaMarkdown renders one schema as a Hugo content page: the document's own keys
// first, then a section per definition, each a table of keys with their full descriptions.
func renderSchemaMarkdown(page schemaDocPage, channelTable string) (string, error) {
	var doc schemaDoc
	if err := json.Unmarshal(page.raw, &doc); err != nil {
		return "", fmt.Errorf("parse schema for %s: %w", page.fileName, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %q\n---\n\n", page.title)
	b.WriteString("<!-- Code generated from the rotini JSON Schema; DO NOT EDIT.\n")
	b.WriteString("     Edit the schema's descriptions, or the example in docs/assets/examples, then run:\n")
	b.WriteString("       go test ./internal/codegen -run SchemaDocs -update-schema-docs -->\n\n")

	fmt.Fprintf(&b, "# %s\n\n", page.fileName)
	if doc.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", doc.Description)
	}
	fmt.Fprintf(&b, "{{< code title=\"%s — every key\" language=\"yaml\" file=%q open=\"true\" copy=\"true\" >}}{{< /code >}}\n\n", page.fileName, page.example)
	fmt.Fprintf(&b, "{{< code title=\"the JSON Schema\" language=\"json\" file=%q open=\"false\" copy=\"true\" >}}{{< /code >}}\n\n", page.schema)
	b.WriteString("Below, every key. `rotini validate` checks all of them before any code is generated, and\n")
	b.WriteString("this list is rendered from the schema, so it always matches what the tool accepts.\n\n")

	b.WriteString("## Document\n\n")
	b.WriteString(renderKeyList(doc, doc.Required, keyOrder(doc, doc.Required), "###"))

	for _, name := range definitionOrder(doc) {
		def := doc.Definitions[name]
		fmt.Fprintf(&b, "\n## %s\n\n", name)
		if d := flattenedDescription(def); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
		}
		if name == "InputSchema" && channelTable != "" {
			b.WriteString(channelTable)
		}
		flat, required := flattenAllOf(def), flattenedRequired(def)
		if name == "Command" {
			b.WriteString(renderGroupedKeys(flat, required))
			continue
		}
		b.WriteString(renderKeyList(flat, required, keyOrder(flat, required), "###"))
	}
	return b.String(), nil
}

// commandKeyGroups orders a command's keys by the job each does, most-reached-for first — the
// command section is the one an author reads most, and alphabetical order put `name` 27th.
// TestCommandKeyGroupsCoverTheSchema fails until a new Command key is placed in a group.
var commandKeyGroups = []struct {
	title string
	keys  []string
}{
	{"Identity and visibility", []string{"name", "aliases", "hidden", "deprecated", "deprecated_identifiers"}},
	{"Inputs", []string{"flags", "arguments", "env", "config", "stdin", "config_files", "env_prefix", "flag_groups", "flag_dependencies"}},
	{"Sub-commands and composition", []string{"commands", "$ref", "handler", "passthrough", "remote_commands", "remote_discovery", "plugin_path", "timeout"}},
	{"Documentation", []string{"summary", "description", "usage", "examples", "exit_status", "see_also", "group", "header", "footer", "headings", "help", "man", "markdown"}},
	{"Output and shared types", []string{"output", "schemas"}},
	{"Generated code", []string{"filename"}},
}

// renderGroupedKeys renders the Command section under commandKeyGroups' headings.
func renderGroupedKeys(doc schemaDoc, required []string) string {
	var b strings.Builder
	for _, g := range commandKeyGroups {
		fmt.Fprintf(&b, "### %s\n\n", g.title)
		b.WriteString(renderKeyList(doc, required, g.keys, "####"))
	}
	return b.String()
}

// keyOrder lists a shape's keys required first, in the order the schema requires them, then
// the rest alphabetically — what a reader needs to write a valid document comes before what
// refines it.
func keyOrder(doc schemaDoc, required []string) []string {
	out := make([]string, 0, len(doc.Properties))
	seen := map[string]bool{}
	for _, r := range required {
		if _, ok := doc.Properties[r]; ok && !seen[r] {
			out, seen[r] = append(out, r), true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(doc.Properties)) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

// definitionOrder lists the definitions in the order a reader meets them, walking from the
// document's own keys through every reference — so Command follows the document, and the
// shapes a command's keys name follow it. A definition nothing references comes last.
func definitionOrder(doc schemaDoc) []string {
	var out []string
	seen := map[string]bool{}
	enqueue := func(ref string) {
		name := ref[strings.LastIndex(ref, "/")+1:]
		if _, ok := doc.Definitions[name]; ok && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	var visit func(d schemaDoc, order []string)
	visit = func(d schemaDoc, order []string) {
		if d.Ref != "" {
			enqueue(d.Ref)
		}
		flat := flattenAllOf(d)
		if order == nil {
			order = keyOrder(flat, flattenedRequired(d))
		}
		for _, k := range order {
			visit(flat.Properties[k], nil)
		}
		for _, part := range d.AllOf {
			if part.Ref != "" {
				enqueue(part.Ref)
			}
		}
		if d.Items != nil {
			visit(*d.Items, nil)
		}
		for _, alt := range d.AnyOf {
			visit(alt, nil)
		}
	}
	visit(doc, nil)
	for i := 0; i < len(out); i++ { // breadth-first through what each definition names
		var order []string
		if out[i] == "Command" { // walked in the order its section presents its keys
			for _, g := range commandKeyGroups {
				order = append(order, g.keys...)
			}
		}
		visit(doc.Definitions[out[i]], order)
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Definitions)) {
		if !seen[name] {
			out = append(out, name)
		}
	}
	return out
}

// TestCommandKeyGroupsCoverTheSchema: every Command key is placed in exactly one group.
func TestCommandKeyGroupsCoverTheSchema(t *testing.T) {
	var doc schemaDoc
	if err := json.Unmarshal(schemaSpecFileBytes, &doc); err != nil {
		t.Fatal(err)
	}
	placed := map[string]int{}
	for _, g := range commandKeyGroups {
		for _, k := range g.keys {
			placed[k]++
		}
	}
	props := flattenAllOf(doc.Definitions["Command"]).Properties
	for k := range props {
		if placed[k] != 1 {
			t.Errorf("Command key %q is in %d groups of commandKeyGroups, want exactly 1", k, placed[k])
		}
	}
	for k := range placed {
		if _, ok := props[k]; !ok {
			t.Errorf("commandKeyGroups names %q, which Command does not declare", k)
		}
	}
}

// renderKeyList renders the named properties of a schema, in the order given, as a definition
// list — one entry per key under a heading of the given level, its type and whether it is
// required on one line, and the schema's own paragraph beneath.
//
// A list rather than a table: these descriptions are paragraphs, and a table cell is the wrong
// shape for a paragraph.
func renderKeyList(doc schemaDoc, required, names []string, heading string) string {
	if len(doc.Properties) == 0 {
		return "_No keys._\n"
	}
	req := map[string]bool{}
	for _, r := range required {
		req[r] = true
	}

	var b strings.Builder
	for _, name := range names {
		p := doc.Properties[name]
		fmt.Fprintf(&b, "%s `%s`\n\n", heading, name)

		var facts []string
		if t := typeLabel(p); t != "" {
			facts = append(facts, t)
		}
		if req[name] {
			facts = append(facts, "**required**")
		}
		if len(p.Enum) > 0 {
			facts = append(facts, "one of "+joinLiterals(p.Enum))
		}
		if p.Default != nil {
			facts = append(facts, fmt.Sprintf("default `%v`", p.Default))
		}
		if len(facts) > 0 {
			fmt.Fprintf(&b, "%s\n\n", strings.Join(facts, " · "))
		}
		if d := flattenedDescription(p); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
		}
	}
	return b.String()
}

// typeLabel renders a property's type for the one-line fact row, following a $ref to the
// definition it names so a reader can jump to it.
func typeLabel(p schemaDoc) string {
	if p.Ref != "" {
		name := p.Ref[strings.LastIndex(p.Ref, "/")+1:]
		return fmt.Sprintf("[`%s`](#%s)", name, strings.ToLower(name))
	}
	switch t := p.Type.(type) {
	case string:
		if t == "array" && p.Items != nil {
			return "array of " + typeLabel(*p.Items)
		}
		return "`" + t + "`"
	case []any:
		parts := make([]string, 0, len(t))
		for _, v := range t {
			parts = append(parts, fmt.Sprintf("`%v`", v))
		}
		return strings.Join(parts, " or ")
	}
	if len(p.AllOf) > 0 || len(p.AnyOf) > 0 {
		return "`object`"
	}
	return ""
}

// flattenAllOf merges an allOf-composed definition into one shape, so a schema written as
// "the object, plus an if/then" documents as the object. rotini uses allOf for inheritance
// (Schema and InputSchema over BaseSchema) and for conditional requirements.
func flattenAllOf(doc schemaDoc) schemaDoc {
	if len(doc.AllOf) == 0 {
		return doc
	}
	out := doc
	if out.Properties == nil {
		out.Properties = map[string]schemaDoc{}
	}
	merged := make(map[string]schemaDoc, len(out.Properties))
	maps.Copy(merged, out.Properties)
	for _, part := range doc.AllOf {
		maps.Copy(merged, flattenAllOf(part).Properties)
	}
	out.Properties = merged
	return out
}

// flattenedRequired collects the required keys of a definition and of its allOf members,
// skipping the conditional branches, whose requirements apply only in that branch.
func flattenedRequired(doc schemaDoc) []string {
	out := append([]string(nil), doc.Required...)
	for _, part := range doc.AllOf {
		out = append(out, flattenedRequired(part)...)
	}
	return out
}

// flattenedDescription returns a schema's description, or the first one its allOf members
// carry when the outer shape has none.
func flattenedDescription(doc schemaDoc) string {
	if doc.Description != "" {
		return doc.Description
	}
	for _, part := range doc.AllOf {
		if d := flattenedDescription(part); d != "" {
			return d
		}
	}
	return ""
}

// joinLiterals renders an enum as inline code, comma-separated.
func joinLiterals(vals []any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("`%v`", v))
	}
	return strings.Join(parts, ", ")
}

// probeChannels are the four places an input is declared, with the YAML that declares one whose
// schema is %s.
var probeChannels = []struct{ name, decl string }{
	{"flag", "  flags:\n    - name: x\n      identifiers: [--x]\n      summary: s\n      schema: %s\n"},
	{"argument", "  arguments:\n    - name: x\n      summary: s\n      schema: %s\n"},
	{"env", "  env:\n    - name: x\n      summary: s\n      schema: %s\n"},
	{"config", "  config:\n    - name: x\n      summary: s\n      schema: %s\n"},
}

// inputKeySamples is one valid use of each input-schema key, with whatever companion key it
// needs (ignore_case needs an enum, negatable a bool). TestInputChannelTableCoversEveryKey fails
// until a new key gets one here, so the reference's channel table never silently omits a key.
var inputKeySamples = map[string]string{
	"$ref":             `{"$ref": "Name"}`,
	"complete":         `{"type": "string", "complete": {"kind": "file"}}`,
	"config_source":    `{"type": "string", "config_source": "main"}`,
	"default":          `{"type": "string", "default": "a"}`,
	"default_text":     `{"type": "string", "default_text": "the default"}`,
	"dotted_keys":      `{"type": "map", "dotted_keys": true}`,
	"enum":             `{"type": "string", "enum": ["a"]}`,
	"exclusiveMaximum": `{"type": "int", "exclusiveMaximum": 5}`,
	"exclusiveMinimum": `{"type": "int", "exclusiveMinimum": 1}`,
	"file":             `{"type": "string", "file": "main"}`,
	"from":             `{"type": "string", "from": ["file"]}`,
	"ignore_case":      `{"type": "string", "enum": ["a"], "ignore_case": true}`,
	"implicit_value":   `{"type": "string", "implicit_value": "a"}`,
	"import":           `{"type": "uuid.UUID", "import": "github.com/google/uuid"}`,
	"items":            `{"type": "array", "items": {"type": "int"}}`,
	"key":              `{"type": "string", "key": "k"}`,
	"layout":           `{"type": "date", "layout": "2006-01-02"}`,
	"maxItems":         `{"type": "array", "maxItems": 3}`,
	"maxLength":        `{"type": "string", "maxLength": 5}`,
	"maximum":          `{"type": "int", "maximum": 5}`,
	"minItems":         `{"type": "array", "minItems": 1}`,
	"minLength":        `{"type": "string", "minLength": 1}`,
	"minimum":          `{"type": "int", "minimum": 1}`,
	"multipleOf":       `{"type": "int", "multipleOf": 2}`,
	"negatable":        `{"type": "bool", "negatable": true}`,
	"nesting":          `{"type": "map", "nesting": "__"}`,
	"nullable":         `{"type": "string", "nullable": true}`,
	"pattern":          `{"type": "string", "pattern": "^a"}`,
	"pattern_message":  `{"type": "string", "pattern": "^a", "pattern_message": "must start with a"}`,
	"placeholder":      `{"type": "string", "placeholder": "X"}`,
	"properties":       `{"type": "map", "properties": {"k": {"type": "string"}}}`,
	"required":         `{"type": "string", "required": true}`,
	"secret":           `{"type": "string", "secret": true}`,
	"separator":        `{"type": "array", "separator": ","}`,
	"type":             `{"type": "string"}`,
	"variable":         `{"type": "string", "variable": "X_VAR"}`,
}

// inputChannelTable renders which input-schema keys each input channel accepts, by ASKING the
// validator: every sample above is validated on every channel. The rules live in a dozen lint
// rules, and a table written by hand would drift from them silently; this one cannot.
func inputChannelTable(t *testing.T) string {
	t.Helper()
	accepts := func(schema, decl string) bool {
		dir := t.TempDir()
		writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
		spec := "version: 0.0.0\ncommand:\n  name: demo\n  summary: s\n" +
			"  schemas:\n    Name: {type: string}\n" +
			"  config_files:\n    - name: main\n      path: c.yaml\n" +
			fmt.Sprintf(decl, schema)
		writeTestFile(t, dir, ".rotini.spec.yaml", spec)
		writeTestFile(t, dir, ".rotini.conf.yaml", lintFixtureConf)
		var warnings []error
		err := NewProcessor("0.0.0").Validate(filepath.Join(dir, ".rotini.spec.yaml"), filepath.Join(dir, ".rotini.conf.yaml"),
			false, "collect", func(string, error) {}, func(w []error) { warnings = append(warnings, w...) })
		return err == nil && len(warnings) == 0
	}
	// Every channel must accept a plain input, or a rejection below would be the probe's fault
	// rather than the key's.
	for _, ch := range probeChannels {
		if !accepts(inputKeySamples["type"], ch.decl) {
			t.Fatalf("the probe's plain %s input is rejected — fix the probe before trusting the table", ch.name)
		}
	}
	keys := slices.Sorted(maps.Keys(inputKeySamples))
	var b strings.Builder
	b.WriteString("### Which keys each kind of input accepts\n\n")
	b.WriteString("An input's `schema:` block takes the keys below, but not every key means something on every\n")
	b.WriteString("kind of input — a flag's `negatable` has no meaning for an environment variable, and\n")
	b.WriteString("`rotini validate` rejects it there. This table is produced by validating each key on each\n")
	b.WriteString("kind of input, so it is what the validator actually accepts. `stdin:` takes a JSON Schema\n")
	b.WriteString("document instead; see [StdinSpec](#stdinspec).\n\n")
	b.WriteString("| Key | flag | argument | env | config |\n|---|:-:|:-:|:-:|:-:|\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| `%s` |", k)
		for _, ch := range probeChannels {
			mark := "—"
			if accepts(inputKeySamples[k], ch.decl) {
				mark = "✓"
			}
			fmt.Fprintf(&b, " %s |", mark)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// TestInputChannelTableCoversEveryKey keeps the channel table complete: every key an input's
// schema accepts needs a sample in inputKeySamples.
func TestInputChannelTableCoversEveryKey(t *testing.T) {
	var doc schemaDoc
	if err := json.Unmarshal(schemaSpecFileBytes, &doc); err != nil {
		t.Fatal(err)
	}
	for key := range flattenAllOf(doc.Definitions["InputSchema"]).Properties {
		if _, ok := inputKeySamples[key]; !ok {
			t.Errorf("input-schema key %q has no sample in inputKeySamples, so the reference's channel table omits it", key)
		}
	}
	for key := range flattenAllOf(doc.Definitions["BaseSchema"]).Properties {
		if _, ok := inputKeySamples[key]; !ok {
			t.Errorf("schema key %q has no sample in inputKeySamples, so the reference's channel table omits it", key)
		}
	}
}

// TestEveryKeyExamplesAreValidAndComplete keeps the "every key" examples on the spec and conf
// pages honest: each must pass `rotini validate` with no problems, and each must use every key
// its schema declares. A new key fails this until it is added to the example.
func TestEveryKeyExamplesAreValidAndComplete(t *testing.T) {
	root := filepath.Join("..", "..")
	mod := t.TempDir()
	writeTestFile(t, mod, "go.mod", "module example.com/deploy\n\ngo 1.26\n")
	for _, page := range schemaDocPages() {
		body, err := os.ReadFile(filepath.Join(root, "docs", "assets", page.example))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, mod, "cmd/deploy/"+page.fileName, string(body))

		var used map[string]any
		if err := yaml.Unmarshal(body, &used); err != nil {
			t.Fatalf("%s: %v", page.example, err)
		}
		keys := map[string]bool{}
		collectYAMLKeys(used, keys)
		var doc schemaDoc
		if err := json.Unmarshal(page.raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, k := range schemaKeyNames(doc) {
			if !keys[k] {
				t.Errorf("%s does not use the key %q — add it, so the page shows every key", page.example, k)
			}
		}
	}
	var warnings []error
	err := NewProcessor("0.0.0").Validate(filepath.Join(mod, "cmd/deploy/.rotini.spec.yaml"), filepath.Join(mod, "cmd/deploy/.rotini.conf.yaml"),
		false, "collect", func(string, error) {}, func(w []error) { warnings = append(warnings, w...) })
	if err != nil || len(warnings) > 0 {
		t.Errorf("the every-key examples do not validate cleanly: %v %v", err, warnings)
	}
}

// collectYAMLKeys records every mapping key in a decoded YAML document.
func collectYAMLKeys(v any, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			into[k] = true
			collectYAMLKeys(e, into)
		}
	case []any:
		for _, e := range x {
			collectYAMLKeys(e, into)
		}
	}
}

// schemaKeyNames lists every property name a schema declares, at the root and in every
// definition.
func schemaKeyNames(doc schemaDoc) []string {
	names := map[string]bool{}
	for k := range doc.Properties {
		names[k] = true
	}
	for _, def := range doc.Definitions {
		for k := range flattenAllOf(def).Properties {
			names[k] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}
