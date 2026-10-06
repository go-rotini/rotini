package codegen

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
)

// TestLintRegistryCompleteness pins the number of registered spec and conf rules so a
// dropped rule is noticed.
func TestLintRegistryCompleteness(t *testing.T) {
	if got := len(specLints); got != 45 {
		t.Errorf("len(specLints) = %d, want 45 (a rule was dropped or added — update intentionally)", got)
	}
	if got := len(confLints); got != 10 {
		t.Errorf("len(confLints) = %d, want 10", got)
	}
}

// TestConstraintNumericFamilyMatchesRuntime pins constraintNumericFamily to the runtime's
// unexported numericFamily, read from the root package's parser.go source.
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

// TestLintSchemaTypes_accepts pins the type vocabulary lintSchemaTypes must accept.
func TestLintSchemaTypes_accepts(t *testing.T) {
	cases := []struct{ typ, imp string }{
		{"string", ""}, {"int", ""}, {"bool", ""}, {"float64", ""},
		{"integer", ""}, {"number", ""}, {"boolean", ""}, // JSON Schema names
		{"array", ""}, {"object", ""},
		{"[]string", ""}, {"[]int", ""}, {"map[string]int", ""}, {"map[string]any", ""},
		{"count", ""},                                                  // the presence counter
		{"duration", ""}, {"time", ""}, {"date", ""}, {"datetime", ""}, // aliases that resolve to time.*
		{"url", ""}, {"email", ""}, {"timezone", ""}, {"mac", ""}, // value types
		{"ip", ""}, {"cidr", ""}, {"hostport", ""},
		{"bytesize", ""}, {"hexbytes", ""}, {"base64bytes", ""},
		{"[]bytesize", ""}, {"map[string]duration", ""}, {"[]existingfile", ""}, // aliases inside Go spellings
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

// TestLintSchemaTypes_rejectsNonTypes pins that values which are not Go types are rejected.
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

// TestLintSchemaTypes_rejectsTypos pins that an unknown lowercase type name is rejected with
// a suggestion.
func TestLintSchemaTypes_rejectsTypos(t *testing.T) {
	for typ, suggest := range map[string]string{
		"strin":             "string",
		"[]strin":           "string",
		"map[string]nubmer": "number",
		"*itn":              "int",
		"bytsize":           "bytesize",
	} {
		problems := lintSchemaTypes(specWithFlagType(typ, ""))
		if len(problems) == 0 {
			t.Errorf("type %q accepted, want rejected", typ)
			continue
		}
		if got := problems[0].Error(); !strings.Contains(got, fmt.Sprintf("did you mean %q", suggest)) {
			t.Errorf("problem for %q should suggest %q, got %q", typ, suggest, got)
		}
	}
}

// TestLintSchemaTypes_qualifiedNeedsImport pins that an author-written qualified type needs
// an `import`, while rotini aliases resolving to qualified types do not.
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
	for _, alias := range []string{"duration", "time", "date", "datetime"} {
		if problems := lintSchemaTypes(specWithFlagType(alias, "")); len(problems) != 0 {
			t.Errorf("alias %q wrongly demanded an import: %v", alias, problems)
		}
	}
}

// TestDocsQuoteNoLintRuleCount pins that no published document quotes a lint-rule count,
// which would go stale as rules are added.
func TestDocsQuoteNoLintRuleCount(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Strip emphasis and collapse whitespace so formatting cannot hide a count.
	countRe := regexp.MustCompile(`\b\d+ (?:rotini )?(?:spec |conf )?lint rules|plus \d+ for the conf`)
	normalize := func(body []byte) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(string(body), "*", "")), " ")
	}

	for _, path := range publishedMarkdown(t, root) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, m := range countRe.FindAllString(normalize(body), -1) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s quotes a lint-rule count (%q); say \"rotini's lint rules\" instead", rel, m)
		}
	}
}

// publishedMarkdown lists the repository's top-level markdown and every docs-site page,
// skipping the generated reference pages.
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

// defaultConstraintSpec decodes a one-flag spec whose flag has the given inline schema.
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

// TestLintDefaultConstraints_acceptsWhatWorks pins that defaults satisfying their constraints
// are accepted.
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
		`type: bool, default: true`,
		`type: 'map[string]string', minItems: 1, default: { a: b }`,
	} {
		t.Run(schema, func(t *testing.T) {
			if problems := lintDefaultConstraints(defaultConstraintSpec(t, schema)); len(problems) > 0 {
				t.Errorf("rejected a working spec: %v", problems)
			}
		})
	}
}

