package internal

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateHelpGolden rewrites the committed golden files under internal/testdata/help
// instead of comparing against them. Run: go test ./internal -run TestHelpGolden
// -update-help-golden — then review the diff before committing.
var updateHelpGolden = flag.Bool("update-help-golden", false, "rewrite internal/testdata/help golden files")

const goldenSpecSchema = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"

// helpGoldenDir returns the absolute path to internal/testdata/help. It must be
// called before genHelp (which t.Chdir's into a temp module).
func helpGoldenDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "testdata", "help")
}

// assertHelpGolden compares got against testdata/help/<name>, or rewrites it under
// -update-help-golden.
func assertHelpGolden(t *testing.T, goldenDir, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateHelpGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n(create/update with: go test ./internal -run TestHelpGolden -update-help-golden)", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %q mismatch:\n--- got (%d bytes) ---\n%s\n<<<EOF\n--- want (%d bytes) ---\n%s\n<<<EOF",
			name, len(got), got, len(want), string(want))
	}
}

// genHelp generates spec (with help enabled) into a fresh temp module and returns
// the absolute help dir. When preTmpl is non-empty it is written as the
// help.txt.tmpl before generation, simulating an end-user-edited template.
func genHelp(t *testing.T, spec, preTmpl string) string {
	t.Helper()
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)
	if preTmpl != "" {
		writeTestFile(t, filepath.Join(tmp, "rtg", "help", "help.txt.tmpl"), preTmpl)
	}
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return filepath.Join(tmp, "rtg", "help")
}

// helpGoldenGeneratedSpec exercises the full generated-help surface: root header/
// description/usage-override/custom-heading/examples/footer; a rich sub-command with
// required/optional/variadic args and flags with type/default/enum/required/
// deprecated/hidden; a deprecated command; and a hidden command.
const helpGoldenGeneratedSpec = goldenSpecSchema +
	`command:
  name: app
  summary: the app
  description: |
    A demo application.

    A second paragraph.
  usage: app <command> [flags]
  header: '===== APP ====='
  footer: Use "app help <command>" for details.
  headings:
    commands: Subcommands
  examples:
    - app build ./src
  commands:
    - name: build
      aliases: [b, bld]
      summary: build things
      description: Build the project from sources.
      examples:
        - app build ./src
        - app build ./src --force
      inputs:
        arguments:
          - name: target
            summary: what to build
            schema: { type: string, required: true }
          - name: extra
            summary: extra targets
            schema: { type: array }
        flags:
          - name: output
            summary: output directory
            identifiers: [-o, --output]
            schema: { type: string, default: ./dist }
          - name: format
            summary: archive format
            identifiers: [--format]
            schema: { type: string, enum: [tar, zip], default: tar }
          - name: force
            summary: overwrite existing output
            identifiers: [-f, --force]
            schema: { type: bool, required: true }
          - name: legacy
            summary: legacy flag
            identifiers: [--legacy]
            deprecated: use --modern
            schema: { type: bool }
          - name: secret
            summary: hidden flag
            identifiers: [--secret]
            hidden: true
            schema: { type: bool }
    - name: oldcmd
      summary: an old command
      deprecated: use build instead
    - name: secretcmd
      summary: a hidden command
      hidden: true
`

// TestHelpGolden_Generated locks the default-template rendering across every
// structured-help feature against committed golden files.
func TestHelpGolden_Generated(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenGeneratedSpec, "")
	for _, f := range []string{"app.txt", "app_build.txt", "app_oldcmd.txt", "app_secretcmd.txt"} {
		assertHelpGolden(t, goldenDir, "generated_"+f, readFileString(t, filepath.Join(helpDir, f)))
	}
}

const helpGoldenCustomSpec = goldenSpecSchema +
	`command:
  name: app
  summary: my app
  footer: bye now
  commands:
    - name: run
      summary: run it
      inputs:
        flags:
          - name: verbose
            summary: be loud
            identifiers: [-v, --verbose]
            schema: { type: bool }
`

