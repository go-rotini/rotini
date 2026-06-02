package internal

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// companionConf points generation at the same package layout as the committed
// rotini companion CLI so the output can be compared against it.
const companionConf = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  rth:
    package: cmd/rotini/rth
    file: handlers.go
  rtg:
    package: cmd/rotini/rtg
    file: rotini.go
    features:
      help:
        enabled: true
      man:
        enabled: true
      markdown:
        enabled: true
      completion:
        enabled: true
`

// minimalGoMod uses the same module path the committed handlers rollup imports,
// so the generated framework import path matches the golden file byte-for-byte.
const minimalGoMod = "module github.com/go-rotini/rotini\n\ngo 1.26\n"

// TestGenerateMatchesCompanionExample generates from the committed companion
// spec into a throwaway module and asserts the output matches the hand-written
// example files that define the target shape.
func TestGenerateMatchesCompanionExample(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, companionConf)

	t.Chdir(tmp)
	if err := Generate(specPath, confPath, false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The always-(re)generated files are reproduced byte-for-byte.
	for _, rel := range []string{
		"cmd/rotini/rtg/rotini.go",
		"cmd/rotini/rth/handlers.go",
	} {
		assertGoEqual(t, filepath.Join(tmp, rel), filepath.Join(repoRoot, rel))
	}

	// The companion's generated feature files (help/man/markdown, plus the seeded
	// templates) are golden: the dogfooded output is reproduced byte-for-byte from
	// the committed spec.
	for _, feat := range []string{"help", "man", "markdown", "completion"} {
		featRel := "cmd/rotini/rtg/" + feat
		entries, err := os.ReadDir(filepath.Join(repoRoot, featRel))
		if err != nil {
			t.Fatalf("read companion %s dir: %v", feat, err)
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			got := readFileString(t, filepath.Join(tmp, featRel, e.Name()))
			want := readFileString(t, filepath.Join(repoRoot, featRel, e.Name()))
			if got != want {
				t.Errorf("companion %s/%s not reproduced:\n--- generated ---\n%s\n--- committed ---\n%s", feat, e.Name(), got, want)
			}
		}
	}

	// Handler stubs are create-if-missing user code, so the committed copies are
	// edited (wired to internal funcs) and intentionally diverge from a fresh
	// stub. Assert the generator produced each with the expected stub shape.
	for _, name := range []string{
		"rotini", "rotini_completion", "rotini_generate",
		"rotini_help", "rotini_initialize", "rotini_validate", "rotini_version",
	} {
		mustContain(t, filepath.Join(tmp, "cmd/rotini/rth", name+".go"),
			"package rth", "rotini.CommandHandlers", "*rotini.Context")
	}
}

// TestGenerateDefaultLayout verifies that, with no conf alongside the spec and
// none supplied, the sane defaults place the framework file at rtg/rotini.go
// and the rollup at rth/handlers.go (relative to the module root).
func TestGenerateDefaultLayout(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	// A spec with NO adjacent conf, so generation falls back to the defaults.
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: rotini\n  commands:\n    - name: generate\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"), "package rtg", "type ProgramHandlers interface")
	mustContain(t, filepath.Join(tmp, "rth", "handlers.go"), "package rth", "var Program = rtg.NewProgram(&handlers{})")
	mustContain(t, filepath.Join(tmp, "rth", "rotini_generate.go"), "type rotiniGenerateHandlers struct{}")
}

// TestGeneratePrunesOrphanStubs verifies that pruning (implicit/always-on) drops
// a stub that no longer maps to a command, while rth.keep files survive and
// existing command stubs are left untouched.
func TestGeneratePrunesOrphanStubs(t *testing.T) {
	repoRoot := repoRoot(t)
	specPath := filepath.Join(repoRoot, "cmd", "rotini", ".rotini.spec.yaml")

	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	handlersDir := filepath.Join(tmp, "rth")
	orphan := filepath.Join(handlersDir, "rotini_obsolete.go")
	keep := filepath.Join(handlersDir, "help.go")
	writeTestFile(t, orphan, "package rth\n")
	writeTestFile(t, keep, "package rth\n")

	conf := "generate:\n  rth:\n    keep:\n      - help.go\n"
	confPath := filepath.Join(tmp, ".rotini.conf.yaml")
	writeTestFile(t, confPath, conf)

	t.Chdir(tmp)
	if err := Generate(specPath, confPath, false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan stub was not pruned: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("kept file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(handlersDir, "rotini_generate.go")); err != nil {
		t.Errorf("current command stub missing: %v", err)
	}
}

// helpKeepConf enables help and keeps one rtg feature-dir file by package-relative path.
const helpKeepConf = "generate:\n  rtg:\n    keep:\n      - help/legacy.txt\n    features:\n      help:\n        enabled: true\n"

// TestGeneratePrunesOrphanHelp verifies rtg pruning (implicit/always-on): a help
// .txt for a command no longer in the spec is removed on regenerate, while the
// editable template, current commands' .txt, and rtg.keep paths survive.
func TestGeneratePrunesOrphanHelp(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpKeepConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	helpDir := filepath.Join(tmp, "rtg", "help")
	orphan := filepath.Join(helpDir, "mycli_obsolete.txt")
	legacy := filepath.Join(helpDir, "legacy.txt")
	writeTestFile(t, orphan, "stale\n")
	writeTestFile(t, legacy, "kept\n")

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("second Generate: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan help .txt was not pruned: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("rtg.keep file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(helpDir, helpTemplateName)); err != nil {
		t.Errorf("editable help template was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(helpDir, "mycli.txt")); err != nil {
		t.Errorf("current command help .txt missing: %v", err)
	}
}

// completionConf enables only the completion feature.
const completionConf = "generate:\n  rtg:\n    features:\n      completion:\n        enabled: true\n"

// TestGenerateCompletionEnabled verifies the features group's exception: completion
// emits per-shell embed vars + a shell-keyed resolver (not a command-path one),
// writes a script per supported shell with the program name substituted, and seeds
// no editable template (rotini-owned, Q9 = no template).
func TestGenerateCompletionEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), completionConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"),
		`_ "embed"`,
		"//go:embed completion/bash.txt", "var CompletionBash string",
		"var CompletionZsh string", "var CompletionFish string",
		"func Completion(shell string) (string, error)",
		`case "bash":`,
	)
	// Scripts written per shell, program name substituted.
	mustContain(t, filepath.Join(tmp, "rtg", "completion", "bash.txt"),
		"mycli __complete", "complete -o default -F _mycli_complete mycli")
	for _, sh := range []string{"bash.txt", "zsh.txt", "fish.txt"} {
		if _, err := os.Stat(filepath.Join(tmp, "rtg", "completion", sh)); err != nil {
			t.Errorf("missing completion script %s: %v", sh, err)
		}
	}
	// Completion is rotini-owned — no editable template is seeded.
	entries, err := os.ReadDir(filepath.Join(tmp, "rtg", "completion"))
	if err != nil {
		t.Fatalf("read completion dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmpl") {
			t.Errorf("completion must not seed a template, found %s", e.Name())
		}
	}
}

// TestGenerateOutputTypes verifies the spec `output` key (and document-level
// `schemas`) generate typed Go types into the framework file: a named schema type,
// a "<Prefix>Output" alias for a bare $ref output, and a "<Prefix>Output" struct
// for an inline output — with no flag, no rendering, and no leaked root sentinel.
func TestGenerateOutputTypes(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: mycli\n" +
		"  output:\n" +
		"    type: object\n" +
		"    properties:\n" +
		"      count: { type: integer }\n" +
		"      items: { type: array, items: { $ref: \"#/schemas/Widget\" } }\n" +
		"  commands:\n" +
		"    - name: get\n" +
		"      output: { $ref: \"#/schemas/Widget\" }\n" +
		"schemas:\n" +
		"  Widget:\n" +
		"    type: object\n" +
		"    required: [id]\n" +
		"    properties:\n" +
		"      id: { type: string }\n" +
		"      size: { type: integer }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "rtg", "rotini.go")
	mustContain(t, rotiniGo,
		"type Widget struct {", "`json:\"id\"`", // the named schema
		"type MycliGetOutput Widget", // sub-command bare-$ref output → named alias type
		"type MycliOutput struct {",  // root inline output → struct
		"[]Widget",                   // nested array of the named type
	)
	// The throwaway generation root never leaks into the output.
	mustNotContain(t, rotiniGo, outputRootSentinel)
}

