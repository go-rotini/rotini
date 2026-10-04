package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// featureSpec is the feature-and-channel-rich counterpart to goldenSpec: where that one
// is a byte-stability net over a minimal CLI, this one turns EVERY derived output on and
// declares every input channel, so the help/man/markdown/completion renderers and the
// schema/InputSettings emitters actually run. Kept functional (assert on what was emitted)
// rather than byte-golden — a snapshot of four rendered doc formats would churn on any
// wording change without catching more.
const featureSpec = `version: 0.0.0
command:
  name: acme
  summary: acme control
  description: the acme control cli
  usage: acme <command> [flags]
  footer: See 'acme help <command>' for more.
  examples:
    - acme deploy web --replicas 3
  env_prefix: ACME
  headings:
    commands: Subcommands
    flags: Options
  exit_status:
    - code: 0
      summary: success
    - code: 2
      summary: usage error
  see_also:
    - acme(1)
  flags:
    - name: verbose
      summary: verbose output
      identifiers: [--verbose, -v]
      cascading: true
      schema: {type: bool}
    - name: config
      summary: config file path
      identifiers: [--config, -c]
      schema: {type: string}
  env:
    - name: token
      summary: api token
      schema: {type: string, variable: ACME_TOKEN, secret: true}
    - name: conf-path
      summary: config path override
      schema: {type: string, config_source: project}
    - name: db
      summary: database settings
      schema: {type: map, nesting: "_"}
  config:
    - name: endpoint
      summary: api endpoint
      schema: {type: string, default: https://api.acme.test}
    - name: retries
      summary: retry budget
      schema: {type: int, minimum: 0, maximum: 5, file: project}
  schemas:
    Manifest:
      type: object
      properties:
        kind: {type: string}
      required: [kind]
  config_files:
    - name: project
      discover: {strategy: walk-up, file: .acme.yaml}
      schema:
        type: object
        properties:
          endpoint: {type: string}
    - name: user
      discover: {strategy: xdg, file: config.yaml, app: acme}
  commands:
    - name: deploy
      summary: deploy a service
      group: core
      aliases: [dep]
      arguments:
        - name: service
          summary: service to deploy
          schema: {type: string, required: true, enum: [web, api]}
        - name: rest
          summary: extra args
          schema: {type: "[]string"}
      flags:
        - name: replicas
          summary: replica count
          identifiers: [--replicas, -r]
          schema: {type: int, minimum: 1, maximum: 10, default: 1}
        - name: dry-run
          summary: plan only
          identifiers: [--dry-run]
          schema: {type: bool}
      flag_groups:
        - kind: mutually_exclusive
          flags: [replicas, dry-run]
      stdin:
        format: yaml
        schema: {$ref: "#/schemas/Manifest"}
    - name: status
      summary: show status
    - name: secret
      summary: hidden helper
      hidden: true
`

// featureConfInline turns all four features on in INLINE mode (embed: false): content is
// a string literal in the generated .go and no render files are written.
const featureConfInline = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/acme/main.go
      package: main
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: help
      enabled: true
    - type: completion
      enabled: true
    - type: man
      enabled: true
    - type: markdown
      enabled: true
`

// featureConfEmbed turns the same features on in EMBED mode with editable templates
// seeded, exercising the other two sourcing quadrants: rendered files under embed_dir
// sourced via //go:embed, and the seeded *.tmpl the doc pages render from.
const featureConfEmbed = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/acme/main.go
      package: main
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: help
      enabled: true
      embed: true
      template: true
    - type: completion
      enabled: true
      embed: true
    - type: man
      enabled: true
      embed: true
      template: true
    - type: markdown
      enabled: true
      embed: true
      template: true
`

// emitFeatureModule writes the feature-rich spec plus conf into a temp module wired to
// THIS repo (the scaffold imports github.com/go-rotini/rotini), runs generate, and
// returns the module dir with every emitted path (module-relative, slash-separated).
func emitFeatureModule(t *testing.T, conf string) (dir string, files []string) {
	t.Helper()
	return emitModule(t, featureSpec, conf)
}

