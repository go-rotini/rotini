package rotini

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComplete_discoversPlugins(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"acme-foo", "acme-bar", "unrelated"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "acme", Handler: "App",
		Commands:  []CommandDef{{Name: "bar", Handler: "AcmeBar"}}, // collides with acme-bar
		Discovery: &RemoteDiscoveryDef{Prefix: "acme-", Path: dir},
	}

	got := complete(def, []string{""})
	if !contains(got, "foo") {
		t.Errorf("discovered plugin %q missing from %v", "foo", got)
	}
	if !contains(got, "bar") {
		t.Errorf("declared command %q missing from %v", "bar", got)
	}
	if n := countString(got, "bar"); n != 1 {
		t.Errorf("%q should appear once (declared wins over discovered acme-bar), got %d in %v", "bar", n, got)
	}
	if contains(got, "unrelated") {
		t.Errorf("non-prefixed executable should not be discovered: %v", got)
	}

	// Hidden discovery dispatches but lists nothing.
	def.Discovery.Hidden = true
	if hidden := complete(def, []string{""}); contains(hidden, "foo") {
		t.Errorf("hidden discovery should not list plugins: %v", hidden)
	}
}

func countString(ss []string, want string) int {
	n := 0
	for _, s := range ss {
		if s == want {
			n++
		}
	}
	return n
}

func completionDef() Definition {
	return Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Commands: []CommandDef{
			{
				Name: "build", Handler: "AppBuild", Aliases: []string{"b"},
				Flags: []FlagDef{{Name: "mode", Identifiers: []string{"-m", "--mode"}, Type: "string", Enum: []string{"debug", "release"}}},
			},
			{Name: "test", Handler: "AppTest"},
		},
		RemoteCommands: []RemoteDef{{Name: "plugin", Binary: "app-plugin"}},
	}
}

func TestComplete(t *testing.T) {
	def := completionDef()
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"all commands + remotes", []string{""}, []string{"b", "build", "plugin", "test"}},
		{"command prefix", []string{"bu"}, []string{"build"}},
		{"remote prefix", []string{"plu"}, []string{"plugin"}},
		{"flag names of chain (declared only, no auto -h)", []string{"build", "-"}, []string{"--mode", "--verbose", "-m", "-v"}},
		{"flag name prefix", []string{"build", "--m"}, []string{"--mode"}},
		{"enum value of preceding flag", []string{"build", "--mode", ""}, []string{"debug", "release"}},
		{"enum value prefix", []string{"build", "--mode", "r"}, []string{"release"}},
		{"no match", []string{"zzz"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := complete(def, c.words)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("complete(%v) = %v, want %v", c.words, got, c.want)
			}
		})
	}
}

// TestComplete_noAutoHelpFlag pins the ethos: completion never auto-adds -h/--help.
// They appear only when the CLI declares a help flag (Pillar 1 — no framework-injected
// flags).
func TestComplete_noAutoHelpFlag(t *testing.T) {
	// No declared help flag → completion offers none.
	bare := completionDef()
	if got := complete(bare, []string{"-"}); contains(got, "--help") || contains(got, "-h") {
		t.Errorf("completion auto-added a help flag the CLI never declared: %v", got)
	}

	// A declared help flag is offered like any other flag.
	withHelp := completionDef()
	withHelp.Flags = append(withHelp.Flags, FlagDef{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool"})
	got := complete(withHelp, []string{"-"})
	if !contains(got, "--help") || !contains(got, "-h") {
		t.Errorf("declared help flag missing from completion: %v", got)
	}
}

func TestComplete_runIntercept(t *testing.T) {
	out := &bytes.Buffer{}
	p := NewProgram(completionDef(), &testHandlers{log: new([]string)}).WithArguments([]string{"__complete", "te"})
	p.stdout, p.stderr = out, &bytes.Buffer{}
	if code := p.run(p.args); code != 0 {
		t.Fatalf("__complete run = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); got != "test" {
		t.Errorf("__complete output = %q, want %q", got, "test")
	}
}
