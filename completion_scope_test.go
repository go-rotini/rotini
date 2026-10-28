package rotini

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
)

// mountedDef is a program with a cli mounted below it, the way a $ref composes one: the root
// and the mounted kit each have a help command, and the root has topics.
func mountedDef() Definition {
	helpArg := []ArgDef{{Name: "command", Type: "[]string", Variadic: true, Complete: Completion{Kind: "command", Message: "a command"}}}
	return Definition{
		Name: "app", Handler: "App",
		Topics: []TopicDef{{Name: "filters", Summary: "how filters work"}},
		Commands: []CommandDef{
			{Name: "help", Handler: "AppHelp", Arguments: helpArg},
			{Name: "status", Handler: "AppStatus"},
			{Name: "kit", Handler: "AppKit", Commands: []CommandDef{
				{Name: "help", Handler: "AppKitHelp", Arguments: helpArg},
				{Name: "build", Handler: "AppKitBuild", Aliases: []string{"b"}},
				{Name: "pkg", Handler: "AppKitPkg", Commands: []CommandDef{
					{Name: "add", Handler: "AppKitPkgAdd"},
				}},
			}},
		},
	}
}

// A help command's path starts at the command it sits under, as rtx.HelpFor reads it, so a
// mounted cli's help completes its own commands; topics are the root's only.
func TestComplete_helpScope(t *testing.T) {
	def := mountedDef()
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"help", ""}, []string{"filters", "help", "kit", "status"}},
		{[]string{"help", "kit", ""}, []string{"b", "build", "help", "pkg"}},
		{[]string{"kit", "help", ""}, []string{"b", "build", "help", "pkg"}},
		{[]string{"kit", "help", "p"}, []string{"pkg"}},
		{[]string{"kit", "help", "pkg", ""}, []string{"add"}},
		{[]string{"kit", "help", "b", ""}, nil},
		{[]string{"kit", "help", "status", ""}, nil},
		{[]string{"kit", "help", "f"}, nil},
	} {
		var got []string
		for _, c := range complete(def, tc.words, nil, nil) {
			got = append(got, candidateName(c))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("complete(%q) = %v, want %v", tc.words, got, tc.want)
		}
	}
	// The path's message is for a path that can still grow.
	if got := completeWire(t, def, nil, nil, "kit", "help", "pkg", "add", ""); got != ":rotini:none\n" {
		t.Errorf("a complete path = %q, want none and no message", got)
	}
}

// Where the command line can take no further word, nothing is offered, not even files.
func TestComplete_nothingMoreFits(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{
			{Name: "status", Handler: "AppStatus"},
			{Name: "logs", Handler: "AppLogs", Arguments: []ArgDef{{Name: "svc", Type: "string"}}},
			{Name: "open", Handler: "AppOpen", Arguments: []ArgDef{{Name: "path", Type: "string", Complete: Completion{Kind: "file"}}}},
		},
	}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"status", ""}, ":rotini:none\n"},
		{[]string{"nope"}, ":rotini:none\n"},
		{[]string{"logs", "api", ""}, ":rotini:none\n"},
		{[]string{"logs", ""}, ""},               // a free-text argument: the shell's default
		{[]string{"open", ""}, ":rotini:file\n"}, // the argument's own hint
		{[]string{"st"}, "status\n"},             // candidates: no directive beside them
		{[]string{"logs", "--", "a", ""}, ":rotini:none\n"},
	} {
		if got := completeWire(t, def, nil, nil, tc.words...); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.words, got, tc.want)
		}
	}
}

// valuesDef has value-taking short flags, a list with a separator, a from:file flag and
// argument, and a shell-completed flag.
func valuesDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "env", Identifiers: []string{"-e", "--env"}, Type: "string", Enum: []string{"dev", "prod"}},
			{Name: "tag", Identifiers: []string{"-t", "--tag"}, Type: "[]string", Separator: ",", Enum: []string{"blue", "green"}},
			{Name: "token", Identifiers: []string{"--token"}, Type: "string", From: []string{"file"}, Complete: Completion{Kind: "none"}},
			{Name: "dir", Identifiers: []string{"-C", "--dir"}, Type: "string", Complete: Completion{Kind: "directory"}},
		},
		Arguments: []ArgDef{{Name: "src", Type: "string", From: []string{"file"}}},
	}
}

// A value attached to a short flag completes as the parser reads it: the cluster's
// value-taking flag takes the rest of the word.
func TestComplete_attachedShortValues(t *testing.T) {
	def := valuesDef()
	for _, tc := range []struct {
		word string
		want []string
	}{
		{"-ep", []string{"-eprod"}},
		{"-vep", []string{"-veprod"}},
		{"-ve=", []string{"-ve=dev", "-ve=prod"}},
		{"-e=d", []string{"-e=dev"}},
		{"-tblue,", []string{"-tblue,green"}},
		{"-v", []string{"-v"}},
		{"-vz", nil},
	} {
		if got := complete(def, []string{tc.word}, nil, nil); !slices.Equal(got, tc.want) {
			t.Errorf("complete(%q) = %v, want %v", tc.word, got, tc.want)
		}
	}
	// The shell completes a directory after "-C=", but can't find where "-C/tm" starts.
	if got := completeWire(t, def, nil, nil, "-C=/t"); got != ":rotini:directory\n" {
		t.Errorf("-C=/t = %q, want the directory hint", got)
	}
	if got := completeWire(t, def, nil, nil, "-C/t"); got != ":rotini:none\n" {
		t.Errorf("-C/t = %q, want none", got)
	}
}

// An "@" on a from:file input starts a path, whatever the input's own hint.
func TestComplete_atFileFallsBackToFiles(t *testing.T) {
	def := valuesDef()
	for _, words := range [][]string{{"--token", "@"}, {"--token=@"}, {"--token", "@sub/"}, {"@"}} {
		if got := completeWire(t, def, nil, nil, words...); got != ":rotini:file\n" {
			t.Errorf("%q = %q, want the file hint", words, got)
		}
	}
	if got := completeWire(t, def, nil, nil, "--token", ""); got != ":rotini:none\n" {
		t.Errorf("--token '' = %q, want the declared none", got)
	}
}

// A completer's writes to rtx.Stdout can't reach the completion answer.
func TestComplete_completerStdoutStaysOut(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "svc"}}}
	lookup := func(string) (Handler, bool) { return noisyCompleter{}, true }
	var out, errOut bytes.Buffer
	p := NewProgramFunc(def, lookup).WithStdout(&out).WithStderr(&errOut)
	if _, err := p.Complete([]string{""}, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "api\n" {
		t.Errorf("answer = %q, want only the candidate", got)
	}
	if got := errOut.String(); got != "debugging\n" {
		t.Errorf("stderr = %q, want the completer's write", got)
	}
}

type noisyCompleter struct{ NoHooks }

func (noisyCompleter) Run(context.Context, *Context) {}

func (noisyCompleter) CompleteArgValue(rtx *Context, _, _ string) []string {
	fmt.Fprintln(rtx.Stdout, "debugging")
	return []string{"api"}
}
