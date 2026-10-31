package codegen

import (
	"go/ast"
	"go/parser"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal/testmod"
)

const constantsSpec = `version: 0.0.0
command:
  name: acme
  summary: ship things
  env_prefix: ACME
  config_files:
    - name: user
      path: acme.yaml
      profiles: { under: profiles, select: profile, default: dev }
  schemas:
    Status:
      type: string
      enum: [open, done]
    Item:
      type: object
      properties:
        kind: { type: string, enum: [a-b, c] }
        state: { $ref: '#/schemas/Status' }
  flag_sets:
    Output:
      flags:
        - name: format
          identifiers: [--format]
          schema: { type: string, enum: [json, text] }
  flags:
    - name: profile
      identifiers: [--profile]
      cascading: true
      schema: { type: string, enum: [dev, prod] }
  env:
    - name: token
      summary: the API token
      deprecated: use --token-file
    - name: gh
      schema: { variable: GITHUB_TOKEN }
  config:
    - name: port
      schema: { key: server.port, type: int }
  commands:
    - name: list
      summary: list things
      use: [Output]
      output: { $ref: '#/schemas/Item' }
      flags:
        - name: sort
          summary: the order
          identifiers: [--sort]
          schema:
            type: string
            enum:
              - { value: name, summary: by name }
              - { value: age, deprecated: use name }
        - name: tag
          identifiers: [--tag]
          schema: { type: '[]string', items: { type: string, enum: [x] } }
        - name: level
          identifiers: [--level]
          schema: { type: int, enum: ["1", "2"] }
`

const constantsConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`

// generateWithNotices generates spec into a fresh module, returning the module and every
// notice generate reported.
func generateWithNotices(t *testing.T, spec, conf string, extra map[string]string) (string, []string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+testmod.Quote(filepath.ToSlash(repoRoot))+"\n"+testmod.Directives(repoRoot))
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	for name, body := range extra {
		writeTestFile(t, dir, name, body)
	}
	t.Chdir(dir)
	var notices []string
	collect := func(errs []error) {
		for _, e := range errs {
			notices = append(notices, e.Error())
		}
	}
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			notices = append(notices, e.Error())
		}
	}, collect); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return dir, notices
}

// TestConstants_generated pins the constants and doc comments the cmd file carries: one per
// string enum value (a named schema's typed by its type, a flag set's under the set's name, an
// output field's under its type), the names of files, keys, profiles and variables, the
// input's summary and deprecation on its field, and the root's summary as the package doc.
// A non-string enum gets none.
func TestConstants_generated(t *testing.T) {
	dir, _ := generateWithNotices(t, constantsSpec, constantsConf, nil)
	src := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	for _, want := range []string{
		"// Package acme implements the acme command: ship things.\npackage acme",
		"\t// by name\n\tAcmeListSortName = \"name\"\n\t// Deprecated: use name\n\tAcmeListSortAge = \"age\"",
		"AcmeListTagX = \"x\"",
		"AcmeOutputFormatJSON = \"json\"",
		"StatusOpen Status = \"open\"",
		"ItemKindAB = \"a-b\"",
		"AcmeProfileDev  = \"dev\"",
		"AcmeConfigFileUser = \"user\"",
		"AcmeConfigKeyServerPort = \"server.port\"",
		"AcmeEnvToken       = \"ACME_TOKEN\"",
		"AcmeEnvGithubToken = \"GITHUB_TOKEN\"",
		"\t// the API token\n\t//\n\t// Deprecated: use --token-file\n\tToken string",
		"\t// the order\n\tSort ",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the cmd file lacks %q", want)
		}
	}
	for _, unwanted := range []string{"AcmeListLevel1", "StatusState"} {
		if strings.Contains(src, unwanted) {
			t.Errorf("the cmd file declares %s", unwanted)
		}
	}
	if strings.Count(src, "AcmeProfileDev ") != 1 {
		t.Error("the profile flag's enum constant and the profile constant are not one declaration")
	}
}

// TestConstants_models pins the constants with a models package: the enum values sit beside
// the types and are re-exported in the cmd package; the name constants are the cmd file's.
func TestConstants_models(t *testing.T) {
	conf := constantsConf + `    - type: models
      file: internal/models/zz_models.go
      package: models