// TestGenerateInputImports verifies the spec `import:` key drives the framework
// file's import block: explicit imports (stdlib + third-party + aliased) are emitted,
// rotini's own time-family aliases auto-import "time", and duplicates dedupe.
func TestGenerateInputImports(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: widget\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: since\n" +
		"        identifiers: [--since]\n" +
		"        schema: { type: time.Time, import: time }\n" +
		"      - name: ttl\n" +
		"        identifiers: [--ttl]\n" +
		"        schema: { type: duration }\n" + // rotini alias → auto "time", and dedupes with the above
		"      - name: id\n" +
		"        identifiers: [--id]\n" +
		"        schema: { type: uuid.UUID, import: github.com/google/uuid }\n" +
		"      - name: home\n" +
		"        identifiers: [--home]\n" +
		"        schema: { type: urlx.URL, import: urlx net/url }\n" // aliased import
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "rtg", "rotini.go")
	mustContain(t, rotiniGo,
		"\"time\"",                   // time.Time + duration alias, deduped to one
		"\"github.com/google/uuid\"", // third-party
		"urlx \"net/url\"",           // aliased form rendered as `urlx "net/url"`
		"type WidgetFlags struct {",
		"uuid.UUID", "urlx.URL", // the typed flag fields
	)
	// "time" appears once in the import block (deduped), not twice.
	if got := readFileString(t, rotiniGo); strings.Count(got, "\t\"time\"\n") != 1 {
		t.Errorf("expected exactly one \"time\" import line, got %d:\n%s", strings.Count(got, "\t\"time\"\n"), got)
	}
}