// emitModule is emitFeatureModule for any spec.
func emitModule(t *testing.T, spec, conf string) (dir string, files []string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)

	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			t.Errorf("Generate reported: %v", e)
		}
	}, func([]error) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return dir, files
}

// readEmitted returns one emitted file's content.
func readEmitted(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestGenerateFeatures_inline covers the default sourcing quadrant: every feature on,
// content inlined as string literals, no files written beside the .go. It also pins the
// RENDERED help contents, which is where most of the doc pipeline (headings, grouping,
// usage derivation, flag rows, cascading flags, env/config sections) actually runs.
func TestGenerateFeatures_inline(t *testing.T) {
	skipUnlessCompiling(t)
	dir, files := emitFeatureModule(t, featureConfInline)

	for _, f := range files {
		if strings.Contains(f, "/renders/") || strings.Contains(f, "/templates/") {
			t.Errorf("inline mode wrote %s — content must be a string literal, not a file", f)
		}
	}

	gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	for _, want := range []string{
		"HelpAcme", "HelpAcmeDeploy", // per-command help vars
		"ManAcme", "MarkdownAcme", // the other doc features
		"CompletionBash", "CompletionZsh", "CompletionFish", "CompletionPowershell",
		"func Help(", "func Man(", "func Markdown(", "func Completion(", // the resolvers
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated file missing %q", want)
		}
	}
	if strings.Contains(gen, "go:embed") {
		t.Error("inline mode emitted a //go:embed directive")
	}

	// The rendered root help: custom headings, the derived usage, declared inputs, and
	// the doc-fields — the output of buildHelpData and everything it calls.
	help := gen[strings.Index(gen, "HelpAcme "):]
	for _, want := range []string{
		// A heading override is rendered VERBATIM — the defaults carry the trailing
		// ":" so an author can restyle or drop it (resolveHeadings). So the flags
		// override appears bare, and only ungrouped commands use the commands
		// heading at all: a grouped command renders under its group title.
		"Subcommands", // headings.commands override, for the ungrouped `status`
		"core:",       // deploy's group title wins over the commands heading
		"Options",     // headings.flags override
		"Usage:",      // an un-overridden heading keeps its default colon
		"the acme control cli",
		"acme <command> [flags]",
		"deploy, dep",           // name + alias
		"--config, -c string",   // typed flag row
		"ACME_TOKEN string",     // env input row
		"https://api.acme.test", // config default
		"acme deploy web",       // example
		"See 'acme help",        // footer
	} {
		if !strings.Contains(help, want) {
			t.Errorf("rendered root help missing %q", want)
		}
	}
	root := help[:strings.Index(help, "HelpAcmeDeploy")]
	if strings.Contains(root, "hidden helper") {
		t.Error("root help lists the hidden command")
	}
	// The override is verbatim: rotini adds no colon of its own, so the overridden
	// heading must NOT gain the one the default carries.
	if strings.Contains(root, "Options:") {
		t.Error(`heading override rendered as "Options:" — an override is verbatim, the template adds nothing`)
	}

	// A hidden command still gets its handler stub — hidden is a HELP concern only.
	for _, want := range []string{"internal/cmd/acme/acme_deploy.go", "internal/cmd/acme/acme_secret.go"} {
		if !slicesContains(files, want) {
			t.Errorf("missing handler stub %s", want)
		}
	}

	buildEmitted(t, dir)
}