// helpGoldenCustomTmpl is a deliberately non-default template: it reorders/omits
// sections, upper-cases the summary, draws a rule, and uses a custom flag layout —
// exercising the helper funcs and proving an end-user-edited template is honored.
const helpGoldenCustomTmpl = `{{- with .Summary}}{{upper .}}
{{end -}}
{{repeat 20 "="}}
{{- with .Flags}}

{{$.Headings.Flags}}
{{range .}}  {{join .Identifiers ", "}} :: {{.Summary}}
{{end}}
{{end -}}
{{- with .Footer}}
{{.}}
{{end -}}
`

// TestHelpGolden_CustomTemplate proves that editing help.txt.tmpl changes the
// rendered output (and that rotini does not overwrite the user's template).
func TestHelpGolden_CustomTemplate(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenCustomSpec, helpGoldenCustomTmpl)
	assertHelpGolden(t, goldenDir, "custom_app_run.txt", readFileString(t, filepath.Join(helpDir, "app_run.txt")))
	// The user's template survives generation untouched.
	mustFileEqual(t, filepath.Join(helpDir, "help.txt.tmpl"), helpGoldenCustomTmpl)
}

const helpGoldenVerbatimSpec = goldenSpecSchema +
	`command:
  name: app
  help: |-
    EXACT ROOT PAGE
      indented line kept as-is
    no trailing newline
  commands:
    - name: run
      summary: run it
      help: |
        EXACT RUN PAGE
        keeps its trailing newline
`

// TestHelpGolden_Verbatim proves an explicit command.help string is written
// byte-for-byte (a |- block has no trailing newline; a | block keeps one), and that
// when every command is verbatim no template is seeded.
func TestHelpGolden_Verbatim(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenVerbatimSpec, "")
	assertHelpGolden(t, goldenDir, "verbatim_app.txt", readFileString(t, filepath.Join(helpDir, "app.txt")))
	assertHelpGolden(t, goldenDir, "verbatim_app_run.txt", readFileString(t, filepath.Join(helpDir, "app_run.txt")))
	if _, err := os.Stat(filepath.Join(helpDir, "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("template should not be seeded when every command is verbatim (err=%v)", err)
	}
}

// helpGoldenCascadingSpec: a root with one cascading flag (--verbose) and one
// non-cascading flag (--root-only), plus two children. 'run' uses the default
// cascading heading; 'deploy' overrides it with a colon-free value.
const helpGoldenCascadingSpec = goldenSpecSchema +
	`command:
  name: app
  summary: the app
  inputs:
    flags:
      - name: verbose
        summary: verbose logging
        identifiers: [-v, --verbose]
        cascading: true
        schema: { type: bool }
      - name: rootonly
        summary: root-only flag
        identifiers: [--root-only]
        schema: { type: bool }
  commands:
    - name: run
      summary: run it
      inputs:
        flags:
          - name: jobs
            summary: parallelism
            identifiers: [-j, --jobs]
            schema: { type: int }
    - name: deploy
      summary: deploy it
      headings:
        cascading: Inherited Flags
      inputs:
        flags:
          - name: target
            summary: where to deploy
            identifiers: [--target]
            schema: { type: string }
`

// TestHelpGolden_Cascading locks the cascading-flags reporting: a cascading flag
// is advertised on descendants under the 'Global Flags:' section (default heading),
// the root itself shows no such section, a non-cascading root flag never leaks down,
// and headings.cascading overrides the heading verbatim (no forced colon).
func TestHelpGolden_Cascading(t *testing.T) {
	goldenDir := helpGoldenDir(t)
	helpDir := genHelp(t, helpGoldenCascadingSpec, "")
	for _, f := range []string{"app.txt", "app_run.txt", "app_deploy.txt"} {
		assertHelpGolden(t, goldenDir, "cascading_"+f, readFileString(t, filepath.Join(helpDir, f)))
	}
}
