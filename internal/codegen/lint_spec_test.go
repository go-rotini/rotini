package codegen

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLintRegistryCompleteness guards the lint_spec.go / lint_conf.go split: if a rule
// is accidentally dropped while relocating funcs, the count regresses.
func TestLintRegistryCompleteness(t *testing.T) {
	if got := len(specLints); got != 29 {
		t.Errorf("len(specLints) = %d, want 29 (a rule was dropped or added — update intentionally)", got)
	}
	if got := len(confLints); got != 6 {
		t.Errorf("len(confLints) = %d, want 6", got)
	}
}

// TestConstraintNumericFamilyMatchesRuntime guards the hand-copied constraintNumericFamily
// (codegen cannot import the runtime's unexported numericFamily) against silent drift by
// extracting the runtime's set from its source. The runtime is the module's root package,
// two levels up from internal/codegen.
func TestConstraintNumericFamilyMatchesRuntime(t *testing.T) {
	parser, err := os.ReadFile(filepath.Join("..", "..", "parser.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(parser)
	start := strings.Index(body, "var numericFamily = map[string]bool{")
	if start < 0 {
		t.Fatal("numericFamily literal not found in runtime parser.go")
	}
	block := body[start : start+strings.Index(body[start:], "}")]
	runtimeSet := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(\w+)":\s*true`).FindAllStringSubmatch(block, -1) {
		runtimeSet[m[1]] = true
	}
	if len(runtimeSet) == 0 {
		t.Fatal("extracted no keys from runtime numericFamily")
	}
	if !maps.Equal(runtimeSet, constraintNumericFamily) {
		t.Errorf("constraintNumericFamily drifted from runtime numericFamily:\n runtime=%v\n codegen=%v", runtimeSet, constraintNumericFamily)
	}
}

// ── schema-type rule ────────────────────────────────────────.

// specWithFlagType builds a minimal spec whose one flag carries the given type (and
// optional import), for exercising lintSchemaTypes in isolation.
func specWithFlagType(typ, imp string) *Spec {
	return &Spec{
		Version: "0.0.0",
		Command: Command{
			Name: "demo",
			Flags: []FlagInput{{
				Name:        "v",
				Identifiers: []string{"--v"},
				Schema:      &InputSchema{Type: typ, Import: imp},
			}},
		},
	}
}

// TestLintSchemaTypes_accepts pins the vocabulary an author may legitimately write.
// The JSON Schema cannot enum `type` (it spans Go names, JSON Schema names, rotini
// aliases and imported types), so this rule is the only thing standing between a typo
// and a gofmt failure over the generated file — which makes false positives expensive.
func TestLintSchemaTypes_accepts(t *testing.T) {
	cases := []struct{ typ, imp string }{
		{"string", ""}, {"int", ""}, {"bool", ""}, {"float64", ""},
		{"integer", ""}, {"number", ""}, {"boolean", ""}, // JSON Schema names
		{"array", ""}, {"object", ""},
		{"[]string", ""}, {"[]int", ""}, {"map[string]int", ""}, {"map[string]any", ""},
		{"count", ""},                                                  // the presence counter
		{"duration", ""}, {"time", ""}, {"date", ""}, {"datetime", ""}, // aliases that resolve to time.*
		{"any", ""}, {"*int", ""},
		{"Widget", ""},                                       // a same-package generated type
		{"uuid.UUID", "github.com/google/uuid"},              // imported, declared
		{"[]uuid.UUID", "github.com/google/uuid"},            // imported element
		{"map[string]uuid.UUID", "u github.com/google/uuid"}, // aliased import form
	}
	for _, tc := range cases {
		if problems := lintSchemaTypes(specWithFlagType(tc.typ, tc.imp)); len(problems) != 0 {
			t.Errorf("type %q (import %q) rejected: %v", tc.typ, tc.imp, problems)
		}
	}
}

// TestLintSchemaTypes_rejectsNonTypes covers the defect this rule exists for: a value
// that is not a Go type reached codegen intact and failed as a raw gofmt parse error
// over the whole generated file, naming neither the input nor its spec line.
func TestLintSchemaTypes_rejectsNonTypes(t *testing.T) {
	for _, typ := range []string{
		"not-a-type", // parses as an EXPRESSION (two subtractions) but is not a type
		"1",
		"a+b",
		"func()x",
		"[3]int", // fixed-size array: not an input shape
	} {
		problems := lintSchemaTypes(specWithFlagType(typ, ""))
		if len(problems) == 0 {
			t.Errorf("type %q accepted, want rejected", typ)
			continue
		}
		if got := problems[0].Error(); !strings.Contains(got, `flag "v"`) || !strings.Contains(got, typ) {
			t.Errorf("problem for %q must name the input and the value, got %q", typ, got)
		}
	}
}

// TestLintSchemaTypes_qualifiedNeedsImport pins the other half: rotini knows the import
// for its OWN aliases only, so a package-qualified type the author wrote must declare
// one or the generated file references a package it never imports.
func TestLintSchemaTypes_qualifiedNeedsImport(t *testing.T) {
	for _, typ := range []string{"time.Duration", "uuid.UUID", "[]uuid.UUID", "*uuid.UUID", "map[string]uuid.UUID"} {
		problems := lintSchemaTypes(specWithFlagType(typ, ""))
		if len(problems) == 0 {
			t.Errorf("qualified type %q with no import accepted, want rejected", typ)
			continue
		}
		if got := problems[0].Error(); !strings.Contains(got, "import") {
			t.Errorf("problem for %q should point at the missing import, got %q", typ, got)
		}
	}
	// The alias vocabulary resolves to time.* but carries its own import — an author
	// writing `duration` must NOT be asked for one.
	for _, alias := range []string{"duration", "time", "date", "datetime"} {
		if problems := lintSchemaTypes(specWithFlagType(alias, "")); len(problems) != 0 {
			t.Errorf("alias %q wrongly demanded an import: %v", alias, problems)
		}
	}
}
