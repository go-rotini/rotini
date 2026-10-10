package rotini

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type pjRoot struct {
	Flags struct {
		Dir string `rotini:"dir"`
	}
	Arguments struct{}
}

type pjShowInputs struct {
	Proj pjRoot
	Show struct {
		Flags struct {
			Name     string `rotini:"name" recon:"name"`
			Spec     string `rotini:"spec"`
			Manifest string `rotini:"manifest"`
		}
		Arguments struct{}
	}
}

func chdirDef() Definition {
	return Definition{
		Name: "proj", Handler: "Proj",
		Flags: []FlagDef{
			{Name: "dir", Identifiers: []string{"-C", "--dir"}, Type: "existingdir", Role: roleChdir},
			{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"},
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
		},
		Commands: []CommandDef{{Name: "show", Handler: "Show", Flags: []FlagDef{
			{Name: "name", Identifiers: []string{"--name"}, Type: "string"},
			{Name: "spec", Identifiers: []string{"--spec"}, Type: "string", From: []string{"value", "file"}},
			{Name: "manifest", Identifiers: []string{"--manifest"}, Type: "existingfile"},
		}}},
		Plugins: []PluginDef{{Name: "ext", Binary: "proj-ext"}},
	}
}

// chdirSeen is what the show handler saw.
type chdirSeen struct {
	in   pjShowInputs
	err  error
	dir  string
	argv []string
}

// chdirTree lays out base/.proj.yaml and base/sub/{.proj.yaml,spec.txt,m.yaml}.
func chdirTree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for name, body := range map[string]string{
		".proj.yaml":     "name: from-base\n",
		"sub/.proj.yaml": "name: from-sub\n",
		"sub/spec.txt":   "spec-in-sub",
		"sub/m.yaml":     "m: 1\n",
	} {
		path := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func runChdir(t *testing.T, base string, argv ...string) (chdirSeen, int, string) {
	t.Helper()
	var seen chdirSeen
	fns := map[string]func(context.Context, *Context){"Show": func(_ context.Context, rtx *Context) {
		seen.in, seen.err = rtx.Inputs[pjShowInputs]()
		seen.dir, seen.argv = rtx.Dir(), rtx.Argv
		if seen.err != nil {
			rtx.HaltWith(seen.err)
		}
	}}
	var errb bytes.Buffer
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "project", Format: "yaml", Discover: &DiscoverDef{Strategy: "walk-up", File: ".proj.yaml"}}}}
	p := NewProgramFunc(chdirDef(), fnLookup(fns)).WithInputSettings(meta).
		WithEnviron([]string{"HOME=" + base}).WithDir(base).WithoutSignalHandling().
		WithStdout(&bytes.Buffer{}).WithStderr(&errb)
	code, _ := p.Run(argv)
	return seen, code, errb.String()
}

func TestChdir_resolvesAgainstTheFlag(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	sub := filepath.Join(base, "sub")
	for _, argv := range [][]string{
		{"-C", "sub", "show", "--spec", "@spec.txt", "--manifest", "m.yaml"},
		{"show", "--spec", "@spec.txt", "--manifest", "m.yaml", "-C", "sub"},
		{"--dir=sub", "show", "--spec", "@spec.txt", "--manifest", "m.yaml"},
		{"-Csub", "show", "--spec", "@spec.txt", "--manifest", "m.yaml"},
		{"-v", "-C", "nope", "-C", "sub", "show", "--spec", "@spec.txt", "--manifest", "m.yaml"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			seen, code, stderr := runChdir(t, base, argv...)
			if code != 0 {
				t.Fatalf("code %d: %s", code, stderr)
			}
			f := seen.in.Show.Flags
			if seen.dir != sub || seen.in.Proj.Flags.Dir != sub {
				t.Errorf("rtx.Dir() = %q, bound -C = %q; want both %q", seen.dir, seen.in.Proj.Flags.Dir, sub)
			}
			if f.Name != "from-sub" || f.Spec != "spec-in-sub" || f.Manifest != "m.yaml" {
				t.Errorf("name %q, spec %q, manifest %q; want config, @file and existingfile read from sub", f.Name, f.Spec, f.Manifest)
			}
		})
	}

	seen, code, stderr := runChdir(t, base, "show")
	if code != 0 || seen.dir != base || seen.in.Show.Flags.Name != "from-base" {
		t.Errorf("without -C: code %d, dir %q, name %q (%s)", code, seen.dir, seen.in.Show.Flags.Name, stderr)
	}
}