// TestLintDefaultConstraints_reportsEveryViolation pins one problem per bad default, not just
// the first.
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

// TestDefaultConstraintsAgreeWithRuntime pins defaultViolation, a hand-written mirror of the
// runtime's unexported constraint checks, to the runtime: each default must be rejected by the
// lint exactly when a no-argument parse rejects it.
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

			// With no argv, the default is the only value validated.
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

// ── lintItemConstraints ──────────────────────────────────────────────────────

func TestLintItemConstraints(t *testing.T) {
	cases := []struct {
		name, schema string
		wantProblem  bool
	}{
		{"conflicting enum", `type: array, enum: [a, b], items: { type: string, enum: [x, y] }`, true},
		{"conflicting minimum", `type: array, minimum: 5, items: { type: int, minimum: 9 }`, true},
		{"conflicting pattern", `type: array, pattern: '^a', items: { type: string, pattern: '^b' }`, true},
		{"item count on items", `type: array, items: { type: string, minItems: 2 }`, true},
		{"identical on both", `type: array, enum: [a, b], items: { type: string, enum: [a, b] }`, false},
		{"items only", `type: array, items: { type: string, enum: [a, b] }`, false},
		{"list only", `type: array, enum: [a, b], items: { type: string }`, false},
		{"item count on the list", `type: array, minItems: 2, items: { type: string }`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := len(lintItemConstraints(defaultConstraintSpec(t, c.schema))) > 0
			if got != c.wantProblem {
				t.Errorf("problem reported = %v, want %v", got, c.wantProblem)
			}
		})
	}
}

// ── lintRequiredArgumentOrder ────────────────────────────────────────────────

func argumentsSpec(t *testing.T, args string) *Spec {
	t.Helper()
	spec, err := decodeData[Spec](formatYAML, []byte(
		"version: 0.0.0\ncommand:\n  name: t\n  summary: s\n  arguments:\n"+args), "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestLintRequiredArgumentOrder(t *testing.T) {
	cases := []struct {
		name, args  string
		wantProblem bool
	}{
		{"required after optional",
			"    - name: a\n      schema: { type: string }\n    - name: b\n      schema: { type: string, required: true }\n", true},
		{"variadic needing a value after optional",
			"    - name: a\n      schema: { type: string }\n    - name: b\n      schema: { type: '[]string', minItems: 1 }\n", true},
		{"required after optional-with-a-default",
			"    - name: a\n      schema: { type: string, default: x }\n    - name: b\n      schema: { type: string, required: true }\n", true},
		{"required then optional",
			"    - name: a\n      schema: { type: string, required: true }\n    - name: b\n      schema: { type: string }\n", false},
		{"optional variadic last",
			"    - name: a\n      schema: { type: string }\n    - name: b\n      schema: { type: '[]string' }\n", false},
		{"all required",
			"    - name: a\n      schema: { type: string, required: true }\n    - name: b\n      schema: { type: string, required: true }\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(lintRequiredArgumentOrder(argumentsSpec(t, c.args))) > 0; got != c.wantProblem {
				t.Errorf("problem reported = %v, want %v", got, c.wantProblem)
			}
		})
	}
}

// TestRequiredArgumentOrderMatchesTheParser pins the parser behavior lintRequiredArgumentOrder
// relies on: a single value fills the leading optional positional and the required one is
// reported missing.
func TestRequiredArgumentOrderMatchesTheParser(t *testing.T) {
	type args struct {
		Optional  string `rotini:"optional"`
		Mandatory string `rotini:"mandatory"`
	}
	type inputs struct {
		T struct{ Arguments args }
	}
	def := rotini.Definition{Name: "t", Handler: "App", Arguments: []rotini.ArgDef{
		{Name: "optional", Type: "string"},
		{Name: "mandatory", Type: "string", Required: true},
	}}

	var got inputs
	err := rotini.NewParser().Parse(rotini.NewContextFor(def, []string{"x"}), &got)
	if got.T.Arguments.Optional != "x" || err == nil {
		t.Errorf("one value: optional=%q err=%v — want it in the optional argument and a missing-input error; "+
			"the parser no longer fills positionals strictly in order, so lintRequiredArgumentOrder is wrong",
			got.T.Arguments.Optional, err)
	}
}

