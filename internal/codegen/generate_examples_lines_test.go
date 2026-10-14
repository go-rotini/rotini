package codegen

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

const multilineExamplesSpec = `version: 0.0.0
command:
  name: ml
  summary: s
  env_prefix: ML
  config_files:
    - name: app
      path: ./ml.yaml
    - name: tuning
      path: ./tuning.toml
    - name: editor
      path: ./editor.jsonc
  flags:
    - name: note
      identifiers: [--note]
      summary: "summary with\nnewline and: more"
      schema: { type: string, key: note, variable: ML_NOTE, default: "first\nsecond: x" }
`

const multilineExamplesConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/ml/zz_ml.go
      package: ml
  features:
    - type: config_example
      enabled: true
    - type: env_example
      enabled: true
`

// TestExamples_multilineTextStaysCommented pins that a summary or default holding a newline
// keeps every line of the config and env examples commented out, so each example still reads
// as an empty document.
func TestExamples_multilineTextStaysCommented(t *testing.T) {
	dir, _ := emitModule(t, multilineExamplesSpec, multilineExamplesConf)
	var doc any
	if err := yaml.Unmarshal([]byte(readEmitted(t, dir, "examples/config/app.example.yaml")), &doc); err != nil || doc != nil {
		t.Errorf("yaml example decodes to %#v (%v), want nothing", doc, err)
	}
	var tm map[string]any
	if err := toml.Unmarshal([]byte(readEmitted(t, dir, "examples/config/tuning.example.toml")), &tm); err != nil || len(tm) != 0 {
		t.Errorf("toml example decodes to %#v (%v), want nothing", tm, err)
	}
	var jm map[string]any
	if err := jsonc.Unmarshal([]byte(readEmitted(t, dir, "examples/config/editor.example.jsonc")), &jm); err != nil || len(jm) != 0 {
		t.Errorf("jsonc example decodes to %#v (%v), want {}", jm, err)
	}
	for line := range strings.SplitSeq(readEmitted(t, dir, ".env.example"), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			t.Errorf(".env.example has an uncommented line %q", line)
		}
	}
}

// TestCarapaceValues_keepsValuesLiteral pins what the carapace spec gets: an enum value
// starting with "$" or holding " ||| " is left out (carapace would run it as a macro or split it
// into modifiers), every other "$" is written "$$" (carapace substitutes ${NAME} in each
// value), a tab in a summary becomes a space, and a message keeps unbalanced parentheses.
func TestCarapaceValues_keepsValuesLiteral(t *testing.T) {
	t.Parallel()
	p := &program{}
	got := p.carapaceValues(&InputSchema{Enum: []any{"$HOME", "/tmp", "a ||| b", "x${HOME}y"}})
	if want := []string{"/tmp", "x$${HOME}y"}; !slices.Equal(got, want) {
		t.Errorf("enum values = %q, want %q", got, want)
	}
	described := &InputSchema{Enum: []any{map[string]any{"value": "v", "summary": "costs ${PRICE}\tnow"}}}
	if got := p.carapaceValues(described); len(got) != 1 || got[0] != "v\tcosts $${PRICE} now" {
		t.Errorf("described value = %q", got)
	}
	for msg, want := range map[string]string{
		"a (b))":       "$message(a (b)))",
		"pick one :)":  "$message(pick one :))",
		"needs ${TOK}": "$message(needs $${TOK})",
	} {
		if got := p.carapaceValues(&InputSchema{Complete: &CompletionHint{Message: msg}}); len(got) != 1 || got[0] != want {
			t.Errorf("message %q = %q, want %q", msg, got, want)
		}
	}
	if got := p.carapaceValues(&InputSchema{Complete: &CompletionHint{Message: "a ||| b"}}); len(got) != 0 {
		t.Errorf("message with a modifier separator = %q, want none", got)
	}
}
