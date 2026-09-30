package codegen

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

// TestLintRegistryCompleteness guards the lint_spec.go / lint_conf.go split: if a rule
// is accidentally dropped while relocating funcs, the count regresses.
func TestLintRegistryCompleteness(t *testing.T) {
	if got := len(specLints); got != 35 {
		t.Errorf("len(specLints) = %d, want 35 (a rule was dropped or added — update intentionally)", got)
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

// TestDocumentedLintCountsMatchTheRegistry keeps the numbers in the prose true.
//
// "29 lint rules" was written into README.md, the home page, the CLI page and the
// specification page, and stayed there while four rules were added — four separate claims,
// all wrong, none checkable. A count in prose is a fact about the code, so the code checks it.
//
// It scans EVERY published document rather than a list someone has to remember to extend,
// because the list is the thing that went stale the first time.
func TestDocumentedLintCountsMatchTheRegistry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Normalized before matching — emphasis stripped, whitespace collapsed — so the pattern
	// does not depend on where someone put the asterisks. See the same note in
	// TestDocumentedSeedSizeMatchesReality, where anticipating one phrasing let a stale
	// claim through.
	countRe := regexp.MustCompile(`(\d+) (?:rotini )?(?:spec )?lint rules`)
	normalize := func(body []byte) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(string(body), "*", "")), " ")
	}

	checked := 0
	for _, path := range publishedMarkdown(t, root) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, m := range countRe.FindAllStringSubmatch(normalize(body), -1) {
			got, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			checked++
			if got != len(specLints) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s says %d lint rules; the registry has %d", rel, got, len(specLints))
			}
		}
	}
	if checked == 0 {
		t.Error("no document quotes a lint-rule count — if that is deliberate, delete this test")
	}
}

// publishedMarkdown lists the documents a reader actually sees: the repository's own top-level
// markdown and every page of the docs site. Generated pages are skipped — they are rendered
// from the schemas and cannot drift by hand.
func publishedMarkdown(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, name := range []string{"README.md", "COMPATIBILITY.md", "UPGRADING.md", "CONTRIBUTING.md"} {
		out = append(out, filepath.Join(root, name))
	}
	content := filepath.Join(root, "docs", "content")
	err := filepath.WalkDir(content, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") || d.Name() == "reference.md" {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", content, err)
	}
	return out
}

// ── lintDefaultConstraints ───────────────────────────────────────────────────
//
// The hole this rule closes let a spec validate, generate, compile, and then fail on EVERY
// invocation that took the default — with an error naming a flag the user never typed:
//
//	schema: { type: string, enum: [fast, slow], default: turbo }
//	$ mycli
//	Error: invalid value "turbo" for --mode (one of: fast, slow)
//
// Six constraint families were uncaught, six for six. The table below is that measurement,
// inverted into a guard.

func defaultConstraintSpec(t *testing.T, schema string) *Spec {
	t.Helper()
	body := "version: 0.0.0\ncommand:\n  name: t\n  summary: s\n  flags:\n" +
		"    - name: f\n      identifiers: [--f]\n      summary: s\n      schema: { " + schema + " }\n"
	spec, err := decodeData[Spec](formatYAML, []byte(body), "test.yaml")
	if err != nil {
		t.Fatalf("decode %q: %v", schema, err)
	}
	return spec
}