// TestGenerateRemoteDiscovery verifies remote_discovery is emitted into the
// Definition: the root gets a default "<host>-" prefix, a sub-command keeps its
// explicit prefix + hidden flag.
func TestGenerateRemoteDiscovery(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: acme\n" +
		"  remote_discovery:\n" +
		"    path: /opt/acme/plugins\n" +
		"  commands:\n" +
		"    - name: cluster\n" +
		"      remote_discovery:\n" +
		"        prefix: acme-plugin-\n" +
		"        hidden: true\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "rtg", "rotini.go")
	mustContain(t, rotiniGo,
		"RemoteDiscoveryDef{Prefix: \"acme-\"", // root: default prefix <host>-
		"Path: \"/opt/acme/plugins\"",
		"RemoteDiscoveryDef{Prefix: \"acme-plugin-\"", // sub: explicit prefix
		"Hidden: true",
	)
}

// TestGenerateInputChannels verifies the typed input channels (Phase 1, types only):
// pure env/config inputs get their own <Prefix>Env/<Prefix>Config structs (env is NOT
// folded into Flags), stdin gets a typed payload type + a *Stdin field, and
// CommandInputs gains the new fields only when the channel is declared.
func TestGenerateInputChannels(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	spec := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
		"command:\n" +
		"  name: widget\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: color\n" +
		"        identifiers: [--color]\n" +
		"        schema: { type: string, key: create.color }\n" + // config-fallback flag
		"      - name: quiet\n" +
		"        identifiers: [--quiet]\n" +
		"        schema: { type: bool }\n" + // argv-only flag (no recon tag)

		"    env:\n" +
		"      - name: region\n" +
		"        schema: { type: string, variable: WIDGET_REGION }\n" +
		"    config:\n" +
		"      - name: endpoint\n" +
		"        schema: { type: string, file: app, key: api.endpoint }\n" +
		"      - name: token\n" +
		"        schema: { type: string, key: api.token, secret: true, required: true }\n" +
		"    stdin:\n" +
		"      schema: { $ref: \"#/schemas/Manifest\" }\n" +
		"configuration_files:\n" +
		"  - name: app\n" +
		"    path: ~/.config/widget.yaml\n" +
		"    format: yaml\n" +
		"schemas:\n" +
		"  Manifest:\n" +
		"    type: object\n" +
		"    required: [kind]\n" +
		"    properties:\n" +
		"      kind: { type: string }\n"
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), spec)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", "", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rotiniGo := filepath.Join(tmp, "rtg", "rotini.go")
	mustContain(t, rotiniGo,
		"type WidgetFlags struct {",  // argv flag stays
		"Color string",               //
		"type WidgetEnv struct {",    // env channel — its own struct
		"Region string",              //
		"type WidgetConfig struct {", // config channel — its own struct
		"Endpoint string",            //
		"type WidgetStdin Manifest",  // stdin payload type (from $ref, via the type machinery)
		"type Manifest struct {",     // the named schema
		"Env       WidgetEnv",        // CommandInputs gains the channels
		"Config    WidgetConfig",     //
		"Stdin     *WidgetStdin",     //
		// a config-fallback flag carries a recon key (the argv-only `quiet` flag does not).
		"recon:\"create.color\"",
		// recon tags drive the binder; env key = name, config key = schema.key, + secret/required.
		"`rotini:\"region\" recon:\"region\"`",
		"`rotini:\"endpoint\" recon:\"api.endpoint\"`",
		"`rotini:\"token\" recon:\"api.token,required,secret\"`",
		// the BindMeta descriptor carries the config-file sources.
		"var BindMeta = rotini.BindMeta{",
		"{Name: \"app\", Path: \"~/.config/widget.yaml\", Format: \"yaml\"}",
	)
	// env is NOT folded into the Flags struct (the channel break).
	flags := readFileString(t, rotiniGo)
	if i := strings.Index(flags, "type WidgetFlags struct {"); i >= 0 {
		block := flags[i:]
		if end := strings.Index(block, "}"); end >= 0 && strings.Contains(block[:end], "Region") {
			t.Errorf("env field Region must not be folded into WidgetFlags:\n%s", block[:end])
		}
	}
}

