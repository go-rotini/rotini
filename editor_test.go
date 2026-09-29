package rotini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The editor is driven by a fake: a shell command that rewrites the file it is handed. That
// exercises the real path — temp file, extension, argv splitting, exec, read back — without
// needing an interactive program.

// fakeEditor writes a shell script that acts as an editor and returns its path.
//
// A script file rather than `sh -c '…'`: the command is split with strings.Fields, which does not
// honor quotes, so a quoted inline script would arrive as a dozen mangled arguments. That is the
// same splitting Pager uses and the same limitation — good enough for `code --wait`, not a shell.
func fakeEditor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEditor_roundTrip(t *testing.T) {
	got, err := NewEditor().
		WithCommand(fakeEditor(t, `printf 'edited\n' > "$1"`)).
		Edit(context.Background(), []byte("draft\n"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if string(got) != "edited\n" {
		t.Errorf("content = %q, want the editor's output", got)
	}
}

// Unchanged and emptied are the two ways a user says "never mind", and both are one sentinel so
// a caller branches once rather than diffing.
func TestEditor_abortedCases(t *testing.T) {
	for _, c := range []struct {
		name    string
		command string
	}{
		{"quit without saving", `exit 0`},
		{"saved an empty buffer", `printf '' > "$1"`},
		{"saved only whitespace", `printf '  \n' > "$1"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewEditor().WithCommand(fakeEditor(t, c.command)).Edit(context.Background(), []byte("draft\n"))
			if !errors.Is(err, ErrEditAborted) {
				t.Errorf("err = %v, want ErrEditAborted", err)
			}
			if CategoryOf(err) != CategoryUsage {
				t.Errorf("category = %v, want usage — the user can fix it by editing again", CategoryOf(err))
			}
		})
	}
}

// The extension is how an editor decides how to highlight the buffer, so it has to reach the
// filename. The fake reports the path it was given.
func TestEditor_extensionReachesTheFile(t *testing.T) {
	got, err := NewEditor().WithExtension("yaml").
		WithCommand(fakeEditor(t, `printf '%s' "$1" > "$1"`)).
		Edit(context.Background(), []byte("draft\n"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !strings.HasSuffix(string(got), ".yaml") {
		t.Errorf("temp file = %q, want a .yaml suffix", got)
	}
	// A leading dot is optional at the call site.
	if e := NewEditor().WithExtension(".md"); e.extension != ".md" {
		t.Errorf("extension = %q, want .md", e.extension)
	}
}

// The temp file must not survive, on any path.
func TestEditor_cleansUp(t *testing.T) {
	dir := t.TempDir()
	_, _ = NewEditor().WithDir(dir).
		WithCommand(fakeEditor(t, `printf 'edited\n' > "$1"`)).
		Edit(context.Background(), []byte("draft\n"))
	_, _ = NewEditor().WithDir(dir).WithCommand(fakeEditor(t, `exit 3`)).
		Edit(context.Background(), []byte("draft\n"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d temp file(s) left behind: %v", len(entries), entries)
	}
}

// A failing editor is an error, not an abort: the user did not decline, the tool broke.
func TestEditor_failureIsNotAnAbort(t *testing.T) {
	_, err := NewEditor().WithCommand(fakeEditor(t, `exit 3`)).Edit(context.Background(), []byte("draft\n"))
	if err == nil {
		t.Fatal("a failing editor returned no error")
	}
	if errors.Is(err, ErrEditAborted) {
		t.Error("a crashed editor was reported as a user abort")
	}
}

// $VISUAL wins over $EDITOR — the convention the two variables exist for.
func TestEditor_resolutionOrder(t *testing.T) {
	t.Setenv("VISUAL", "visual-editor --wait")
	t.Setenv("EDITOR", "editor-fallback")

	name, args := NewEditor().resolve()
	if name != "visual-editor" || len(args) != 1 || args[0] != "--wait" {
		t.Errorf("resolve() = (%q, %v), want $VISUAL split into command and args", name, args)
	}

	os.Unsetenv("VISUAL")
	if name, _ := NewEditor().resolve(); name != "editor-fallback" {
		t.Errorf("without $VISUAL, resolve() = %q, want $EDITOR", name)
	}

	// An explicit command beats both.
	if name, _ := NewEditor().WithCommand("explicit").resolve(); name != "explicit" {
		t.Errorf("WithCommand did not win: %q", name)
	}

	// Nothing set at all still resolves to something rather than failing.
	os.Unsetenv("EDITOR")
	if name, _ := NewEditor().resolve(); name == "" {
		t.Error("no editor configured resolved to nothing; want a platform default")
	}
}
