package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateSchemaDocs rewrites the generated reference pages:
// `go test ./internal/codegen -run SchemaDocs -update-schema-docs`.
var updateSchemaDocs = flag.Bool("update-schema-docs", false, "rewrite the generated schema reference pages")

// schemaDocPages maps each generated Hugo page to the schema it renders and the weight that
// orders it within its section.
func schemaDocPages() []struct {
	path, title, weight string
	raw                 []byte
} {
	return []struct {
		path, title, weight string
		raw                 []byte
	}{
		{filepath.Join("docs", "content", "specification", "reference.md"), "spec reference", "20", schemaSpecFileBytes},
		{filepath.Join("docs", "content", "configuration", "reference.md"), "conf reference", "20", schemaConfFileBytes},
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
	for _, page := range schemaDocPages() {
		got, err := renderSchemaMarkdown(page.title, page.weight, page.raw)
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
	page, err := renderSchemaMarkdown("spec reference", "20", schemaSpecFileBytes)
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