// The value is resolved against the run's directory, and rtx.Argv carries the absolute form,
// so the parse binds and checks that.
func TestChdir_argvHoldsTheAbsoluteDirectory(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	seen, code, stderr := runChdir(t, base, "--dir=sub", "-v", "show")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	want := []string{"--dir=" + filepath.Join(base, "sub"), "-v", "show"}
	if strings.Join(seen.argv, " ") != strings.Join(want, " ") {
		t.Errorf("rtx.Argv = %q, want %q", seen.argv, want)
	}
}

// After "--", -C is an argument, not the flag.
func TestChdir_notAfterDoubleDash(t *testing.T) {
	t.Parallel()
	got := chdirSites(chainFor(chdirDef(), "show"), []string{"show", "--", "-C", "sub"})
	if len(got) != 0 {
		t.Errorf("sites after --: %+v", got)
	}
}

func chainFor(def Definition, argv ...string) []Command {
	chain, _ := resolveChain(def, argv)
	return chain
}

func TestChdir_missingDirectoryIsAUsageError(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	for _, argv := range [][]string{{"-C", "nope", "show"}, {"-C", "nope", "--help"}, {"-C", "sub/m.yaml", "show"}} {
		seen, code, stderr := runChdir(t, base, argv...)
		if code != 1 || !strings.Contains(stderr, "-C: ") || seen.dir != "" {
			t.Errorf("%q: code %d, handler ran in %q, stderr %q; want exit 1 naming -C before any handler", argv, code, seen.dir, stderr)
		}
	}
	_, _, stderr := runChdir(t, base, "-C", "nope", "show")
	if !strings.Contains(stderr, `-C: no such directory: "nope"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

// A plugin typed after -C is allowed (the words before a plugin name are otherwise refused),
// and runs in the directory.
func TestChdir_pluginRunsInTheDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell-script stand-in for a plugin cannot run on Windows; see e2e r1_chdir")
	}
	t.Parallel()
	base := chdirTree(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "proj-ext"), []byte("#!/bin/sh\npwd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	p := NewProgramFunc(chdirDef(), fnLookup(nil)).WithEnviron([]string{"PATH=" + bin}).WithDir(base).
		WithoutSignalHandling().WithStdout(&out).WithStderr(&errb)
	if code, err := p.Run([]string{"-C", "sub", "ext"}); code != 0 {
		t.Fatalf("Run = %d, %v: %s", code, err, errb.String())
	}
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out.String()))
	want, _ := filepath.EvalSymlinks(filepath.Join(base, "sub"))
	if got != want {
		t.Errorf("plugin ran in %q, want %q", got, want)
	}

	// Another flag before the plugin's name is still refused.
	errb.Reset()
	if code, _ := p.Run([]string{"-C", "sub", "-v", "ext"}); code != 1 || !strings.Contains(errb.String(), "-v can't come before plugin") {
		t.Errorf("code %d, stderr %q", code, errb.String())
	}
}

// Completers see the flag's directory as rtx.Dir().
type chdirCompleter struct {
	NoHooks
}

func (chdirCompleter) Run(context.Context, *Context) {}
func (chdirCompleter) CompleteFlagValue(rtx *Context, _, _ string) []string {
	return []string{filepath.Base(rtx.Dir())}
}

func TestChdir_completionSeesTheDirectory(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	var out bytes.Buffer
	p := NewProgramFunc(chdirDef(), func(string) (Handler, bool) { return chdirCompleter{}, true }).
		WithDir(base).WithStdout(&out)
	if code, err := p.Complete([]string{"-C", "sub", "show", "--name", ""}, nil); code != 0 {
		t.Fatalf("Complete = %d, %v", code, err)
	}
	if first, _, _ := strings.Cut(out.String(), "\n"); first != "sub" {
		t.Errorf("completion = %q, want the completer to see sub", out.String())
	}
}

// Each run has its own directory.
func TestChdir_parallelRuns(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	var wg sync.WaitGroup
	for i := range 20 {
		dir := fmt.Sprintf("d%d", i)
		if err := os.Mkdir(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			seen, code, stderr := runChdir(t, base, "-C", dir, "show")
			if code != 0 || seen.dir != filepath.Join(base, dir) {
				t.Errorf("run %s: code %d, dir %q (%s)", dir, code, seen.dir, stderr)
			}
		})
	}
	wg.Wait()
}