// TestGenerateFeatures_embed covers the other two sourcing quadrants at once: rendered
// output files under embed_dir sourced via //go:embed, and the editable *.tmpl seeded
// into template_dir that the doc pages then render from.
func TestGenerateFeatures_embed(t *testing.T) {
	skipUnlessCompiling(t)
	dir, files := emitFeatureModule(t, featureConfEmbed)

	for _, want := range []string{
		"internal/cmd/acme/renders/help_acme.txt",
		"internal/cmd/acme/renders/help_acme_deploy.txt",
		"internal/cmd/acme/renders/acme.1",
		"internal/cmd/acme/renders/acme-deploy.1",
		"internal/cmd/acme/renders/markdown_acme.md",
		"internal/cmd/acme/renders/completion_bash.txt",
		"internal/cmd/acme/renders/completion_zsh.txt",
		"internal/cmd/acme/renders/completion_fish.txt",
		"internal/cmd/acme/renders/completion_powershell.txt",
		"internal/cmd/acme/templates/help.txt.tmpl",
		"internal/cmd/acme/templates/man.txt.tmpl",
		"internal/cmd/acme/templates/markdown.md.tmpl",
	} {
		if !slicesContains(files, want) {
			t.Errorf("embed mode did not write %s (emitted: %v)", want, files)
		}
	}
	// completion has no editable template — seeding one would be a silent lie.
	if slicesContains(files, "internal/cmd/acme/templates/completion.txt.tmpl") {
		t.Error("seeded a completion template; completion has none")
	}

	gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if !strings.Contains(gen, "go:embed") {
		t.Error("embed mode emitted no //go:embed directive")
	}
	// The completion script drives the binary's hidden protocol entry.
	if bash := readEmitted(t, dir, "internal/cmd/acme/renders/completion_bash.txt"); !strings.Contains(bash, "__complete") {
		t.Error("bash completion script does not call the __complete entry")
	}

	buildEmitted(t, dir)
}

