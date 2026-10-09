// Command benchcli measures a generated program built from this working tree: the fixture CLI
// in e2e/testdata/script/r11_lean_runtime.txtar. It prints the binary's size (default and
// stripped), how many packages it links, and the wall-clock time of a dispatch, a --help and a
// completion request, each the median of -n fork+exec runs after warm-up runs. `make bench-cli`
// runs it. The numbers are for recording; nothing here passes or fails on them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/txtar"
)

const fixture = "e2e/testdata/script/r11_lean_runtime.txtar"

func main() {
	root := flag.String("root", ".", "the rotini module root")
	runs := flag.Int("n", 300, "timed runs per case")
	warm := flag.Int("warm", 20, "untimed warm-up runs per case")
	flag.Parse()
	if err := run(*root, *runs, *warm); err != nil {
		fmt.Fprintln(os.Stderr, "benchcli:", err)
		os.Exit(1)
	}
}

func run(root string, runs, warm int) error {
	if runs < 1 {
		return errors.New("-n must be at least 1")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("module root: %w", err)
	}
	dir, err := os.MkdirTemp("", "rotini-benchcli-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	ar, err := txtar.ParseFile(filepath.Join(root, fixture))
	if err != nil {
		return fmt.Errorf("read the fixture: %w", err)
	}
	if err := txtar.Write(ar, dir); err != nil {
		return fmt.Errorf("write the fixture: %w", err)
	}
	gomod := fmt.Sprintf("module example.com/app\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini v0.0.0 => %s\n",
		filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o600); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	rotini := filepath.Join(dir, "rotini"+exe)
	app := filepath.Join(dir, "app"+exe)
	stripped := filepath.Join(dir, "app-stripped"+exe)
	steps := [][]string{
		{root, "go", "build", "-buildvcs=false", "-o", rotini, "./cmd/rotini"}, // unstamped, like the e2e tier
		{dir, rotini, "generate", "cmd/app/.rotini.spec.yaml", "--config", "cmd/app/.rotini.conf.yaml"},
		{dir, "go", "mod", "tidy"},
		{dir, "go", "build", "-o", app, "./cmd/app"},
		{dir, "go", "build", "-ldflags", "-s -w", "-o", stripped, "./cmd/app"},
	}
	for _, s := range steps {
		if _, err := output(s[0], s[1], s[2:]...); err != nil {
			return err
		}
	}
	deps, err := output(dir, "go", "list", "-deps", "./cmd/app")
	if err != nil {
		return err
	}

	fmt.Printf("%s/%s, %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	for _, b := range []struct{ name, path string }{{"binary", app}, {"binary, -s -w", stripped}} {
		info, err := os.Stat(b.path)
		if err != nil {
			return fmt.Errorf("binary size: %w", err)
		}
		fmt.Printf("%-16s %12d bytes\n", b.name, info.Size())
	}
	fmt.Printf("%-16s %12d\n", "packages", len(strings.Fields(deps)))

	for _, c := range []struct {
		name string
		args []string
	}{
		{"dispatch", []string{"build", "--name", "x", "tgt"}},
		{"--help", []string{"--help"}},
		{"complete", []string{"__complete", "build", "--t"}},
	} {
		ds, err := timeRuns(app, c.args, runs, warm)
		if err != nil {
			return err
		}
		fmt.Printf("%-16s median %6.2fms  p10 %6.2fms  p90 %6.2fms\n",
			c.name, ms(ds[len(ds)/2]), ms(ds[len(ds)/10]), ms(ds[len(ds)*9/10]))
	}
	return nil
}

// timeRuns runs bin with args warm times untimed, then runs times timed, and returns the
// durations sorted.
func timeRuns(bin string, args []string, runs, warm int) ([]time.Duration, error) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devnull.Close()
	once := func() (time.Duration, error) {
		cmd := exec.Command(bin, args...)
		cmd.Stdout, cmd.Stderr = devnull, devnull
		start := time.Now()
		err := cmd.Run()
		d := time.Since(start)
		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			return 0, fmt.Errorf("run %s: %w", bin, err)
		}
		return d, nil
	}
	for range warm {
		if _, err := once(); err != nil {
			return nil, err
		}
	}
	ds := make([]time.Duration, runs)
	for i := range ds {
		if ds[i], err = once(); err != nil {
			return nil, err
		}
	}
	slices.Sort(ds)
	return ds, nil
}

// output runs a command in dir and returns its stdout, or an error carrying its stderr.
func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return string(out), nil
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