// helpSpecYAML is a spec exercising generated help: root summary/description plus
// a sub-command with a summary, a required argument, and a bool flag. Help fields
// live directly on the command (flattened).
const helpSpecYAML = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n" +
	"command:\n" +
	"  name: mycli\n" +
	"  summary: my cli\n" +
	"  description: A demo CLI.\n" +
	"  footer: run 'mycli help <command>' for details\n" +
	"  commands:\n" +
	"    - name: build\n" +
	"      aliases: [b]\n" +
	"      summary: build the project\n" +
	"      inputs:\n" +
	"        arguments:\n" +
	"          - name: target\n" +
	"            summary: thing to build\n" +
	"            schema:\n" +
	"              type: string\n" +
	"              required: true\n" +
	"        flags:\n" +
	"          - name: verbose\n" +
	"            summary: chattier output\n" +
	"            identifiers: [-v, --verbose]\n" +
	"            schema:\n" +
	"              type: bool\n"

const helpEnabledConf = "generate:\n  rtg:\n    features:\n      help:\n        enabled: true\n"

// TestGenerateHelpEnabled verifies that, with generate.help enabled, the
// framework file gains the embedded Help<Prefix> vars + an alias-aware Help
// resolver, and that each command's help .txt is rendered from the spec (the
// default generate mode), with a second pass producing byte-identical output.
func TestGenerateHelpEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"),
		`_ "embed"`,
		"//go:embed help/mycli.txt",
		"var HelpMycli string",
		"var HelpMycliBuild string",
		"func Help(path ...string) (string, error)",
		`case "":`,
		`case "build", "b":`,
	)

	// The editable default template was seeded.
	if _, err := os.Stat(filepath.Join(tmp, "rtg", "help", "help.txt.tmpl")); err != nil {
		t.Errorf("default help template not seeded: %v", err)
	}

	// The root page renders the description, a derived usage line, the commands
	// list (with the alias and summary), and the footer.
	root := filepath.Join(tmp, "rtg", "help", "mycli.txt")
	mustContain(t, root,
		"A demo CLI.",
		"Usage:",
		"mycli <command>",
		"Commands:",
		"build;b",
		"build the project",
		"run 'mycli help <command>' for details",
	)
	// Generated pages end exactly at their last line — no trailing newline.
	if got := readFileString(t, root); strings.HasSuffix(got, "\n") {
		t.Errorf("generated help should not end with a trailing newline; got %q", got)
	}
	// The leaf page renders derived usage, the decorated required arg, and the flag.
	build := filepath.Join(tmp, "rtg", "help", "mycli_build.txt")
	mustContain(t, build,
		"mycli build <target> [flags]",
		"Arguments:",
		"<target>",
		"thing to build",
		"Flags:",
		"-v,--verbose",
		"chattier output",
	)

	// Capture rendered output + framework file, then prove a second pass is a
	// byte-identical no-op (determinism + overwrite-guard "identical => skip").
	rootBefore := readFileString(t, root)
	buildBefore := readFileString(t, build)
	fwBefore := readAndFormat(t, filepath.Join(tmp, "rtg", "rotini.go"))

	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate (second pass): %v", err)
	}
	mustFileEqual(t, root, rootBefore)
	mustFileEqual(t, build, buildBefore)

	after := readAndFormat(t, filepath.Join(tmp, "rtg", "rotini.go"))
	if !bytes.Equal(fwBefore, after) {
		t.Errorf("help generation is not idempotent:\n--- before ---\n%s\n--- after ---\n%s", fwBefore, after)
	}
}