`
	dir, _ := generateWithNotices(t, constantsSpec, conf, nil)
	models := readEmitted(t, dir, "internal/models/zz_models.go")
	cmd := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	for _, want := range []string{"StatusOpen Status = \"open\"", "AcmeListSortName = \"name\"", "ItemKindAB = \"a-b\""} {
		if !strings.Contains(models, want) {
			t.Errorf("the models file lacks %q", want)
		}
	}
	for _, want := range []string{"= models.StatusOpen\n", "= models.ItemKindAB\n", "AcmeConfigFileUser = \"user\""} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the cmd file lacks %q", want)
		}
	}
	if strings.Contains(models, "AcmeConfigFileUser") {
		t.Error("the models file declares a name constant")
	}
}

// TestConstants_authorOwned pins that a constant a file of the author's already declares is
// left out with a notice, and that an author's package comment keeps the generated one away.
func TestConstants_authorOwned(t *testing.T) {
	dir, notices := generateWithNotices(t, constantsSpec, constantsConf, map[string]string{
		"internal/cmd/acme/doc.go":    "// Package acme is mine.\npackage acme\n\nconst StatusOpen Status = \"open\"\n",
		"internal/cmd/acme/x_test.go": "// Not a package doc that counts.\npackage acme\n",
	})
	src := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if strings.Contains(src, "StatusOpen Status") || !strings.Contains(src, "StatusDone Status") {
		t.Errorf("the author's StatusOpen wasn't left to them:\n%s", src)
	}
	if strings.Contains(src, "// Package acme") {
		t.Error("the cmd file has a package comment beside the author's")
	}
	want := `internal/cmd/acme/doc.go declares StatusOpen, the name of the generated constant for the enum value "open" of the schema Status; generate leaves the constant out`
	if !strings.Contains(strings.Join(notices, "\n"), want) {
		t.Errorf("notices %q lack %q", notices, want)
	}
}

// TestConstants_clashes pins the clash rules: two spellings that make one name lose both
// constants, and a name another generated name holds loses the constant, each with a lint
// warning and a generate notice, while the spec stays valid and generates.
func TestConstants_clashes(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: acme
  flags:
    - name: mode
      identifiers: [--mode]
      schema: { type: string, enum: [dry-run, dry_run, inputs] }
`
	groups := mergeConstGroups(inputEnumGroups(decodeSpecYAML(t, spec)))
	kept, clashes := withoutClashes(groups, map[string]string{"AcmeModeInputs": "command acme's Inputs type"})
	if len(clashes) != 2 {
		t.Fatalf("clashes = %+v, want 2", clashes)
	}
	if !strings.Contains(clashes[0].msg, `the enum value "dry_run" of the mode flag of acme and the enum value "dry-run" of the mode flag of acme would both get the constant AcmeModeDryRun`) {
		t.Errorf("clash message: %s", clashes[0].msg)
	}
	if !strings.Contains(clashes[1].msg, "which command acme's Inputs type already is") {
		t.Errorf("taken message: %s", clashes[1].msg)
	}
	if len(kept) != 0 {
		t.Errorf("kept %+v, want none", kept)
	}
	_, notices := generateWithNotices(t, spec, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`, nil)
	if !strings.Contains(strings.Join(notices, "\n"), "would both get the constant AcmeModeDryRun") {
		t.Errorf("generate gave no notice: %q", notices)
	}
}

// parseCallForTest parses src as one call expression.
func parseCallForTest(t *testing.T, src string) *ast.CallExpr {
	t.Helper()
	e, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatal(err)
	}
	return e.(*ast.CallExpr)
}

// TestConfigNamesCheck pins the config-call audit's judgement: an undeclared file or key, and
// a profile no selector's enum lists, while declared names and generated constants pass.
func TestConfigNamesCheck(t *testing.T) {
	n := configNames{
		keys:     map[string][]string{"user": {"server.port"}},
		profiles: []string{"dev", "prod"},
		consts:   map[string]string{"AcmeConfigFileUser": "user"},
	}
	for _, tt := range []struct {
		src, want string
	}{
		{`rotini.SetConfigValue(rtx, "usr", "k", "v")`, `names the config file "usr", which the spec doesn't declare (it declares "user")`},
		{`rotini.SetConfigValue(rtx, AcmeConfigFileUser, "server.prot", "v")`, `names the key "server.prot", which no input reads from config file "user"`},
		{`rotini.UnsetConfigValue(rtx, "user", "server.port")`, ""},
		{`rotini.ConfigFilePath(rtx, "nope")`, `names the config file "nope"`},
		{`rotini.ConfigProfiles(rtx, name)`, ""},
		{`rotini.ToProfile("stage")`, `names the profile "stage", which no profile selector's enum lists ("dev", "prod")`},
		{`rotini.ToProfile("prod")`, ""},
	} {
		call := parseCallForTest(t, tt.src)
		sel := call.Fun.(*ast.SelectorExpr)
		got := n.check(sel.Sel.Name, call.Args)
		if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.src, got, tt.want)
		}
	}
	if got := (configNames{keys: n.keys}).check("ToProfile", parseCallForTest(t, `rotini.ToProfile("any")`).Args); got != "" {
		t.Errorf("profiles not all declared, yet: %q", got)
	}
}

// TestFieldDoc pins an input field's doc comment lines.
func TestFieldDoc(t *testing.T) {
	for _, tt := range []struct {
		summary, deprecated string
		want                []string
	}{
		{"", "", nil},
		{"the order", "", []string{"// the order"}},
		{"", "gone", []string{"// Deprecated: gone"}},
		{"two\nlines ", "use x\nsoon", []string{"// two", "// lines", "//", "// Deprecated: use x", "// soon"}},
		{"a\x00b\rc\ufeffd", "", []string{"// a b c d"}},
	} {
		if got := fieldDoc(tt.summary, tt.deprecated); strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("fieldDoc(%q, %q) = %q, want %q", tt.summary, tt.deprecated, got, tt.want)
		}
	}
}
