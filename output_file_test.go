package rotini

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"
)

// outputDir is a run context whose directory is a fresh temporary one.
func outputDir(t *testing.T) (*Context, string) {
	t.Helper()
	dir := t.TempDir()
	return newContext().WithDir(dir), dir
}

// leftovers lists the directory's entries other than keep.
func leftovers(t *testing.T, dir string, keep ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !slices.Contains(keep, e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestCreateOutput_dashIsStdout(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	rtx := newContext().WithStdout(&out)
	f, err := CreateOutput(rtx, "-")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "hello")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if f.Name() != "-" || out.String() != "hello" {
		t.Errorf("Name = %q, stdout = %q", f.Name(), out.String())
	}
	if err := f.Abort(); err != nil {
		t.Errorf("Abort after Close = %v", err)
	}
}

func TestCreateOutput_newFileCommitsOnClose(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	f, err := CreateOutput(rtx, "out.txt")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "data")
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the target exists before Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(data) != "data" {
		t.Fatalf("out.txt = %q, %v", data, err)
	}
	if extra := leftovers(t, dir, "out.txt"); len(extra) > 0 {
		t.Errorf("left behind %v", extra)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "out.txt"))
		if info.Mode().Perm()&^0o644 != 0 {
			t.Errorf("mode = %v, want at most 0644", info.Mode().Perm())
		}
	}
	if err := f.Close(); err != nil {
		t.Errorf("a second Close = %v", err)
	}
}

func TestCreateOutput_refusesToOverwrite(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CreateOutput(rtx, "out.txt")
	if !errors.Is(err, fs.ErrExist) || CategoryOf(err) != CategoryUsage {
		t.Fatalf("err = %v (category %v), want a usage error matching fs.ErrExist", err, CategoryOf(err))
	}
	if err.Error() != `output file "out.txt" already exists` {
		t.Errorf("message = %q", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("the file changed: %q", data)
	}
}

func TestCreateOutput_overwriteKeepsMode(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := CreateOutput(rtx, path, Overwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "new")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Errorf("file = %q, want new", data)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want the replaced file's 0600", info.Mode().Perm())
		}
	}
}

func TestCreateOutput_fileMode(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits")
	}
	rtx, dir := outputDir(t)
	f, err := CreateOutput(rtx, "secret", FileMode(0o600))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "secret")); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestCreateOutput_abortDiscards(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := CreateOutput(rtx, "out.txt", Overwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "new")
	if err := f.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close after Abort = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("file = %q, want old", data)
	}
	if extra := leftovers(t, dir, "out.txt"); len(extra) > 0 {
		t.Errorf("left behind %v", extra)
	}
	if _, err := f.Write([]byte("late")); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Write after Abort = %v, want fs.ErrClosed", err)
	}
}

func TestCreateOutput_writeErrorFailsClose(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := CreateOutput(rtx, "out.txt", Overwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	f.w = failingWriter{syscall.ENOSPC}
	if _, err := f.Write([]byte("data")); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Write = %v", err)
	}
	err = f.Close()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("Close = %v, want ENOSPC", err)
	}
	if want := `could not write output file "out.txt": ` + syscall.ENOSPC.Error(); err.Error() != want {
		t.Errorf("message = %q, want %q", err, want)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("file = %q, want old", data)
	}
	if extra := leftovers(t, dir, "out.txt"); len(extra) > 0 {
		t.Errorf("left behind %v", extra)
	}
}

func TestCreateOutput_directoryAndMissingParent(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateOutput(rtx, "sub"); err == nil || CategoryOf(err) != CategoryUsage {
		t.Errorf("a directory target: %v", err)
	}
	if _, err := CreateOutput(rtx, filepath.Join("missing", "out")); err == nil {
		t.Error("a missing parent directory was accepted")
	}
	if _, err := CreateOutput(rtx, ""); err == nil || CategoryOf(err) != CategoryUsage {
		t.Errorf("an empty path: %v", err)
	}
}

func TestCreateOutput_symlinkKeepsTheLink(t *testing.T) {
	t.Parallel()
	rtx, dir := outputDir(t)
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	f, err := CreateOutput(rtx, "link.txt", Overwrite(true))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "new")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Errorf("the link's target = %q, want new", data)
	}
}

func TestCreateOutput_deviceIsWrittenDirectly(t *testing.T) {
	t.Parallel()
	rtx, _ := outputDir(t)
	f, err := CreateOutput(rtx, os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if f.tmp != "" {
		t.Errorf("a device got a temporary file %q", f.tmp)
	}
	fmt.Fprint(f, "gone")
	if err := f.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestCreateOutput_runAbortsOpenFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, end := range []string{"return", "panic"} {
		p := NewProgram(Definition{Name: "app", Handler: "App"}, &funcHandlers{run: func(rtx *Context) {
			f, err := CreateOutput(rtx, end+".txt")
			if err != nil {
				t.Error(err)
				return
			}
			fmt.Fprint(f, "half")
			if end == "panic" {
				panic("boom")
			}
		}}).WithDir(dir).WithStderr(&bytes.Buffer{}).WithoutSignalHandling()
		p.Run(nil)
	}
	if extra := leftovers(t, dir); len(extra) > 0 {
		t.Errorf("left behind %v", extra)
	}
}

func TestRetryRename(t *testing.T) {
	t.Parallel()
	busy := errors.New("busy")
	calls := 0
	var pauses []time.Duration
	err := retryRename(func(string, string) error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	}, "a", "b", func(err error) bool { return errors.Is(err, busy) }, func(d time.Duration) { pauses = append(pauses, d) })
	if err != nil || calls != 3 {
		t.Errorf("err = %v after %d calls, want success on the third", err, calls)
	}
	if !slices.Equal(pauses, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}) {
		t.Errorf("pauses = %v", pauses)
	}

	calls = 0
	err = retryRename(func(string, string) error { calls++; return busy }, "a", "b",
		func(error) bool { return true }, func(time.Duration) {})
	if !errors.Is(err, busy) || calls != 6 {
		t.Errorf("err = %v after %d calls, want busy after 6", err, calls)
	}

	calls = 0
	_ = retryRename(func(string, string) error { calls++; return busy }, "a", "b",
		func(error) bool { return false }, func(time.Duration) {})
	if calls != 1 {
		t.Errorf("a final error was retried: %d calls", calls)
	}
}
