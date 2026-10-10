package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type exFlags struct {
	File  string   `rotini:"file" expand:"home,env"`
	Out   string   `rotini:"out" expand:"env"`
	Cache string   `rotini:"cache" recon:"cache" env:"APP_CACHE" expand:"home" relativeto:"config"`
	Paths []string `rotini:"paths" expand:"env"`
	Raw   string   `rotini:"raw"`
	Help  bool     `rotini:"help"`
}
type exArgs struct {
	Src  string   `rotini:"src" expand:"home,env"`
	Rest []string `rotini:"rest" expand:"env"`
}
type exCmd struct {
	Flags     exFlags
	Arguments exArgs
	Env       struct {
		Home string `rotini:"home" recon:"home" env:"APP_HOME" expand:"home,env" path:"dir"`
		Log  string `rotini:"log" recon:"log" env:"APP_LOG" path:"file"`
	}
	Config struct {
		Data  string   `rotini:"data" recon:"data" expand:"home" relativeto:"config"`
		Dirs  []string `rotini:"dirs" recon:"dirs" relativeto:"config" path:"dir"`
		Plain string   `rotini:"plain" recon:"plain"`
	}
}
type exInputs struct{ App exCmd }

var exDef = Definition{Name: "app", Handler: "App",
	Flags: []FlagDef{
		{Name: "file", Identifiers: []string{"--file", "-f"}, Type: "existingfile"},
		{Name: "out", Identifiers: []string{"--out"}, Type: "string", Default: "$OUTDIR/out.txt"},
		{Name: "cache", Identifiers: []string{"--cache"}, Type: "string"},
		{Name: "paths", Identifiers: []string{"--paths"}, Type: "[]string", Separator: ","},
		{Name: "raw", Identifiers: []string{"--raw"}, Type: "string"},
		{Name: "help", Identifiers: []string{"--help"}, Type: "bool", ShortCircuit: true},
	},
	Arguments: []ArgDef{{Name: "src", Type: "string"}, {Name: "rest", Type: "[]string", Variadic: true}},
}

// exSetup makes a home directory, a working directory and a config file under one temp root.
func exSetup(t *testing.T) (root, home, work, conf string) {
	t.Helper()
	root = t.TempDir()
	home, work = filepath.Join(root, "home"), filepath.Join(root, "work")
	conf = filepath.Join(root, "etc", "app.yaml")
	for _, d := range []string{home, work, filepath.Dir(conf), filepath.Join(root, "etc", "d1")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	idWrite(t, home, "f.txt", "x")
	return root, home, work, conf
}

func exBind(t *testing.T, work, conf, confBody string, env []string, argv ...string) (exInputs, error) {
	t.Helper()
	if conf != "" {
		idWrite(t, filepath.Dir(conf), filepath.Base(conf), confBody)
	}
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "conf", Scope: "app", Path: conf, Format: "yaml"}}}
	// --out's default reads $OUTDIR; a test overrides it with a later entry.
	rtx := NewContextFor(exDef, argv).WithDir(work).WithEnviron(append([]string{"OUTDIR=/o"}, env...))
	var in exInputs
	err := NewInputReader(meta).Read(rtx, &in)
	return in, err
}

func TestExpand_argv(t *testing.T) {
	_, home, work, conf := exSetup(t)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "OUTDIR=" + work, "A=a1", "B=b2"}
	in, err := exBind(t, work, conf, "", env, "-f", "~/f.txt", "--paths", "$A,$B", "--raw", "~/$A", "~/src", "$A", "${B}x")
	if err != nil {
		t.Fatal(err)
	}
	f := in.App.Flags
	if f.File != filepath.Join(home, "f.txt") || f.Out != work+"/out.txt" || !slices.Equal(f.Paths, []string{"a1", "b2"}) || f.Raw != "~/$A" {
		t.Errorf("flags = %+v", f)
	}
	if a := in.App.Arguments; a.Src != home+"/src" || !slices.Equal(a.Rest, []string{"a1", "b2x"}) {
		t.Errorf("arguments = %+v", a)
	}

	// The process environment is not read: OUTDIR is set (non-empty) only there.
	t.Setenv("OUTDIR", "/process")
	if _, err := exBind(t, work, conf, "", []string{"HOME=" + home, "OUTDIR="}); err == nil || err.Error() != `--out: $OUTDIR is not set (in "$OUTDIR/out.txt")` {
		t.Errorf("default with an unset variable = %v", err)
	}
}

func TestExpand_argvErrors(t *testing.T) {
	_, home, work, conf := exSetup(t)
	env := []string{"HOME=" + home, "OUTDIR=" + work}
	for argv, want := range map[string]string{
		"--paths=$A,$NOPE":   `--paths: $NOPE is not set (in "$NOPE")`,
		"--out=${OUTDIR:-x}": `--out: only $NAME and ${NAME} are expanded (in "${OUTDIR:-x}")`,
		"-f=~/missing":       `-f: no such file: "` + filepath.Join(home, "missing") + `" (written as "~/missing")`,
	} {
		_, err := exBind(t, work, conf, "", append(env, "A=1"), argv)
		var pe *ParseError
		if err == nil || err.Error() != want || !errors.As(err, &pe) || !errors.Is(err, ErrUsage) {
			t.Errorf("%s: err = %v, want usage error %q", argv, err, want)
		}
	}
	// An argv expansion failure fails even under a short-circuit flag.
	if _, err := exBind(t, work, conf, "", env, "--help", "--paths=$NOPE"); err == nil {
		t.Error("--help with a bad argv value: want an error")
	}
}

