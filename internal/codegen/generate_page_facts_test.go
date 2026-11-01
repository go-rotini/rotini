package codegen

import (
	"slices"
	"strings"
	"testing"
)

// TestFlagRules_text pins each rule kind's sentence, and that a rule naming a hidden flag is
// left out with the flag.
func TestFlagRules_text(t *testing.T) {
	flag := func(name string, hidden bool) FlagInput {
		return FlagInput{Name: name, Identifiers: []string{"--" + name}, Hidden: hidden}
	}
	in := &Inputs{
		Flags: []FlagInput{flag("json", false), flag("yaml", false), flag("tls", false), flag("cert", false), flag("insecure", false), flag("format", false), flag("debug", true)},
		FlagGroups: []FlagGroup{
			{Kind: "mutually_exclusive", Flags: []string{"json", "yaml"}},
			{Kind: "required_together", Flags: []string{"tls", "cert"}},
			{Kind: "one_of", Flags: []string{"json", "yaml"}},
			{Kind: "at_least_one", Flags: []string{"json", "yaml", "format"}},
			{Kind: "mutually_exclusive", Flags: []string{"json", "debug"}},
		},
		FlagDependencies: []FlagDependency{
			{When: "tls", Requires: []string{"cert"}, Forbids: []string{"insecure"}},
			{When: "format", Equals: []any{"csv", "tsv"}, Requires: []string{"json", "yaml"}},
			{Unless: []string{"json"}, Requires: []string{"cert"}},
			{When: "debug", Requires: []string{"cert"}},
		},
	}
	var got []string
	for _, r := range flagRules(in) {
		got = append(got, r.Kind+": "+r.Text)
	}
	want := []string{
		"mutually_exclusive: --json and --yaml can't be used together",
		"required_together: --tls and --cert must be used together",
		"one_of: exactly one of --json or --yaml is required",
		"at_least_one: at least one of --json, --yaml or --format is required",
		"dependency: with --tls, --cert is required and --insecure can't be used",
		"dependency: with --format csv or tsv, --json and --yaml are required",
		"dependency: unless --json is given, --cert is required",
	}
	if !slices.Equal(got, want) {
		t.Errorf("flagRules =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestFileNote pins how each kind of configuration file is described on a page.
func TestFileNote(t *testing.T) {
	gp := &program{rootName: "app", envPrefix: "APP"}
	for _, tc := range []struct {
		cf         ConfigurationFile
		file, note string
	}{
		{ConfigurationFile{Name: "a", Path: "./app.yaml"}, "./app.yaml", ""},
		{ConfigurationFile{Name: "b", Path: "./app.conf", Format: "toml"}, "./app.conf", "toml"},
		{ConfigurationFile{Name: "c", Discover: &ConfigurationFileDiscover{Strategy: "xdg", App: "app", File: "config.yaml"}}, "app/config.yaml", "in $XDG_CONFIG_HOME (default ~/.config)"},
		{ConfigurationFile{Name: "d", Discover: &ConfigurationFileDiscover{Strategy: "native", App: "app", File: "config.yaml"}}, "app/config.yaml", "in the platform's configuration directory"},
		{ConfigurationFile{Name: "e", Discover: &ConfigurationFileDiscover{Strategy: "xdg-system", App: "app", File: "config.yaml"}}, "app/config.yaml", "in each $XDG_CONFIG_DIRS directory (default /etc/xdg)"},
		{ConfigurationFile{Name: "f", Path: ".env", As: "env"}, ".env", "read as environment variables"},
	} {
		row := gp.fileRow(scopedConfigFile{ConfigurationFile: tc.cf, Scope: "app"}, nil)
		if row.File != tc.file || row.Note != tc.note {
			t.Errorf("%s: file %q note %q, want %q and %q", tc.cf.Name, row.File, row.Note, tc.file, tc.note)
		}
	}
}

// TestNotesText pins the one parenthetical a row's notes share.
func TestNotesText(t *testing.T) {
	for _, tc := range []struct {
		notes []any
		want  string
	}{
		{nil, ""},
		{[]any{"", false}, ""},
		{[]any{"default 1", "", "1..100"}, " (default 1, 1..100)"},
		{[]any{false, "required"}, " (required)"},
	} {
		if got := notesText(tc.notes...); got != tc.want {
			t.Errorf("notesText(%v) = %q, want %q", tc.notes, got, tc.want)
		}
	}
}

// TestTopics_generatedTwice pins that a second topic generating the same page is an error, for
// the configuration page as for the environment page.
func TestTopics_generatedTwice(t *testing.T) {
	spec := decodeSpecYAML(t, `version: 0.0.0
command:
  name: app
  topics:
    - {name: config, summary: the files, generate: configuration}
    - {name: settings, summary: the files again, generate: configuration}
`)
	var msgs []string
	for _, e := range lintTopics(spec) {
		msgs = append(msgs, e.Error())
	}
	if !slices.ContainsFunc(msgs, func(m string) bool { return strings.Contains(m, "generates the configuration page a second time") }) {
		t.Errorf("no problem for the second configuration topic:\n%s", strings.Join(msgs, "\n"))
	}
}
