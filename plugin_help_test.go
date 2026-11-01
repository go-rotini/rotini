package rotini

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// pluginHelpDef is a program with a help command, a declared plugin, and plugin discovery under
// a group.
func pluginHelpDef() Definition {
	help := CommandDef{Name: "help", Handler: "AppRun", Arguments: []ArgDef{{Name: "command", Type: "[]string", Variadic: true}}}
	return Definition{
		Name: "app", Handler: "App",
		Plugins: []PluginDef{{Name: "sync", Aliases: []string{"sy"}, Binary: "app-sync"}},
		Commands: []CommandDef{help, {
			Name: "tools", Handler: "App", PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-tools-"},
			Commands: []CommandDef{help},
		}},
	}
}

// runPluginHelp runs args against pluginHelpDef, the help command answering with PluginHelp.
func runPluginHelp(t *testing.T, args ...string) (code int, err error, handled bool, stdout string) {
	t.Helper()
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		words := rtx.Argv[1:]
		if rtx.Argv[0] == "tools" {
			words = rtx.Argv[2:]
		}
		handled = rtx.PluginHelp(words...)
	}}
	p := NewProgram(pluginHelpDef(), h).WithArgs(args)
	var out strings.Builder
	p.stdout = &out
	p.stderr = &strings.Builder{}
	code, err = p.Run(p.args)
	return code, err, handled, out.String()
}

// TestContext_PluginHelp pins that a help command's PluginHelp runs a declared plugin, by name
// or alias, with its words and --help, and passes its output and exit code through.
func TestContext_PluginHelp(t *testing.T) {
	writeFakeBinary(t, "app-sync", "#!/bin/sh\necho \"sync got: $*\"\nexit 4\n")
	for _, word := range []string{"sync", "sy"} {
		code, _, handled, out := runPluginHelp(t, "help", word, "push")
		if !handled || code != 4 || strings.TrimSpace(out) != "sync got: push --help" {
			t.Errorf("help %s push: handled %v, code %d, stdout %q; want true, 4, the plugin's help", word, handled, code, out)
		}
	}
	if code, err, handled, out := runPluginHelp(t, "help", "nope"); handled || code != 0 || err != nil || out != "" {
		t.Errorf("help nope: handled %v, code %d, err %v, stdout %q; want nothing done", handled, code, err, out)
	}
	if _, _, handled, _ := runPluginHelp(t, "help"); handled {
		t.Error("help with no words ran a plugin")
	}
}

// TestContext_PluginHelpDiscovered pins that a discovered plugin counts only when its binary is
// found, under the command the help command sits under.
func TestContext_PluginHelpDiscovered(t *testing.T) {
	writeFakeBinary(t, "app-tools-lint", "#!/bin/sh\necho \"lint got: $*\"\n")
	code, _, handled, out := runPluginHelp(t, "tools", "help", "lint")
	if !handled || code != 0 || strings.TrimSpace(out) != "lint got: --help" {
		t.Errorf("tools help lint: handled %v, code %d, stdout %q", handled, code, out)
	}
	if _, _, handled, _ := runPluginHelp(t, "tools", "help", "missing"); handled {
		t.Error("a discovered word with no binary was treated as a plugin")
	}
}

// TestContext_PluginHelpFailure pins that a plugin PluginHelp can't run halts the run with the
// same *PluginError running it would give: here its timeout.
func TestContext_PluginHelpFailure(t *testing.T) {
	writeFakeBinary(t, "app-slow", "#!/bin/sh\nsleep 5\n")
	def := pluginHelpDef()
	def.Plugins = []PluginDef{{Name: "slow", Binary: "app-slow", Timeout: 50 * time.Millisecond}}
	var handled bool
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) { handled = rtx.PluginHelp(rtx.Argv[1:]...) }}
	p := NewProgram(def, h).WithArgs([]string{"help", "slow"})
	p.stdout, p.stderr = &strings.Builder{}, &strings.Builder{}
	code, err := p.Run(p.args)
	pe, ok := errors.AsType[*PluginError](err)
	if !handled || code != 1 || !ok || pe.Kind != PluginTimeout {
		t.Errorf("help slow: handled %v, code %d, err %v; want true, 1, a timeout", handled, code, err)
	}
	if NewContextFor(def, nil).PluginHelp("slow") {
		t.Error("a Context outside a run ran a plugin")
	}
}