func TestExpand_envAndConfig(t *testing.T) {
	root, home, work, conf := exSetup(t)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APP_HOME=~", "APP_CACHE=~/c", "APP_LOG=" + filepath.Join(home, "f.txt")}
	in, err := exBind(t, work, conf, "data: ~/d\ndirs: [d1, "+root+"]\nplain: ~/p\n", env)
	if err != nil {
		t.Fatal(err)
	}
	etc := filepath.Join(root, "etc")
	if in.App.Env.Home != home || in.App.Flags.Cache != home+"/c" {
		t.Errorf("env = %+v, cache %q", in.App.Env, in.App.Flags.Cache)
	}
	if c := in.App.Config; c.Data != home+"/d" || !slices.Equal(c.Dirs, []string{filepath.Join(etc, "d1"), root}) || c.Plain != "~/p" {
		t.Errorf("config = %+v", c)
	}

	// A flag's config fallback is resolved against the file's directory; its env fallback is not.
	in, err = exBind(t, work, conf, "cache: rel\n", []string{"HOME=" + home})
	if err != nil || in.App.Flags.Cache != filepath.Join(etc, "rel") {
		t.Errorf("config fallback = %q, %v", in.App.Flags.Cache, err)
	}
	in, err = exBind(t, work, conf, "", []string{"HOME=" + home, "APP_CACHE=rel"})
	if err != nil || in.App.Flags.Cache != "rel" {
		t.Errorf("env fallback = %q, %v", in.App.Flags.Cache, err)
	}
}

func TestExpand_envErrors(t *testing.T) {
	_, home, work, conf := exSetup(t)
	_, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_HOME=$NOPE"})
	if err == nil || err.Error() != `APP_HOME: $NOPE is not set (in "$NOPE")` || !errors.Is(err, ErrUsage) {
		t.Errorf("env expansion = %v", err)
	}
	_, err = exBind(t, work, conf, "", []string{"APP_CACHE=~/x"})
	if err == nil || !strings.HasPrefix(err.Error(), `--cache: cannot expand "~/x": `) || !strings.HasSuffix(err.Error(), "(from environment variable APP_CACHE)") {
		t.Errorf("fallback expansion = %v", err)
	}
	// A configuration value that cannot be expanded is skipped under a short-circuit flag.
	if _, err := exBind(t, work, conf, "data: ~/d\ncache: ~/c\n", nil, "--help"); err != nil {
		t.Errorf("--help with an unexpandable config value = %v", err)
	}
	if _, err := exBind(t, work, conf, "data: ~/d\n", nil); err == nil || !strings.Contains(err.Error(), `cannot expand "~/d"`) {
		t.Errorf("config expansion = %v", err)
	}
}

// Env and config inputs of a path type are checked, against the run's directory.
func TestChannelPaths_checked(t *testing.T) {
	_, home, work, conf := exSetup(t)
	idWrite(t, work, "log.txt", "x")
	if _, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_LOG=log.txt"}); err != nil {
		t.Errorf("a relative file in the run's directory = %v", err)
	}
	_, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_LOG=/nonexistent"})
	if err == nil || err.Error() != `APP_LOG: no such file: "/nonexistent"` || !errors.Is(err, ErrUsage) {
		t.Errorf("missing env file = %v", err)
	}
	_, err = exBind(t, work, conf, "", []string{"HOME=" + home, "APP_LOG=" + work})
	if err == nil || !strings.Contains(err.Error(), "is a directory, not a file") {
		t.Errorf("env dir for a file = %v", err)
	}
	_, err = exBind(t, work, conf, "dirs: [nope]\n", []string{"HOME=" + home})
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Errorf("missing config dir = %v", err)
	}
}

func TestExpand_layers(t *testing.T) {
	_, home, work, _ := exSetup(t)
	rtx := NewContextFor(exDef, []string{"--raw=x", "-f", "~/f.txt", "--paths=$A"}).WithDir(work).WithEnviron([]string{"HOME=" + home, "A=a", "OUTDIR=/o"})
	argv, err := rtx.ArgvInputs[exInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if f := argv.Values.App.Flags; f.File != filepath.Join(home, "f.txt") || !slices.Equal(f.Paths, []string{"a"}) {
		t.Errorf("argv layer = %+v", f)
	}
	if src := argv.Set["App.Flags.Paths"]; src.Raw != "a" {
		t.Errorf("argv presence = %+v", src)
	}
	defs, err := rtx.DefaultInputs[exInputs]()
	if err != nil || defs.Values.App.Flags.Out != "/o/out.txt" {
		t.Errorf("defaults layer = %q, %v", defs.Values.App.Flags.Out, err)
	}
}

// An empty path variable is not checked as a path.
func TestChannelPaths_emptySkipped(t *testing.T) {
	_, home, work, conf := exSetup(t)
	if _, err := exBind(t, work, conf, "", []string{"HOME=" + home, "APP_LOG="}); err != nil {
		t.Errorf("empty APP_LOG = %v", err)
	}
}
