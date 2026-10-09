package codegen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRoffEscape pins the escaping that keeps spec text inert on a roff page. Each case is text
// that, unescaped, would be read as markup or would print wrongly.
func TestRoffEscape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain words", "plain words"},
		{".TH injected", `\&.TH injected`},
		{"'quoted at the start", `\(aqquoted at the start`},
		{`a \fB backslash`, `a \efB backslash`},
		{"--verbose and -v", `\-\-verbose and \-v`},
		{"it's `code`", `it\(aqs \(gacode\(ga`},
		{"café ✓ 日本", `caf\[u00E9] \[u2713] \[u65E5]\[u672C]`},
		{"tab\there", "tab here"},
		{"bell\x07gone", "bellgone"},
		{"..", `\&..`},
		{"", ""},
	} {
		if got := roffEscapeLine(tc.in); got != tc.want {
			t.Errorf("roffEscapeLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if got, want := roffInline("two\nlines  and   spaces"), "two lines and spaces"; got != want {
		t.Errorf("roffInline joins lines: got %q, want %q", got, want)
	}
	if got, want := roffInline("x\n.SH oops"), "x .SH oops"; got != want {
		t.Errorf("roffInline: a dot after a joined line break is not at a line start: got %q, want %q", got, want)
	}
	if got, want := roffArg(`say "hi"`), `"say \(dqhi\(dq"`; got != want {
		t.Errorf("roffArg = %q, want %q", got, want)
	}
	if got, want := roffBlock("first para\nstill first\n\n\n.second para"), "first para\nstill first\n.PP\n\\&.second para"; got != want {
		t.Errorf("roffBlock = %q, want %q", got, want)
	}
	if got, want := roffBlock("Install it:\n\n  bash   source it\n  zsh    save it\nThen go."), "Install it:\n.PP\n.nf\n  bash   source it\n  zsh    save it\n.fi\nThen go."; got != want {
		t.Errorf("roffBlock keeps indented lines as laid out: got %q, want %q", got, want)
	}
	if got, want := roffLines("$ app --x\n.dot\n"), "$ app \\-\\-x\n\\&.dot"; got != want {
		t.Errorf("roffLines = %q, want %q", got, want)
	}
}

// TestWrapRoffText pins that a long filled line breaks at spaces and that a piece starting with
// "." is escaped as text.
func TestWrapRoffText(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  []string
	}{
		{"short", 10, []string{"short"}},
		{"aaa bbb ccc ddd", 8, []string{"aaa bbb", "ccc ddd"}},
		{"aaa .bbb", 4, []string{"aaa", `\&.bbb`}},
		{"averyveryverylongword x", 5, []string{"averyveryverylongword", "x"}},
	} {
		if got := wrapRoffText(tc.in, tc.width); !slices.Equal(got, tc.want) {
			t.Errorf("wrapRoffText(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

// manSpec is the feature fixture with text chosen to break a careless page: a description with
// paragraphs, a line that starts with a dot, backslashes, quotes and non-ASCII.
const manSpec = `version: 0.0.0
command:
  name: acme
  summary: acme control — the "acme" cli
  description: |
    The acme control cli. It ships \backslashes\ and 'quotes'. This sentence runs on well past eighty bytes so that the renderer has to wrap it into more than one roff text line .with a dot.

    .this line starts with a dot, and -dash --options too.
    Ünïcödé is fine.
  examples:
    - acme deploy web --replicas 3
    - |
      # a two-line example
      acme status --verbose
  env_prefix: ACME
  exit_status:
    - code: 0
      summary: success
    - code: 2
      summary: usage error
    - code: 3
      summary: some deploys failed
      output: {type: array, items: {$ref: "#/schemas/Status"}}
  schemas:
    Status:
      type: object
      required: [service]
      properties:
        service: {type: string, description: the service .name}
        healthy: {type: boolean, description: whether it answers its \\health check}
  see_also:
    - git(1)
  flags:
    - name: verbose
      summary: verbose output
      identifiers: [--verbose, -v]
      cascading: true
      schema: {type: bool}
    - name: color
      summary: when to color
      identifiers: [--color]
      schema: {type: string, enum: [auto, always, never], default: auto, implicit_value: always}
  env:
    - name: token
      summary: api token
      schema: {type: string, variable: ACME_TOKEN, secret: true}
  config:
    - name: endpoint
      summary: api endpoint
      schema: {type: string, default: https://api.acme.test}
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
          group: tuning
          schema: {type: int, minimum: 1, maximum: 10, default: 1}
      output: {$ref: "#/schemas/Status"}
    - name: status
      summary: show status
      flags:
        - name: format
          summary: output format
          identifiers: [-o, --format]
          schema: {type: string, default: text, enum: [text, json, yaml]}
      output:
        type: array
        description: One status per service.
        items: {$ref: "#/schemas/Status"}
    - name: secret
      summary: hidden helper
      hidden: true
`

const manConfEmbed = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/acme/main.go
      package: main
    - type: cmd
      file: internal/cmd/acme/zz_acme.go
      package: acme
  features:
    - type: man
      enabled: true
      embed: true
`

// manPages reads every rendered man page from the module's renders directory, by file name.
func manPages(t *testing.T, dir string) map[string]string {
	t.Helper()
	renders := filepath.Join(dir, "internal", "cmd", "acme", "renders")
	entries, err := os.ReadDir(renders)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(renders, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		pages[e.Name()] = string(b)
	}
	return pages
}

// TestManPages_shape pins the page rotini renders: the header, the NAME line apropos indexes,
// each section, escaping, and the cross-references between pages.
func TestManPages_shape(t *testing.T) {
	dir, _ := emitModule(t, manSpec, manConfEmbed)
	pages := manPages(t, dir)

	for _, name := range []string{"acme.1", "acme-deploy.1", "acme-status.1", "acme-secret.1"} {
		if _, ok := pages[name]; !ok {
			t.Fatalf("no page %s; have %v", name, keysOf(pages))
		}
	}
	root := pages["acme.1"]
	for _, want := range []string{
		`.TH "ACME" 1 "" "acme" "User Commands"`,
		".SH NAME\nacme \\- acme control \\[u2014] the \"acme\" cli",
		".SH DESCRIPTION\nThe acme control cli. It ships \\ebackslashes\\e and \\(aqquotes\\(aq. This sentence\n" +
			"runs on well past eighty bytes so that the renderer has to wrap it into more\n" +
			"than one roff text line .with a dot.\n.PP\n\\&.this line starts",
		`.SH "GLOBAL OPTIONS"`, // no: the root's own cascading flag is an option here
		".SH ENVIRONMENT\n.TP\n\\fBACME_TOKEN\\fR \\fIstring\\fR\napi token",
		".SH \"EXIT STATUS\"\n.TP\n\\fB0\\fR\nsuccess",
		".SH EXAMPLES\n.RS 4\n.nf\nacme deploy web \\-\\-replicas 3\n.sp\n# a two\\-line example\nacme status \\-\\-verbose\n.fi\n.RE",
		".SH \"SEE ALSO\"\n\\fBacme\\-deploy\\fR(1), \\fBacme\\-status\\fR(1), git(1)",
		"\\fB\\-\\-color[=string]\\fR\nwhen to color (default auto) [auto|always|never] (implicit: always)",
	} {
		if want == `.SH "GLOBAL OPTIONS"` {
			if strings.Contains(root, want) {
				t.Errorf("the root page has a GLOBAL OPTIONS section; its cascading flags are its own options:\n%s", root)
			}
			continue
		}
		if !strings.Contains(root, want) {
			t.Errorf("acme.1 lacks %q:\n%s", want, root)
		}
	}
	if strings.Contains(root, "acme\\-secret") {
		t.Errorf("acme.1 cross-references the hidden command:\n%s", root)
	}

	deploy := pages["acme-deploy.1"]
	for _, want := range []string{
		".SH SYNOPSIS\n\\fBacme deploy\\fR [flags] <service> [rest...]",
		".SH ARGUMENTS\n.TP\n\\fI<service>\\fR\nservice to deploy [web|api]",
		".SH \"TUNING\"\n.TP\n\\fB\\-r\\fR, \\fB\\-\\-replicas\\fR \\fIint\\fR\nreplica count (default 1)",
		".SH \"GLOBAL OPTIONS\"\n.TP\n\\fB\\-v\\fR, \\fB\\-\\-verbose\\fR\nverbose output",
		".SH \"SEE ALSO\"\n\\fBacme\\fR(1)",
	} {
		if !strings.Contains(deploy, want) {
			t.Errorf("acme-deploy.1 lacks %q:\n%s", want, deploy)
		}
	}
	for name, page := range pages {
		if !strings.HasSuffix(page, "\n") || strings.Contains(page, "\n\n") {
			t.Errorf("%s must end with one newline and hold no blank lines:\n%q", name, page)
		}
	}
}

// TestManPages_lintClean runs every rendered page through mandoc or groff, skipping when
// neither is installed. mandoc's "missing date" warning is allowed: the date is empty unless
// SOURCE_DATE_EPOCH sets it.
func TestManPages_lintClean(t *testing.T) {
	dir, _ := emitModule(t, manSpec, manConfEmbed)
	pages := manPages(t, dir)
	renders := filepath.Join(dir, "internal", "cmd", "acme", "renders")

	lint := func(path string) (string, bool) {
		if bin, err := exec.LookPath("mandoc"); err == nil {
			out, _ := exec.Command(bin, "-Tlint", "-W", "all", path).CombinedOutput()
			return string(out), true
		}
		if bin, err := exec.LookPath("groff"); err == nil {
			out, _ := exec.Command(bin, "-man", "-Tutf8", "-ww", "-z", path).CombinedOutput()
			return string(out), true
		}
		return "", false
	}
	for name := range pages {
		out, ok := lint(filepath.Join(renders, name))
		if !ok {
			t.Skip("neither mandoc nor groff is installed")
		}
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			if line == "" || strings.Contains(line, "missing date") {
				continue
			}
			t.Errorf("%s: %s", name, line)
		}
	}
}

// TestManPages_section pins that a conf section sets the header section, the file extension,
// the cross-reference sections and the ManSection constant.
func TestManPages_section(t *testing.T) {
	conf := strings.Replace(manConfEmbed, "      embed: true\n", "      embed: true\n      section: 8\n", 1)
	dir, files := emitModule(t, manSpec, conf)
	pages := manPages(t, dir)

	page, ok := pages["acme-deploy.8"]
	if !ok {
		t.Fatalf("no acme-deploy.8; have %v", keysOf(pages))
	}
	if !strings.HasPrefix(page, `.TH "ACME\-DEPLOY" 8 `) || !strings.Contains(page, `\fBacme\fR(8)`) {
		t.Errorf("section 8 is not in the header or the cross-reference:\n%s", page)
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".1") {
			t.Errorf("a section-1 page was written: %s", f)
		}
	}
	if gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go"); !strings.Contains(gen, `const ManSection = "8"`) {
		t.Error("the generated file does not declare ManSection = \"8\"")
	}
}

// TestManPages_sourceDateEpoch pins that SOURCE_DATE_EPOCH sets the header date.
func TestManPages_sourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1759276800") // 2025-10-01T00:00:00Z
	dir, _ := emitModule(t, manSpec, manConfEmbed)
	if page := manPages(t, dir)["acme.1"]; !strings.HasPrefix(page, `.TH "ACME" 1 "2025-10-01" "acme" `) {
		t.Errorf("the header does not carry SOURCE_DATE_EPOCH's date:\n%s", page)
	}
}

// TestManPages_nameCollision pins that validate reports two commands sharing a man page name,
// placed in the spec, and only when the man feature is on.
func TestManPages_nameCollision(t *testing.T) {
	// Two commands that differ only in case. A dash collision (`tag-remove` against
	// `tag remove`) is refused earlier by the handler-file rule.
	const spec = `version: 0.0.0
command:
  name: notes
  commands:
    - name: Remove
    - name: remove
`
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/notes\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", strings.ReplaceAll(manConfEmbed, "acme", "notes"))
	t.Chdir(dir)

	var got error
	if err := NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", func(_ string, err error) { got = err }, func([]error) {}); err != nil && got == nil {
		got = err
	}
	if got == nil || !strings.Contains(got.Error(), `"notes-remove"`) ||
		!strings.Contains(got.Error(), "notes Remove") || !strings.Contains(got.Error(), "notes remove") {
		t.Fatalf("validate = %v, want a man page name collision naming both commands", got)
	}
	if !strings.Contains(got.Error(), ".rotini.spec.yaml:") {
		t.Errorf("the collision is not placed in the spec: %v", got)
	}

	off := strings.Replace(strings.ReplaceAll(manConfEmbed, "acme", "notes"), "enabled: true", "enabled: false", 1)
	writeTestFile(t, dir, ".rotini.conf.yaml", off)
	got = nil
	_ = NewProcessor("0.0.0").Validate(".rotini.spec.yaml", ".rotini.conf.yaml", false, "", func(_ string, err error) { got = err }, func([]error) {})
	if got != nil {
		t.Errorf("with man off, validate = %v, want no problem", got)
	}
}

// TestManPages_pruning pins that generate removes pre-v1.2.0 man_*.txt pages and pages from a
// previous section, and leaves other files in renders/ alone.
func TestManPages_pruning(t *testing.T) {
	dir, _ := emitModule(t, manSpec, manConfEmbed)
	renders := filepath.Join(dir, "internal", "cmd", "acme", "renders")
	for _, f := range []string{"man_acme.txt", "man_acme_deploy.txt", "acme-gone.1", "notes.txt", "other-tool.1", "acme.md"} {
		writeTestFile(t, renders, f, "x")
	}

	gen := func(conf string) {
		t.Helper()
		writeTestFile(t, dir, ".rotini.conf.yaml", conf)
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
			if e != nil {
				t.Errorf("Generate: %v", e)
			}
		}, func([]error) {}); err != nil {
			t.Fatal(err)
		}
	}
	gen(manConfEmbed)
	have := keysOf(manPages(t, dir))
	for _, gone := range []string{"man_acme.txt", "man_acme_deploy.txt", "acme-gone.1"} {
		if slices.Contains(have, gone) {
			t.Errorf("%s was not pruned; have %v", gone, have)
		}
	}
	for _, kept := range []string{"notes.txt", "other-tool.1", "acme.md", "acme.1", "acme-deploy.1"} {
		if !slices.Contains(have, kept) {
			t.Errorf("%s was removed; have %v", kept, have)
		}
	}

	gen(strings.Replace(manConfEmbed, "      embed: true\n", "      embed: true\n      section: 8\n", 1))
	have = keysOf(manPages(t, dir))
	if slices.Contains(have, "acme.1") || !slices.Contains(have, "acme.8") || !slices.Contains(have, "other-tool.1") {
		t.Errorf("after the section change: %v; want acme.8, no acme.1, and other-tool.1 kept", have)
	}
}

// TestManPages_verbatimIsExact pins that a command's `man:` page is written byte-for-byte.
func TestManPages_verbatimIsExact(t *testing.T) {
	const page = ".TH CUSTOM 1\n.SH NAME\ncustom \\- written by hand\n"
	spec := strings.Replace(manSpec, "    - name: status\n      summary: show status\n",
		"    - name: status\n      summary: show status\n      man: "+strconvQuote(page)+"\n", 1)
	dir, _ := emitModule(t, spec, manConfEmbed)
	if got := manPages(t, dir)["acme-status.1"]; got != page {
		t.Errorf("verbatim page = %q, want %q", got, page)
	}
}

// pageListsTest runs inside a generated module: ManPages and MarkdownPages list every visible
// command once, in tree order, under its man page name, with the content the resolvers return.
const pageListsTest = `package acme

import (
	"slices"
	"testing"
)

func TestPageLists(t *testing.T) {
	want := []string{"acme", "acme-deploy", "acme-status"} // the hidden "secret" is left out
	wantPaths := [][]string{nil, {"deploy"}, {"status"}}

	var names []string
	for i, p := range ManPages() {
		names = append(names, p.Name)
		if !slices.Equal(p.Path, wantPaths[i]) {
			t.Errorf("ManPages()[%d].Path = %q, want %q", i, p.Path, wantPaths[i])
		}
		if got, err := Man(p.Path...); err != nil || got != p.Content {
			t.Errorf("Man(%q) disagrees with ManPages: %v", p.Path, err)
		}
	}
	if !slices.Equal(names, want) {
		t.Errorf("ManPages names = %q, want %q", names, want)
	}

	names = nil
	for _, p := range MarkdownPages() {
		names = append(names, p.Name)
		if got, err := Markdown(p.Path...); err != nil || got != p.Content {
			t.Errorf("Markdown(%q) disagrees with MarkdownPages: %v", p.Path, err)
		}
	}
	if !slices.Equal(names, want) {
		t.Errorf("MarkdownPages names = %q, want %q", names, want)
	}
	if ManSection != "1" {
		t.Errorf("ManSection = %q, want 1", ManSection)
	}
}
`

// TestPageLists runs pageListsTest inside the generated module, in both embed and inline mode.
func TestPageLists(t *testing.T) {
	skipUnlessCompiling(t)
	if testing.Short() {
		t.Skip("compiles and tests the generated module; skipped under -short")
	}
	for _, embed := range []string{"true", "false"} {
		t.Run("embed="+embed, func(t *testing.T) {
			conf := strings.Replace(manConfEmbed, "      embed: true\n",
				"      embed: "+embed+"\n    - type: markdown\n      enabled: true\n      embed: "+embed+"\n", 1)
			dir, _ := emitModule(t, manSpec, conf)
			writeTestFile(t, filepath.Join(dir, "internal", "cmd", "acme"), "pages_test.go", pageListsTest)
			for _, args := range [][]string{{"mod", "tidy"}, {"test", "./internal/cmd/acme"}} {
				cmd := exec.Command("go", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
				}
			}
		})
	}
}

// TestPageLists_onlyForEnabledFeatures pins that a page list is generated only for an enabled
// man or markdown feature, never for help.
func TestPageLists_onlyForEnabledFeatures(t *testing.T) {
	dir, _ := emitModule(t, manSpec, manConfEmbed)
	gen := readEmitted(t, dir, "internal/cmd/acme/zz_acme.go")
	if !strings.Contains(gen, "func ManPages() []rotini.Page") {
		t.Error("ManPages was not generated with man on")
	}
	if strings.Contains(gen, "MarkdownPages") || strings.Contains(gen, "HelpPages") {
		t.Error("a page list was generated for a feature that is off or has none")
	}
}

func strconvQuote(s string) string {
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
