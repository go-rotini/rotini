package codegen

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestRootOnlyKeys_schemaAgrees pins the spec schema to the keys lintDocLevelKeys keeps on the
// root: each one's description, and no other command key's, starts "Root only:", and the
// descriptions of the document, its `command` and the Command shape name every one.
func TestRootOnlyKeys_schemaAgrees(t *testing.T) {
	t.Parallel()
	type described struct {
		Description string `json:"description"`
	}
	var schema struct {
		Description string `json:"description"`
		Properties  struct {
			Command described `json:"command"`
		} `json:"properties"`
		Definitions struct {
			Command struct {
				Description string               `json:"description"`
				Properties  map[string]described `json:"properties"`
			} `json:"Command"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(schemaSpecFileBytes, &schema); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, k := range rootOnlyKeys() {
		keys = append(keys, k.key)
	}
	for name, p := range schema.Definitions.Command.Properties {
		if got, want := strings.HasPrefix(p.Description, "Root only: "), slices.Contains(keys, name); got != want {
			t.Errorf("Command.%s: description starts with \"Root only: \" = %v, want %v", name, got, want)
		}
	}
	for _, k := range keys {
		if _, ok := schema.Definitions.Command.Properties[k]; !ok {
			t.Errorf("root-only key %q is not a Command property", k)
		}
	}
	for where, desc := range map[string]string{
		"the document":        schema.Description,
		"command":             schema.Properties.Command.Description,
		"definitions/Command": schema.Definitions.Command.Description,
	} {
		for _, k := range keys {
			if !strings.Contains(desc, "`"+k+"`") {
				t.Errorf("%s's description doesn't name the root-only key `%s`:\n%s", where, k, desc)
			}
		}
	}
}
