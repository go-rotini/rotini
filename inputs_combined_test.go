package rotini

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fsPathSet is a flag set's generated struct, embedded in the command's Flags.
type fsPathSet struct {
	File  string `rotini:"file" expand:"home,env"`
	Cache string `rotini:"cache" recon:"cache" env:"APP_CACHE" expand:"home"`
}

type fsPathInputs struct {
	App struct {
		Flags struct {
			Raw string `rotini:"raw"`
			fsPathSet
		}
		Arguments struct{}
	}
}

var fsPathDef = Definition{Name: "app", Handler: "App", Flags: []FlagDef{
	{Name: "raw", Identifiers: []string{"--raw"}, Type: "string"},
	{Name: "file", Identifiers: []string{"--file"}, Type: "existingfile"},
	{Name: "cache", Identifiers: []string{"--cache"}, Type: "string"},
}}

// A flag from a flag set is expanded, path-checked and origin-tagged like the command's own.
func TestFlagSet_pathsAndOrigins(t *testing.T) {
	_, home, work, _ := exSetup(t)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APP_CACHE=~/c"}
	rtx := NewContextFor(fsPathDef, []string{"--raw", "~/r", "--file", "~/f.txt"}).WithDir(work).WithEnviron(env)
	in, report, err := rtx.InputsWithReport[fsPathInputs]()
	if err != nil {
		t.Fatal(err)
	}
	f := in.App.Flags
	if f.File != filepath.Join(home, "f.txt") || f.Cache != home+"/c" || f.Raw != "~/r" {
		t.Errorf("flags = %+v", f)
	}
	for path, want := range map[FieldPath]string{"App.Flags.File": "argv:--file", "App.Flags.Cache": "env:APP_CACHE"} {
		if w, ok := report.Winner(path); !ok || w.Origin != want {
			t.Errorf("Winner(%s) = %+v, %v; want origin %q", path, w, ok, want)
		}
	}

	_, err = NewContextFor(fsPathDef, []string{"--file", "~/missing"}).WithDir(work).WithEnviron(env).Inputs[fsPathInputs]()
	if want := `--file: no such file: "` + filepath.Join(home, "missing") + `" (written as "~/missing")`; err == nil || err.Error() != want {
		t.Errorf("missing set file = %v, want %q", err, want)
	}
}

type cdPathInputs struct {
	Proj pjRoot
	Show struct {
		Flags struct {
			File  string `rotini:"file" expand:"env"`
			Cache string `rotini:"cache" recon:"cache" relativeto:"config"`
		}
		Arguments struct{}
		Env       struct {
			Log string `rotini:"log" recon:"log" env:"APP_LOG" path:"file"`
		}
		Config struct {
			Dirs []string `rotini:"dirs" recon:"dirs" path:"dir"`
		}
	}
}

// -C moves every path rule to its directory: the expanded argv value, an env input and a config
// list are checked against it (and bind as written), and the walk-up config file found from it
// anchors relative_to.
func TestChdir_pathRules(t *testing.T) {
	t.Parallel()
	base := chdirTree(t)
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".proj.yaml"), []byte("cache: c\ndirs: [d]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def := chdirDef()
	def.Commands[0].Flags = []FlagDef{
		{Name: "file", Identifiers: []string{"--file"}, Type: "existingfile"},
		{Name: "cache", Identifiers: []string{"--cache"}, Type: "string"},
	}
	run := func(env []string, argv ...string) (cdPathInputs, string, int) {
		var in cdPathInputs
		fns := map[string]func(context.Context, *Context){"Show": func(_ context.Context, rtx *Context) {
			var err error
			if in, err = rtx.Inputs[cdPathInputs](); err != nil {
				rtx.HaltWith(err)
			}
		}}
		var errb bytes.Buffer
		meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "project", Format: "yaml", Discover: &DiscoverDef{Strategy: "walk-up", File: ".proj.yaml"}}}}
		code, _ := NewProgramFunc(def, fnLookup(fns)).WithInputSettings(meta).
			WithEnviron(env).WithDir(base).WithoutSignalHandling().
			WithStdout(&bytes.Buffer{}).WithStderr(&errb).Run(argv)
		return in, errb.String(), code
	}

	in, stderr, code := run([]string{"HOME=" + base, "N=spec.txt", "APP_LOG=m.yaml"}, "-C", "sub", "show", "--file", "$N")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	s := in.Show
	if s.Flags.File != "spec.txt" || s.Flags.Cache != filepath.Join(sub, "c") || s.Env.Log != "m.yaml" || len(s.Config.Dirs) != 1 || s.Config.Dirs[0] != "d" {
		t.Errorf("show = %+v", s)
	}

	// The same values are missing from the process's directory, so without -C each fails.
	if _, stderr, code := run([]string{"HOME=" + base, "N=spec.txt"}, "show", "--file", "$N"); code == 0 || !strings.Contains(stderr, "no such file") {
		t.Errorf("without -C: exit %d, %s", code, stderr)
	}
	if _, stderr, code := run([]string{"HOME=" + base, "APP_LOG=m.yaml"}, "show"); code == 0 || !strings.Contains(stderr, `APP_LOG: no such file: "m.yaml"`) {
		t.Errorf("env path without -C: exit %d, %s", code, stderr)
	}
}

// The low-level Parser expands declared paths too, before its path check.
func TestParser_expands(t *testing.T) {
	_, home, work, _ := exSetup(t)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "A=a1", "OUTDIR=" + work}
	rtx := NewContextFor(exDef, []string{"-f", "~/f.txt", "--paths", "$A", "--raw", "~/r", "~/src"}).WithDir(work).WithEnviron(env)
	var in exInputs
	if err := rtx.Parser().Parse(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if f := in.App.Flags; f.File != filepath.Join(home, "f.txt") || len(f.Paths) != 1 || f.Paths[0] != "a1" || f.Raw != "~/r" || f.Out != work+"/out.txt" {
		t.Errorf("flags = %+v", f)
	}
	if in.App.Arguments.Src != home+"/src" {
		t.Errorf("src = %q", in.App.Arguments.Src)
	}
	rtx = NewContextFor(exDef, []string{"-f", "~/nope"}).WithDir(work).WithEnviron(env)
	err := rtx.Parser().Parse(rtx, &in)
	if want := `-f: no such file: "` + filepath.Join(home, "nope") + `" (written as "~/nope")`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