// TestLintPatternMessage_needsAPattern pins that a pattern_message without a pattern is
// rejected wherever it sits in a schema tree.
func TestLintPatternMessage_needsAPattern(t *testing.T) {
	msg := BaseSchema{Type: "string", PatternMessage: "must be lowercase"}
	spec := &Spec{Command: Command{
		Name: "app",
		Schemas: map[string]Schema{
			"DB": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{"host": {BaseSchema: msg}}}},
		},
		Flags: []FlagInput{
			{Name: "ok", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "string", Pattern: "^a", PatternMessage: "must start with a"}}},
			{Name: "alone", Schema: &InputSchema{BaseSchema: msg}},
			{Name: "tags", Schema: &InputSchema{BaseSchema: BaseSchema{Type: "array", Items: &Schema{BaseSchema: msg}}}},
		},
	}}
	var got []string
	for _, p := range lintPatternCompiles(spec) {
		got = append(got, p.Error())
	}
	if len(got) != 3 {
		t.Fatalf("problems = %q, want 3 (schema DB's host, flag alone, flag tags' items)", got)
	}
	for _, want := range []string{`schema "DB"`, `flag "alone"`, `flag "tags"`} {
		if !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) && strings.Contains(s, "no `pattern` beside it") }) {
			t.Errorf("no problem naming %s in %q", want, got)
		}
	}
}

// TestLintFeatureDirs_defaultCmdPackage pins that, with no cmd package declared, embed_dir
// must resolve under internal/cmd/<some name>.
func TestLintFeatureDirs_defaultCmdPackage(t *testing.T) {
	conf := func(dir string) *Conf {
		return &Conf{Generate: &GenerateConfig{Features: []Feature{{Type: "help", Enabled: true, Embed: true, EmbedDir: dir}}}}
	}
	if got := lintFeatureDirs(conf("docs/renders")); len(got) != 1 || !strings.Contains(got[0].Error(), "internal/cmd/<root name>") {
		t.Errorf("embed_dir outside internal/cmd: problems = %v, want one naming the default cmd package", got)
	}
	for _, ok := range []string{"internal/cmd/demo/renders", "internal/cmd/demo"} {
		if got := lintFeatureDirs(conf(ok)); len(got) != 0 {
			t.Errorf("embed_dir %q: problems = %v, want none", ok, got)
		}
	}
	if got := lintFeatureDirs(conf("internal/cmd/../elsewhere")); len(got) != 1 {
		t.Errorf("embed_dir climbing out of internal/cmd: problems = %v, want one", got)
	}
}

// TestLintSchemaFiles pins that schema file paths must be module-root-relative.
func TestLintSchemaFiles(t *testing.T) {
	for file, want := range map[string]int{
		".rotini-schema.spec.json": 0, "cmd/app/.rotini-schema.spec.json": 0,
		"/etc/schema.json": 1, "../schema.json": 1, "a/../../schema.json": 1,
	} {
		conf := &Conf{Generate: &GenerateConfig{Schemas: &SchemasConfig{Spec: &SchemaConfig{File: file}}}}
		if got := lintSchemaFiles(conf); len(got) != want {
			t.Errorf("schemas.spec.file %q: problems = %v, want %d", file, got, want)
		}
	}
}

// TestLocateNearest_fallsBackToAncestor pins that an unplaceable pointer resolves to its
// nearest placeable ancestor.
func TestLocateNearest_fallsBackToAncestor(t *testing.T) {
	locate := func(ptr string) (int, int, bool) {
		if ptr == "/command/flags/2" {
			return 9, 7, true
		}
		return 0, 0, false
	}
	if line, col, ok := locateNearest(locate, "/command/flags/2/schema/default"); !ok || line != 9 || col != 7 {
		t.Errorf("locateNearest = %d:%d %v, want 9:7 from the enclosing flag", line, col, ok)
	}
	if _, _, ok := locateNearest(locate, "/generate/features/0"); ok {
		t.Error("an unplaceable pointer with no placeable ancestor must report no position")
	}
}

// TestDocGoNamesEveryInputType pins that doc.go's "Input values" section names every type in
// knownTypeNames.
func TestDocGoNamesEveryInputType(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "doc.go"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(body)
	start := strings.Index(doc, "// # Input values")
	if start < 0 {
		t.Fatal(`doc.go has no "# Input values" section`)
	}
	section := doc[start:]
	if end := strings.Index(section[1:], "\n// # "); end >= 0 {
		section = section[:end+1]
	}
	for _, name := range knownTypeNames() {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(section) {
			t.Errorf("doc.go's Input values section does not name the type %q, which a spec may write", name)
		}
	}
}
