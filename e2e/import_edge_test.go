//go:build !mutation

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportEdgeCases imports programs built the way real ones are, beyond the importer's
// corpus: a Cobra tree built across files and functions, with inherited required flags, flag
// groups, a normalized flag name, standard-library flags and a command added only when an
// environment variable is set; and a program with no sub-commands in each framework. The
// built CLI must accept what the program accepted and reject what it rejected.
func TestImportEdgeCases(t *testing.T) {
	importCmd(t)
	roundTrip(t, []importCase{
		{framework: "cobra", fixture: "edge/cobra-edge", pkg: ".", cli: "edge", checks: []importCheck{
			{args: []string{"-p", "dev", "server", "start", "fast", "--port", "1", "--insecure"}, want: "edge server start"},
			{args: []string{"--profile=dev", "--log-level", "debug", "-v", "3", "server", "stop", "-ff", "--signal", "KILL"}, want: "Force:2"},
			{args: []string{"-p", "dev", "copy", "a", "b", "c", "-R"}, want: "edge copy"},
			{args: []string{"-p", "dev", "server", "halt"}, want: "edge server stop"},
			{args: []string{"server", "start", "--help"}, want: "Usage:\n  edge server start"},
			{args: []string{"server", "stop"}, want: "--profile", fail: true},
			{args: []string{"-p", "dev", "server", "start", "fast", "--insecure"}, want: "--port", fail: true},
			{args: []string{"-p", "dev", "server", "start", "fast", "--port", "1", "--insecure", "--tls-cert", "c"}, want: "--insecure", fail: true},
			{args: []string{"-p", "dev", "server", "start", "fast", "--port", "1"}, want: "--insecure", fail: true},
			{args: []string{"-p", "dev", "server", "start", "fast", "--port", "1", "--insecure", "--token", "t"}, want: "--token-file", fail: true},
			{args: []string{"-p", "dev", "server", "start", "slow", "--port", "1", "--insecure"}, want: "slow", fail: true},
			{args: []string{"-p", "dev", "copy", "a"}, want: "dst", fail: true},
			{args: []string{"-p", "dev", "server", "stop", "extra"}, want: `takes no arguments, got "extra"`, fail: true},
			{args: []string{"-p", "dev", "experimental"}, want: "experimental", fail: true},
		}},
		{framework: "cobra", fixture: "edge/cobra-single", pkg: ".", cli: "single", summary: "1 command,", checks: []importCheck{
			{args: []string{"-w", "--skip", "a,b", "f.txt"}, want: "Skip:[a b]"},
			{args: []string{"--version"}, want: "single"},
			{args: []string{}, want: "<file>", fail: true},
			{args: []string{"a", "b"}, want: "at most 1 argument", fail: true},
		}},
		{framework: "kong", fixture: "edge/kong-single", pkg: ".", cli: "single", checks: []importCheck{
			{args: []string{"-w", "f.txt"}, env: []string{"SINGLE_SKIP=a,b"}, want: "Skip:[a b]"},
			{args: []string{"--mode", "fast", "f.txt"}, want: "Mode:fast"},
			{args: []string{"--mode", "slow", "f.txt"}, want: "slow", fail: true},
			{args: []string{}, want: "<file>", fail: true},
		}},
		{framework: "urfave", fixture: "edge/urfave-single", pkg: ".", cli: "single", checks: []importCheck{
			{args: []string{"-w", "f.txt"}, env: []string{"SINGLE_SKIP=a,b"}, want: "Skip:[a b]"},
			{args: []string{}, want: "<file>", fail: true},
		}},
	})
}

// treeOf returns every file under dir with its contents, for comparing two states of a tree.
func treeOf(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		b.WriteString(path)
		b.WriteString("\n")
		b.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