// TestGenerateHelpRegenerates verifies that rendered help .txt files are
// rotini-managed: each pass (re)writes the rendered content, so a hand edit to a
// rendered file is replaced on the next generation (to own a command's words,
// set its verbatim `help` string in the spec instead).
func TestGenerateHelpRegenerates(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// A hand edit to a generate-mode file is overwritten on the next pass.
	build := filepath.Join(tmp, "rtg", "help", "mycli_build.txt")
	writeTestFile(t, build, "hand-written help for build\n")
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate (regenerate): %v", err)
	}
	mustContain(t, build, "mycli build <target> [flags]")
	mustNotContain(t, build, "hand-written help for build")
}

// manMarkdownConf enables man + markdown (but not help).
const manMarkdownConf = "generate:\n  rtg:\n    features:\n      man:\n        enabled: true\n      markdown:\n        enabled: true\n"

// TestGenerateManMarkdownEnabled verifies the help pipeline generalizes: with man
// and markdown enabled, the framework gains per-feature embed vars + alias-aware
// resolvers, each feature renders into its own dir with its own extension and
// seeded template, and a disabled feature (help) produces nothing.
func TestGenerateManMarkdownEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"), helpSpecYAML)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), manMarkdownConf)

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustContain(t, filepath.Join(tmp, "rtg", "rotini.go"),
		`_ "embed"`,
		"//go:embed man/mycli.txt", "var ManMycli string", "var ManMycliBuild string",
		"func Man(path ...string) (string, error)",
		"//go:embed markdown/mycli.md", "var MarkdownMycli string", "var MarkdownMycliBuild string",
		"func Markdown(path ...string) (string, error)",
		`case "build", "b":`,
	)
	// Help was not enabled — no Help resolver, no help dir.
	mustNotContain(t, filepath.Join(tmp, "rtg", "rotini.go"), "func Help(")
	if _, err := os.Stat(filepath.Join(tmp, "rtg", "help")); !os.IsNotExist(err) {
		t.Errorf("help dir should not exist when help is off (err=%v)", err)
	}

	for _, p := range []string{
		"rtg/man/man.txt.tmpl", "rtg/man/mycli.txt", "rtg/man/mycli_build.txt",
		"rtg/markdown/markdown.md.tmpl", "rtg/markdown/mycli.md", "rtg/markdown/mycli_build.md",
	} {
		if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected generated %s: %v", p, err)
		}
	}
}