// buildEmitted compiles the generated module. Rendering valid-looking text is not
// enough: the feature vars, //go:embed directives and resolvers must be valid Go that
// references files actually written.
func buildEmitted(t *testing.T, dir string) {
	t.Helper()
	if testing.Short() {
		t.Skip("compiles the generated module; skipped under -short")
	}
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the generated module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func slicesContains(list []string, want string) bool {
	return slices.Contains(list, want)
}

// readEmittedIfExists reads an emitted file, returning the error when it is absent —
// for asserting that codegen did NOT write something.
func readEmittedIfExists(dir, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	return string(b), err
}

// TestFlagGroups covers the `group:` key on a flag — the presentational bucketing that turns a
// command with twenty flags from one undifferentiated wall into a page someone reads.
//
// The properties that matter are the ones a reader would notice: groups appear in
// first-declared order, ungrouped flags keep the default heading, and a command that declares
// no groups renders exactly what it rendered before the key existed.
func TestFlagGroups(t *testing.T) {
	rows := []templateDocFlagRow{
		{Identifiers: []string{"--verbose"}},
		{Identifiers: []string{"--cert"}, Group: "TLS"},
		{Identifiers: []string{"--out"}, Group: "Output"},
		{Identifiers: []string{"--key"}, Group: "TLS"},
		{Identifiers: []string{"--quiet"}},
	}
	groups := groupFlags(rows)

	if len(groups) != 3 {
		t.Fatalf("got %d buckets, want 3 (ungrouped, TLS, Output)", len(groups))
	}
	// First-declared order, and the ungrouped bucket is titled "" so each template heads
	// it with its own default.
	want := []struct {
		title string
		flags []string
	}{
		{"", []string{"--verbose", "--quiet"}},
		{"TLS", []string{"--cert", "--key"}},
		{"Output", []string{"--out"}},
	}
	for i, w := range want {
		if groups[i].Title != w.title {
			t.Errorf("bucket %d title = %q, want %q", i, groups[i].Title, w.title)
		}
		var got []string
		for _, f := range groups[i].Flags {
			got = append(got, f.Identifiers[0])
		}
		if !slices.Equal(got, w.flags) {
			t.Errorf("bucket %q = %v, want %v", w.title, got, w.flags)
		}
	}

	// No groups declared → one bucket, in declaration order: identical to the old output.
	plain := groupFlags([]templateDocFlagRow{
		{Identifiers: []string{"--a"}}, {Identifiers: []string{"--b"}},
	})
	if len(plain) != 1 || plain[0].Title != "" || len(plain[0].Flags) != 2 {
		t.Errorf("an ungrouped command should render one untitled bucket, got %+v", plain)
	}

	if groupFlags(nil) != nil {
		t.Error("no flags should yield no buckets, so the section is omitted entirely")
	}
}

// TestFeatureCombinations_compile: every subset of the four features has to produce a module
// that COMPILES. The golden tests cover one combination, which is how a program with only the
// completion feature on ended up importing "strings" and never using it — the generated file
// did not build, and no test in the repo turned that feature on alone.
//
// The imports the generated file needs depend on WHICH features are on, not merely on whether
// any are: help, man and markdown emit a path-keyed resolver that uses strings.Join, and
// completion — keyed by shell — does not.
func TestFeatureCombinations_compile(t *testing.T) {
	skipUnlessCompiling(t)
	const spec = `version: 0.0.0
command:
  name: acme
  summary: a cli
  description: A cli.
  commands:
    - name: deploy
      summary: deploy it
`
	all := []string{"help", "completion", "man", "markdown"}

	// Every non-empty subset, plus the empty one: 16 in total.
	for mask := range 1 << len(all) {
		var on []string
		for i, name := range all {
			if mask&(1<<i) != 0 {
				on = append(on, name)
			}
		}
		label := "none"
		if len(on) > 0 {
			label = strings.Join(on, "+")
		}

		t.Run(label, func(t *testing.T) {
			repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
			if err != nil {
				t.Fatal(err)
			}
			conf := `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
`
			if len(on) > 0 {
				conf += "  features:\n"
				for _, name := range on {
					conf += "    - type: " + name + "\n      enabled: true\n"
				}
			}

			dir := t.TempDir()
			writeTestFile(t, dir, "go.mod", "module example.com/acme\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
			writeTestFile(t, dir, ".rotini.spec.yaml", spec)
			writeTestFile(t, dir, ".rotini.conf.yaml", conf)
			t.Chdir(dir)

			if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
				t.Fatalf("generate with %s: %v", label, err)
			}
			if out, err := goBuild(t, dir); err != nil {
				t.Fatalf("go build with features %s:\n%s", label, out)
			}
		})
	}
}

// TestHelpPage_fallsBackToSummary pins the prose a command's own help page leads with.
//
// A command that declares only a `summary:` used to render a page with no prose at all — the
// summary appeared in its PARENT's command list and nowhere on its own page, so `cli help sub`
// answered every question about a command except what it does. man carries the summary in its
// NAME line and markdown renders it outright, so help was the only one dropping it.
//
// Both directions matter: the fallback must not displace a real description.
// TestDisplayName pins what display_name changes and what it leaves alone. A kubectl plugin is
// the binary kubectl-acme, but the user types `kubectl acme`, and its pages should say so. The
// pages it rewrites are the derived ones; the file names, the completion script's target and
// anything written verbatim keep the real name or the author's words.
func TestDisplayName(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: kubectl-acme
  display_name: kubectl acme
  summary: an acme plugin
  footer: Use "kubectl-acme help" (verbatim, never rewritten).
  commands:
    - name: deploy
      summary: deploy a thing
      arguments:
        - name: target
          schema: { type: string }
`
	dir, files := emitModule(t, spec, featureConfEmbed)

	read := func(rel string) string { return readEmitted(t, dir, "internal/cmd/acme/renders/"+rel) }
	for _, c := range []struct{ file, want string }{
		{"help_kubectl-acme.txt", "  kubectl acme <command>"},
		{"help_kubectl-acme_deploy.txt", "  kubectl acme deploy [target]"},
		{"kubectl-acme-deploy.1", `.TH "KUBECTL\-ACME\-DEPLOY" 1 "" "kubectl acme" "User Commands"`},
		{"kubectl-acme-deploy.1", `kubectl\-acme\-deploy \- deploy a thing`},
		{"kubectl-acme-deploy.1", `\fBkubectl acme deploy\fR [target]`},
		{"markdown_kubectl-acme_deploy.md", "# kubectl acme deploy"},
	} {
		if page := read(c.file); !strings.Contains(page, c.want) {
			t.Errorf("%s lacks %q:\n%s", c.file, c.want, page)
		}
	}
	if page := read("help_kubectl-acme.txt"); !strings.Contains(page, `Use "kubectl-acme help" (verbatim`) {
		t.Errorf("the verbatim footer was rewritten:\n%s", page)
	}
	if script := read("completion_bash.txt"); !strings.Contains(script, "kubectl-acme __complete") || strings.Contains(script, "kubectl acme") {
		t.Errorf("completion must target the binary, kubectl-acme:\n%s", script)
	}
	for _, f := range files {
		if strings.Contains(f, " ") {
			t.Errorf("a generated file name contains the display name's space: %s", f)
		}
	}
}

func TestHelpPage_fallsBackToSummary(t *testing.T) {
	t.Parallel()
	tmpl, err := parseDocTemplate("help", templateHelp)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		data       templateHelpData
		wantFirst  string
		wantAbsent string
	}{
		{
			name:      "summary only",
			data:      templateHelpData{Summary: "build a container image", Invocation: "app image", UsageDerived: "app image"},
			wantFirst: "build a container image",
		},
		{
			name:       "a description wins",
			data:       templateHelpData{Summary: "the short one", Description: "The long one.", Invocation: "app image", UsageDerived: "app image"},
			wantFirst:  "The long one.",
			wantAbsent: "the short one",
		},
		{
			name:      "neither leaves the page prose-free",
			data:      templateHelpData{Invocation: "app image", UsageDerived: "app image"},
			wantFirst: "Usage:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.data.Headings.Usage = "Usage:"
			page, err := renderDocText(tmpl, tt.data)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(page, "\n")
			if first != tt.wantFirst {
				t.Errorf("page leads with %q, want %q\n--- page ---\n%s", first, tt.wantFirst, page)
			}
			if tt.wantAbsent != "" && strings.Contains(page, tt.wantAbsent) {
				t.Errorf("page contains %q, which the description should have displaced\n%s", tt.wantAbsent, page)
			}
		})
	}
}

// TestHelpRows_deprecatedIdentifiersDeprecateOnlyThoseNames pins where a deprecation shows. With
// deprecated_identifiers, the run warns only when one of THOSE spellings is used — so help used
// to contradict it, marking the whole command `(deprecated: …)` and listing the deprecated alias
// beside it as though it were current. The row now drops the deprecated names
// and the marker; without the list, the message still deprecates the whole command or flag.
func TestHelpRows_deprecatedIdentifiersDeprecateOnlyThoseNames(t *testing.T) {
	t.Parallel()
	children := []rnode{
		{name: "api-resources", aliases: []string{"api-versions", "ar"}, deprecatedIdentifiers: []string{"api-versions"}, deprecated: "use api-resources"},
		{name: "old", aliases: []string{"o"}, deprecated: "use new"},
		{name: "only", aliases: []string{"legacy"}, deprecatedIdentifiers: []string{"legacy"}, deprecated: "use only"},
	}
	rows := buildHelpData("app", cmdHelp{}, nil, children, nil, nil, "").CommandGroups[0].Commands
	want := []templateDocCommandRow{
		{Name: "api-resources", Aliases: []string{"ar"}},
		{Name: "old", Aliases: []string{"o"}, Deprecated: "use new"},
		{Name: "only", Aliases: []string{}},
	}
	for i, w := range want {
		got := rows[i]
		if got.Name != w.Name || strings.Join(got.Aliases, ",") != strings.Join(w.Aliases, ",") || got.Deprecated != w.Deprecated {
			t.Errorf("row %d = {%s %v %q}, want {%s %v %q}", i, got.Name, got.Aliases, got.Deprecated, w.Name, w.Aliases, w.Deprecated)
		}
	}

	for _, tt := range []struct {
		f       FlagInput
		ids     string
		message string
	}{
		{FlagInput{Name: "output", Identifiers: []string{"-o", "--output", "--format"}, DeprecatedIdentifiers: []string{"--format"}, Deprecated: "use --output"}, "-o,--output", ""},
		{FlagInput{Name: "store", Identifiers: []string{"--store"}, Deprecated: "use a context"}, "--store", "use a context"},
		// Every spelling deprecated is the whole flag deprecated: listed, and marked.
		{FlagInput{Name: "x", Identifiers: []string{"--x"}, DeprecatedIdentifiers: []string{"--x"}, Deprecated: "gone soon"}, "--x", "gone soon"},
	} {
		row := flagRow(tt.f)
		if strings.Join(row.Identifiers, ",") != tt.ids || row.Deprecated != tt.message {
			t.Errorf("flag %s row = %v %q, want %s %q", tt.f.Name, row.Identifiers, row.Deprecated, tt.ids, tt.message)
		}
	}
}
