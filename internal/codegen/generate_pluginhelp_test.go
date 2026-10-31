package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pluginHelpConf = "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/%s/zz_%s.go\n  features:\n    - type: help\n      enabled: true\n"

const helpCmd = "    - name: help\n      arguments:\n        - name: command\n          schema: { type: '[]string' }\n"

// generateStaged writes files into a fresh module and generates the named specs in order,
// returning everything emitted and each generate's notices.
func generateStaged(t *testing.T, files map[string]string, order ...string) (map[string]string, map[string][]error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for path, body := range files {
		writeTestFile(t, dir, path, body)
	}
	t.Chdir(dir)
	notices := map[string][]error{}
	for _, name := range order {
		if _, err := os.Stat(filepath.Join(dir, "cmd", name, ".rotini.conf.yaml")); err != nil {
			writeTestFile(t, dir, "cmd/"+name+"/.rotini.conf.yaml", strings.ReplaceAll(pluginHelpConf, "%s", name))
		}
		err := NewProcessor("0.0.0").Generate("cmd/"+name+"/.rotini.spec.yaml", "cmd/"+name+"/.rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices[name] = append(notices[name], n...) })
		if err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
	}
	return collectGoFiles(t, dir), notices
}

// TestStub_helpPluginHelpOnlyWherePlugins pins that a seeded help command asks PluginHelp only
// when the command it sits under, or a command below that, declares a plugin or plugin
// discovery, composed children's included.
func TestStub_helpPluginHelpOnlyWherePlugins(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		order []string
		stub  string
		want  bool
	}{
		{
			name:  "no plugins",
			files: map[string]string{"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" + helpCmd},
			order: []string{"root"}, stub: "internal/cmd/root/root_help.go", want: false,
		},
		{
			name: "plugin discovery on the root",
			files: map[string]string{"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n  plugin_discovery: {}\n" +
				"  commands:\n" + helpCmd},
			order: []string{"root"}, stub: "internal/cmd/root/root_help.go", want: true,
		},
		{
			name: "plugin on a command below",
			files: map[string]string{"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" + helpCmd +
				"    - name: tools\n      plugins:\n        - {name: lint}\n"},
			order: []string{"root"}, stub: "internal/cmd/root/root_help.go", want: true,
		},
		{
			name: "plugin on a sibling group only",
			files: map[string]string{"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" +
				"    - name: a\n      commands:\n  " + strings.ReplaceAll(helpCmd, "\n    ", "\n      ") +
				"    - name: b\n      plugins:\n        - {name: lint}\n"},
			order: []string{"root"}, stub: "internal/cmd/root/root_a_help.go", want: false,
		},
		{
			name: "plugin in a composed child",
			files: map[string]string{
				"cmd/child/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: child\n  plugins:\n    - {name: lint}\n",
				"cmd/root/.rotini.spec.yaml":  "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" + helpCmd + "    - $ref: ../child/.rotini.spec.yaml\n",
			},
			order: []string{"child", "root"}, stub: "internal/cmd/root/root_help.go", want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emitted, _ := generateStaged(t, tc.files, tc.order...)
			stub, ok := emitted[tc.stub]
			if !ok {
				t.Fatalf("no stub %s in %v", tc.stub, keysOf(emitted))
			}
			if got := strings.Contains(stub, "rtx.PluginHelp("); got != tc.want {
				t.Errorf("stub calls PluginHelp: %v, want %v\n%s", got, tc.want, stub)
			}
			if !strings.Contains(stub, "rtx.HelpFor(") {
				t.Errorf("stub doesn't call HelpFor:\n%s", stub)
			}
		})
	}
}

// TestAudit_helpWithoutPluginHelp pins the notice for a help handler that can name a plugin but
// never asks PluginHelp, as a stub seeded before the spec declared a plugin is, and that a
// handler asking it gets none.
func TestAudit_helpWithoutPluginHelp(t *testing.T) {
	bare := "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" + helpCmd
	emitted, notices := generateStaged(t, map[string]string{"cmd/root/.rotini.spec.yaml": bare}, "root")
	if strings.Contains(emitted["internal/cmd/root/root_help.go"], "PluginHelp") {
		t.Fatal("a help stub with no plugins in reach asks PluginHelp")
	}
	if len(notices["root"]) != 0 {
		t.Errorf("notices without plugins: %v", notices["root"])
	}

	// The spec gains a plugin; the stub, create-once, stays as it was.
	writeTestFile(t, ".", "cmd/root/.rotini.spec.yaml", bare+"  plugins:\n    - {name: lint}\n")
	var got []error
	if err := NewProcessor("0.0.0").Generate("cmd/root/.rotini.spec.yaml", "cmd/root/.rotini.conf.yaml", false,
		func(string, error) {}, func(n []error) { got = append(got, n...) }); err != nil {
		t.Fatal(err)
	}
	joined := errors.Join(got...)
	if joined == nil || !strings.Contains(joined.Error(), "internal/cmd/root/root_help.go:") ||
		!strings.Contains(joined.Error(), "`root help` can name a plugin, but its Run calls rtx.HelpFor without rtx.PluginHelp") {
		t.Errorf("notices %v, want the missing PluginHelp warning for root_help.go", got)
	}

	// A fresh stub, seeded with the plugin declared, asks it, and draws no warning.
	if err := os.Remove("internal/cmd/root/root_help.go"); err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := NewProcessor("0.0.0").Generate("cmd/root/.rotini.spec.yaml", "cmd/root/.rotini.conf.yaml", false,
		func(string, error) {}, func(n []error) { got = append(got, n...) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("notices after reseeding: %v", got)
	}
	body, err := os.ReadFile("internal/cmd/root/root_help.go")
	if err != nil || !strings.Contains(string(body), "rtx.PluginHelp(") {
		t.Errorf("the reseeded stub doesn't ask PluginHelp (%v):\n%s", err, body)
	}
}

// TestCompose_helpMissesAddedPlugins pins the notice for a help command mounted from a composed
// spec when the commands added beside its $ref bring the only plugins under it: its handler was
// seeded from the composed spec alone.
func TestCompose_helpMissesAddedPlugins(t *testing.T) {
	child := "version: 0.0.0\ncommand:\n  name: child\n  commands:\n" + helpCmd
	cases := []struct {
		name, root string
		want       bool
	}{
		{"added plugin", "    - $ref: ../child/.rotini.spec.yaml\n      commands:\n        - name: extra\n          plugins:\n            - {name: lint}\n", true},
		{"added command without plugins", "    - $ref: ../child/.rotini.spec.yaml\n      commands:\n        - name: extra\n", false},
		{"no additions", "    - $ref: ../child/.rotini.spec.yaml\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emitted, notices := generateStaged(t, map[string]string{
				"cmd/child/.rotini.spec.yaml": child,
				"cmd/root/.rotini.spec.yaml":  "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" + tc.root,
			}, "child", "root")
			if strings.Contains(emitted["internal/cmd/child/child_help.go"], "PluginHelp") {
				t.Error("the child's help stub asks PluginHelp with no plugin in the child")
			}
			joined := errors.Join(notices["root"]...)
			got := joined != nil && strings.Contains(joined.Error(), `command "root child help" comes from a composed spec`)
			if got != tc.want {
				t.Errorf("composed-help notice: %v, want %v; notices %v", got, tc.want, notices["root"])
			}
		})
	}
}