// TestGenerateManMarkdownVerbatim verifies the per-command verbatim escapes: a
// command's `man` / `markdown` strings are written byte-for-byte (no template
// seeded when nothing renders), exactly like help's verbatim `help` string.
func TestGenerateManMarkdownVerbatim(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), manMarkdownConf)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"command:\n"+
			"  name: mycli\n"+
			"  man: |-\n"+
			"    MYCLI(1)\n"+
			"    exact man page\n"+
			"  markdown: |-\n"+
			"    # mycli\n"+
			"    exact markdown page\n")

	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	mustFileEqual(t, filepath.Join(tmp, "rtg", "man", "mycli.txt"), "MYCLI(1)\nexact man page")
	mustFileEqual(t, filepath.Join(tmp, "rtg", "markdown", "mycli.md"), "# mycli\nexact markdown page")

	// Nothing renders (root supplies verbatim, no sub-commands) → no templates seeded.
	for _, p := range []string{"rtg/man/man.txt.tmpl", "rtg/markdown/markdown.md.tmpl"} {
		if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("%s should not be seeded when no command renders (err=%v)", p, err)
		}
	}
}

// TestGenerateHelpVerbatim verifies that a populated `help` string is written
// EXACTLY as supplied — byte-for-byte, with no trailing-newline normalization (a
// YAML `|-` strip block yields no trailing newline, and rotini keeps it that way) —
// and that when every command supplies verbatim help no template is seeded.
func TestGenerateHelpVerbatim(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"), helpEnabledConf)
	writeTestFile(t, filepath.Join(tmp, ".rotini.spec.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\n"+
			"command:\n"+
			"  name: mycli\n"+
			"  help: |-\n"+ // strip: no trailing newline
			"    my exact help page\n"+
			"    line two\n")
	t.Chdir(tmp)
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Exactly the supplied bytes — no trailing newline added.
	mustFileEqual(t, filepath.Join(tmp, "rtg", "help", "mycli.txt"), "my exact help page\nline two")

	// Nothing renders (root supplies verbatim help, no sub-commands) → no template.
	if _, err := os.Stat(filepath.Join(tmp, "rtg", "help", "help.txt.tmpl")); !os.IsNotExist(err) {
		t.Errorf("help.txt.tmpl should not be seeded when no command renders (err=%v)", err)
	}

	// The verbatim string wins; a hand edit is overwritten back to the spec value.
	root := filepath.Join(tmp, "rtg", "help", "mycli.txt")
	writeTestFile(t, root, "tampered\n")
	if err := Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("Generate (regenerate): %v", err)
	}
	mustFileEqual(t, root, "my exact help page\nline two")
}