func TestLintDefaultConstraints_rejectsADefaultItsOwnConstraintsForbid(t *testing.T) {
	for _, schema := range []string{
		`type: string, enum: [fast, slow], default: turbo`,
		`type: int, minimum: 10, default: 3`,
		`type: int, maximum: 5, default: 99`,
		`type: int, exclusiveMinimum: 0, default: 0`,
		`type: int, exclusiveMaximum: 10, default: 10`,
		`type: int, multipleOf: 5, default: 7`,
		`type: string, minLength: 5, default: ab`,
		`type: string, maxLength: 2, default: abcdef`,
		`type: string, pattern: '^v[0-9]+$', default: nope`,
		`type: '[]string', minItems: 2, default: [x]`,
		`type: '[]string', maxItems: 1, default: [x, y]`,
		// Per-value constraints apply element-wise to a multi-value default.
		`type: '[]string', minLength: 3, default: [ok, no]`,
		`type: '[]int', minimum: 10, default: [50, 3]`,
		`type: '[]string', enum: [a, b], default: [a, zz]`,
	} {
		t.Run(schema, func(t *testing.T) {
			if problems := lintDefaultConstraints(defaultConstraintSpec(t, schema)); len(problems) == 0 {
				t.Error("accepted a default its own constraints forbid — the CLI would fail on every " +
					"run that took it, blaming a flag the user never typed")
			}
		})
	}
}

// TestLintDefaultConstraints_acceptsWhatWorks is the false-positive half. A rule that rejects
// valid specs is worse than the hole it closes, because it blocks a build that would have run.
func TestLintDefaultConstraints_acceptsWhatWorks(t *testing.T) {
	for _, schema := range []string{
		`type: string, enum: [fast, slow], default: fast`,
		`type: int, minimum: 10, default: 10`, // inclusive
		`type: int, maximum: 5, default: 5`,
		`type: int, exclusiveMinimum: 0, default: 1`,
		`type: int, multipleOf: 5, default: 20`,
		`type: float64, multipleOf: 0.1, default: 1.2`, // float tolerance
		`type: string, minLength: 2, default: ab`,
		`type: string, pattern: '^v[0-9]+$', default: v2`,
		`type: '[]string', minItems: 1, default: [x]`,
		`type: '[]string', minLength: 1, default: [x, y]`,
		`type: string, default: anything`, // no constraints at all
		`type: int, minimum: 1`,           // constraint, no default
		`type: string, enum: [a, b]`,      // enum, no default
		`type: duration, default: 5s`,     // non-numeric, non-enum
		`type: bool, default: true`,       //
		`type: 'map[string]string', minItems: 1, default: { a: b }`,
	} {
		t.Run(schema, func(t *testing.T) {
			if problems := lintDefaultConstraints(defaultConstraintSpec(t, schema)); len(problems) > 0 {
				t.Errorf("rejected a working spec: %v", problems)
			}
		})
	}
}

// TestLintDefaultConstraints_reportsEveryViolation: the schema validator reports all its errors,
// and this rule must too. Reporting only the first means a spec with three bad defaults takes
// three round trips to fix.
func TestLintDefaultConstraints_reportsEveryViolation(t *testing.T) {
	spec, err := decodeData[Spec](formatYAML, []byte(
		"version: 0.0.0\ncommand:\n  name: t\n  summary: s\n  flags:\n"+
			"    - name: a\n      identifiers: [--a]\n      summary: s\n      schema: { type: int, minimum: 10, default: 1 }\n"+
			"    - name: b\n      identifiers: [--b]\n      summary: s\n      schema: { type: int, minimum: 10, default: 2 }\n"+
			"    - name: c\n      identifiers: [--c]\n      summary: s\n      schema: { type: int, minimum: 10, default: 3 }\n"),
		"test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := lintDefaultConstraints(spec); len(got) != 3 {
		t.Errorf("reported %d violations, want 3 — one per bad default", len(got))
	}
}