// runUnknownHelp runs args against pluginHelpDef with pages for its commands, the help command
// answering as the generated one does: PluginHelp, else HelpFor, recording HelpFor's error.
func runUnknownHelp(t *testing.T, structured bool, args ...string) (code int, err error, stderr string) {
	t.Helper()
	def := pluginHelpDef()
	pages := func(path ...string) (string, error) {
		switch strings.Join(path, " ") {
		case "", "help", "tools", "tools help":
			return "page", nil
		}
		return "", UnknownCommandError(def, path...)
	}
	h := &testHandlers{log: new([]string), onRun: func(rtx *Context) {
		words := rtx.Argv[1:]
		if rtx.Argv[0] == "tools" {
			words = rtx.Argv[2:]
		}
		if rtx.PluginHelp(words...) {
			return
		}
		if _, err := rtx.HelpFor(words...); err != nil {
			rtx.RecordError(err)
		}
	}}
	p := NewProgram(def, h).WithArgs(args).WithHelp(pages)
	if structured {
		p.WithReporter(StructuredReporter(func(*Context) bool { return true }))
	}
	var errb strings.Builder
	p.stdout, p.stderr = &strings.Builder{}, &errb
	code, err = p.Run(p.args)
	return code, err, errb.String()
}

// TestContext_HelpForUnknownCommand pins that `help <word>` naming no command fails as typing
// the word as a command does: an unknown-command usage error reported against the deepest
// command the words reach, with the words accepted there as candidates.
func TestContext_HelpForUnknownCommand(t *testing.T) {
	cases := []struct {
		args       []string
		msg        string
		command    string
		token      string
		candidates []string
	}{
		{[]string{"help", "nope"}, `app: unknown command "nope"`, "app", "nope", []string{"help", "tools", "sync", "sy"}},
		{[]string{"help", "tools", "nope"}, `app tools: unknown command "nope"`, "tools", "nope", []string{"help"}},
		{[]string{"tools", "help", "nope"}, `app tools: unknown command "nope"`, "tools", "nope", []string{"help"}},
		{[]string{"help", "help", "extra"}, `app help: unknown command "extra"`, "help", "extra", nil},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, err, stderr := runUnknownHelp(t, false, tc.args...)
			pe, ok := errors.AsType[*ParseError](err)
			if code != 1 || !ok || !errors.Is(err, ErrUsage) || pe.Kind != ParseKindUnknownCommand {
				t.Fatalf("code %d, err %#v; want 1 and an unknown-command *ParseError", code, err)
			}
			if pe.Error() != tc.msg || pe.Command != tc.command || pe.Token != tc.token || !slices.Equal(pe.Candidates, tc.candidates) {
				t.Errorf("got %q, Command %q, Token %q, Candidates %q", pe.Error(), pe.Command, pe.Token, pe.Candidates)
			}
			if want := "Error: " + tc.msg + "\n"; stderr != want {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
		})
	}

	// The mistyped word reads the same typed as a command, which the parser rejects.
	chain, _ := resolveChain(pluginHelpDef(), []string{"nope"})
	direct := atCommand(strayCommand(&chain[0], &scopeInputs{args: []string{"nope"}}, nil), chain)
	if viaHelp := UnknownCommandError(pluginHelpDef(), "nope"); direct.Error() != viaHelp.Error() {
		t.Errorf("`app nope` = %v, `app help nope` = %v; want the same error", direct, viaHelp)
	}

	_, _, stderr := runUnknownHelp(t, true, "help", "nope")
	for _, want := range []string{`"kind":"unknown-command"`, `"category":"usage"`, `"token":"nope"`, `"message":"app: unknown command \"nope\""`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("structured report lacks %s:\n%s", want, stderr)
		}
	}
}

// TestUnknownCommandError pins the error's shape at the edges: help topics are candidates at
// the root only, a path whose every word names a command reports the last word, and a
// passthrough command stops the walk.
func TestUnknownCommandError(t *testing.T) {
	def := Definition{
		Name:   "app",
		Topics: []TopicDef{{Name: "env"}},
		Commands: []CommandDef{
			{Name: "db", Aliases: []string{"d"}, Commands: []CommandDef{{Name: "migrate"}}},
			{Name: "exec", Passthrough: true, Commands: []CommandDef{{Name: "inner"}}},
		},
	}
	cases := []struct {
		path       []string
		msg        string
		candidates []string
	}{
		{[]string{"nope"}, `app: unknown command "nope"`, []string{"db", "d", "exec", "env"}},
		{[]string{"d", "nope"}, `app db: unknown command "nope"`, []string{"migrate"}},
		{[]string{"db", "migrate"}, `app db: unknown command "migrate"`, []string{"migrate"}},
		{[]string{"exec", "inner"}, `app exec: unknown command "inner"`, []string{"inner"}},
	}
	for _, tc := range cases {
		err := UnknownCommandError(def, tc.path...)
		if err.Error() != tc.msg || err.Kind != ParseKindUnknownCommand || !slices.Equal(err.Candidates, tc.candidates) {
			t.Errorf("UnknownCommandError(%q) = %q, %v, %q; want %q, unknown-command, %q", tc.path, err, err.Kind, err.Candidates, tc.msg, tc.candidates)
		}
	}
}
