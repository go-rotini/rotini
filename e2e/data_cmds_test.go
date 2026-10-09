//go:build !mutation

package e2e

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// dataCmds are the script commands for data passing through a program: writing exact bytes and
// large inputs, timing a run whose stdin stays open, and measuring a run's memory.
func dataCmds() map[string]func(ts *testscript.TestScript, neg bool, args []string) {
	return map[string]func(ts *testscript.TestScript, neg bool, args []string){
		// writebytes writes a Go-quoted string's bytes to a file, for content a txtar can't
		// hold, such as a NUL.
		"writebytes": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg || len(args) != 2 {
				ts.Fatalf("usage: writebytes <file> <go-quoted string>")
			}
			s, err := strconv.Unquote(args[1])
			if err != nil {
				ts.Fatalf("writebytes: %q is not a Go-quoted string", args[1])
			}
			ts.Check(os.WriteFile(ts.MkAbs(args[0]), []byte(s), 0o600))
		},
		// genlines writes a file of about n bytes of numbered lines.
		"genlines": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg || len(args) != 2 {
				ts.Fatalf("usage: genlines <bytes> <file>")
			}
			n, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				ts.Fatalf("genlines: %q is not a byte count", args[0])
			}
			f, err := os.Create(ts.MkAbs(args[1]))
			ts.Check(err)
			w := bufio.NewWriterSize(f, 1<<20)
			pad := strings.Repeat("x", 64)
			for i, written := 0, int64(0); written < n; i++ {
				line := strconv.Itoa(i) + " " + pad + "\n"
				_, err := w.WriteString(line)
				ts.Check(err)
				written += int64(len(line))
			}
			ts.Check(w.Flush())
			ts.Check(f.Close())
		},
		// heldstdin runs a command whose stdin is a pipe held open, and fails if it does not
		// exit within the budget: a program that waits on stdin it should not read hangs.
		"heldstdin": func(ts *testscript.TestScript, neg bool, args []string) {
			if len(args) < 2 {
				ts.Fatalf("usage: heldstdin <seconds> <command> [args...]")
			}
			budget, err := time.ParseDuration(args[0] + "s")
			if err != nil {
				ts.Fatalf("heldstdin: %q is not a number of seconds", args[0])
			}
			r, w, err := os.Pipe()
			ts.Check(err)
			defer w.Close()
			cmd := scriptCommand(ts, args[1], args[2:])
			cmd.Stdin = r
			var out strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &out
			ts.Check(cmd.Start())
			r.Close()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				ts.Logf("%s", out.String())
				var ee *exec.ExitError
				if err != nil && !errors.As(err, &ee) {
					ts.Fatalf("heldstdin: %v", err)
				}
				if (err != nil) != neg {
					ts.Fatalf("heldstdin: %v exited %v", args[1:], err)
				}
			case <-time.After(budget):
				_ = cmd.Process.Kill()
				ts.Fatalf("heldstdin: %v still running after %s: it waits on stdin", args[1:], budget)
			}
		},
		// rssbelow runs a command with stdin from a file and its stdout discarded, and fails
		// when its peak resident memory reaches a number of megabytes.
		"rssbelow": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg || len(args) < 3 {
				ts.Fatalf("usage: rssbelow <MB> <stdin file> <command> [args...]")
			}
			mb, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				ts.Fatalf("rssbelow: %q is not a number of megabytes", args[0])
			}
			in, err := os.Open(ts.MkAbs(args[1]))
			ts.Check(err)
			defer in.Close()
			cmd := scriptCommand(ts, args[2], args[3:])
			cmd.Stdin, cmd.Stdout = in, io.Discard
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				ts.Fatalf("rssbelow: %v: %v\n%s", args[2:], err, stderr.String())
			}
			peak, ok := maxRSS(cmd.ProcessState)
			if !ok {
				ts.Logf("rssbelow: peak memory is not reported on this platform")
				return
			}
			ts.Logf("%v: peak RSS %d MB (budget %d MB)", args[2:], peak>>20, mb)
			if peak >= mb<<20 {
				ts.Fatalf("%v peaked at %d MB, over the budget of %d MB", args[2:], peak>>20, mb)
			}
		},
	}
}

// scriptCommand builds a command the way exec runs it: a relative program path is the
// script's, in the script's directory and environment.
func scriptCommand(ts *testscript.TestScript, name string, args []string) *exec.Cmd {
	if strings.ContainsAny(name, `/\`) {
		name = ts.MkAbs(name)
	} else if p, err := exec.LookPath(name); err == nil {
		name = p
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = ts.MkAbs(".")
	cmd.Env = append(os.Environ(), "HOME="+ts.Getenv("HOME"), "NO_COLOR=1", "TERM=dumb")
	if !filepath.IsAbs(cmd.Path) {
		cmd.Path = ts.MkAbs(cmd.Path)
	}
	return cmd
}

// mergeCmds joins script command sets; a name in more than one is a mistake.
func mergeCmds(sets ...map[string]func(ts *testscript.TestScript, neg bool, args []string)) map[string]func(ts *testscript.TestScript, neg bool, args []string) {
	out := map[string]func(ts *testscript.TestScript, neg bool, args []string){}
	for _, set := range sets {
		for name, fn := range set {
			if _, dup := out[name]; dup {
				panic("e2e: script command " + name + " is defined twice")
			}
			out[name] = fn
		}
	}
	return out
}