// TestDefaultConstraintsAgreeWithRuntime is the anti-drift guard, and the reason this rule
// checks defaultString/defaultList output rather than the raw YAML value.
//
// codegen cannot call the runtime's checkNumericBounds / checkStringBounds / checkItemCount —
// they are unexported — so the checks in defaultViolation are a hand-written mirror, exactly as
// constraintNumericFamily mirrors numericFamily. A copy that is never compared drifts, so this
// runs the same defaults through BOTH: the lint rule, and a real parse of a real Definition with
// NO arguments, which is precisely when a default gets validated.
//
// Agreement is the assertion. Where they disagree, one of them is wrong about what a user's CLI
// will actually do.
func TestDefaultConstraintsAgreeWithRuntime(t *testing.T) {
	type stringFlags struct {
		F string `rotini:"f"`
	}
	type stringInputs struct {
		T struct{ Flags stringFlags }
	}
	type intFlags struct {
		F int `rotini:"f"`
	}
	type intInputs struct {
		T struct{ Flags intFlags }
	}

	cases := []struct {
		schema string
		def    rotini.FlagDef
		out    func() any
	}{
		{`type: string, enum: [fast, slow], default: turbo`,
			rotini.FlagDef{Type: "string", Default: "turbo", Enum: []string{"fast", "slow"}},
			func() any { return &stringInputs{} }},
		{`type: string, enum: [fast, slow], default: fast`,
			rotini.FlagDef{Type: "string", Default: "fast", Enum: []string{"fast", "slow"}},
			func() any { return &stringInputs{} }},
		{`type: int, minimum: 10, default: 3`,
			rotini.FlagDef{Type: "int", Default: "3",
				Constraints: rotini.Constraints{Minimum: rotini.Ptr[float64](10)}},
			func() any { return &intInputs{} }},
		{`type: int, minimum: 10, default: 10`,
			rotini.FlagDef{Type: "int", Default: "10",
				Constraints: rotini.Constraints{Minimum: rotini.Ptr[float64](10)}},
			func() any { return &intInputs{} }},
		{`type: int, maximum: 5, default: 99`,
			rotini.FlagDef{Type: "int", Default: "99",
				Constraints: rotini.Constraints{Maximum: rotini.Ptr[float64](5)}},
			func() any { return &intInputs{} }},
		{`type: int, multipleOf: 5, default: 7`,
			rotini.FlagDef{Type: "int", Default: "7",
				Constraints: rotini.Constraints{MultipleOf: rotini.Ptr[float64](5)}},
			func() any { return &intInputs{} }},
		{`type: string, minLength: 5, default: ab`,
			rotini.FlagDef{Type: "string", Default: "ab",
				Constraints: rotini.Constraints{MinLength: 5}},
			func() any { return &stringInputs{} }},
		{`type: string, maxLength: 2, default: abcdef`,
			rotini.FlagDef{Type: "string", Default: "abcdef",
				Constraints: rotini.Constraints{MaxLength: 2}},
			func() any { return &stringInputs{} }},
		{`type: string, pattern: '^v[0-9]+$', default: nope`,
			rotini.FlagDef{Type: "string", Default: "nope",
				Constraints: rotini.Constraints{Pattern: "^v[0-9]+$"}},
			func() any { return &stringInputs{} }},
		{`type: string, pattern: '^v[0-9]+$', default: v2`,
			rotini.FlagDef{Type: "string", Default: "v2",
				Constraints: rotini.Constraints{Pattern: "^v[0-9]+$"}},
			func() any { return &stringInputs{} }},
	}

	for _, c := range cases {
		t.Run(c.schema, func(t *testing.T) {
			lintRejects := len(lintDefaultConstraints(defaultConstraintSpec(t, c.schema))) > 0

			fd := c.def
			fd.Name, fd.Identifiers = "f", []string{"--f"}
			def := rotini.Definition{Name: "t", Handler: "App", Flags: []rotini.FlagDef{fd}}

			// No argv at all: the default is the only value there is to validate, which is
			// exactly the failure this rule exists to catch before a user ever sees it.
			rtx := rotini.NewContextFor(def, nil)
			runtimeErr := rotini.NewParser().Parse(rtx, c.out())
			runtimeRejects := runtimeErr != nil

			if lintRejects != runtimeRejects {
				t.Errorf("lint and runtime disagree: lint rejects=%v, runtime rejects=%v (%v)\n"+
					"One of them is wrong about what a user's CLI will do.",
					lintRejects, runtimeRejects, runtimeErr)
			}
		})
	}
}
