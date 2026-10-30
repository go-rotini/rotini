package rotini

import (
	"errors"
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