// repoRoot returns the rotini module root (the parent of the internal package
// directory the test runs in).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// assertGoEqual compares two Go files for equality after gofmt normalization,
// so non-canonical whitespace in the hand-written golden files does not cause
// spurious failures.
func assertGoEqual(t *testing.T, generatedPath, goldenPath string) {
	t.Helper()
	gen := readAndFormat(t, generatedPath)
	golden := readAndFormat(t, goldenPath)
	if !bytes.Equal(gen, golden) {
		t.Errorf("generated %s does not match golden %s\n--- generated ---\n%s\n--- golden ---\n%s",
			generatedPath, goldenPath, gen, golden)
	}
}

func readAndFormat(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	formatted, err := format.Source(data)
	if err != nil {
		t.Fatalf("gofmt %s: %v", path, err)
	}
	return formatted
}

func mustContain(t *testing.T, path string, substrs ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range substrs {
		if !bytes.Contains(data, []byte(s)) {
			t.Errorf("%s missing %q\n%s", path, s, data)
		}
	}
}

func mustFileEqual(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("%s = %q, want %q", path, data, want)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// specWith builds a minimal mycli spec declaring the given top-level commands.
func specWith(commands ...string) string {
	var b strings.Builder
	b.WriteString("$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json\ncommand:\n  name: mycli\n  commands:\n")
	for _, c := range commands {
		b.WriteString("    - name: " + c + "\n")
	}
	return b.String()
}

func fileContains(path, substr string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(substr))
}

// waitForCond polls cond until it returns true or the timeout elapses.
func waitForCond(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGenerateWatchInitialAndStop verifies the initial pass runs and that
// cancelling ctx makes watchLoop return cleanly (the signal-driven exit).
func TestGenerateWatchInitialAndStop(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	onGen := func(_ string, err error) {
		if err != nil {
			fmt.Fprintln(&buf, err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", onGen) }()

	rtg := filepath.Join(tmp, "rtg", "rotini.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate did not produce %s with MycliAlpha\noutput:\n%s", rtg, buf.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchLoop returned error after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchLoop did not return after ctx cancellation")
	}
}

// TestGenerateWatchRegeneratesOnChange verifies that editing the spec while
// watching triggers a re-generation that reflects the change.
func TestGenerateWatchRegeneratesOnChange(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), minimalGoMod)
	specPath := filepath.Join(tmp, ".rotini.spec.yaml")
	writeTestFile(t, specPath, specWith("alpha"))
	t.Chdir(tmp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf bytes.Buffer
	onGen := func(_ string, err error) {
		if err != nil {
			fmt.Fprintln(&buf, err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- watchLoop(ctx, specPath, "", onGen) }()

	rtg := filepath.Join(tmp, "rtg", "rotini.go")
	if !waitForCond(3*time.Second, func() bool { return fileContains(rtg, "MycliAlpha") }) {
		t.Fatalf("initial generate missing MycliAlpha\noutput:\n%s", buf.String())
	}
	if fileContains(rtg, "MycliBeta") {
		t.Fatal("MycliBeta present before the spec was changed")
	}

	// Add a beta command; the watcher should pick it up and re-generate.
	writeTestFile(t, specPath, specWith("alpha", "beta"))
	if !waitForCond(5*time.Second, func() bool { return fileContains(rtg, "MycliBeta") }) {
		t.Fatalf("watch did not regenerate after spec change (no MycliBeta)\noutput:\n%s", buf.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watchLoop did not return after cancel")
	}
}

// TestRoundDuration checks that the watch summary's elapsed time renders in the
// best-fitting unit, trimmed to ~3 significant figures.
func TestRoundDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{312 * time.Nanosecond, "312ns"},
		{2793 * time.Nanosecond, "2.79µs"},
		{45678 * time.Nanosecond, "45.7µs"},
		{2793256 * time.Nanosecond, "2.79ms"},
		{1234567890 * time.Nanosecond, "1.23s"},
	}
	for _, c := range cases {
		if got := roundDuration(c.in).String(); got != c.want {
			t.Errorf("roundDuration(%d ns) = %q, want %q", c.in.Nanoseconds(), got, c.want)
		}
	}
}
