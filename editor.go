package rotini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Editor brings text IN through the user's editor, the `git commit` / `crontab -e` shape. It is
// the counterpart to [Pager], which sends text out through the user's pager.
//
// Doing it by hand is an afternoon of fiddly work, and the same afternoon in every project: a
// temp file with a meaningful extension so the editor highlights it, $VISUAL before $EDITOR
// before a platform default, splitting a command like `code --wait` into its arguments, handing
// the terminal over and taking it back, reading the result, telling "unchanged" and "emptied"
// apart, and cleaning up on every path including the ones that failed.
//
// The zero value is not usable; start from [NewEditor].
type Editor struct {
	command   string
	extension string
	dir       string
}

// ErrEditAborted reports that the user did not provide new content: they quit without saving, or
// saved an empty buffer. Both are a deliberate "never mind" in every editor-driven CLI, so both
// are one sentinel rather than an error to inspect.
//
// It is a [UsageError]: the user can fix it by editing again.
var ErrEditAborted = UsageError(errors.New("rotini: edit aborted"))

// NewEditor returns an editor using $VISUAL, then $EDITOR, then a platform default.
func NewEditor() *Editor { return &Editor{extension: ".txt"} }

// WithCommand sets the editor command, overriding $VISUAL and $EDITOR. It is split on spaces, so
// "code --wait" works.
//
// Split on spaces, not parsed as a shell: an argument containing one cannot be expressed, the
// same limitation [Pager.WithCommand] has. A command that needs shell syntax should be a script,
// and pointed at by path.
func (e *Editor) WithCommand(command string) *Editor {
	e.command = command
	return e
}

// WithExtension sets the temp file's extension, which is how an editor decides how to highlight
// it — ".yaml" for a spec, ".md" for a message. The default is ".txt". A leading dot is added
// when missing.
func (e *Editor) WithExtension(ext string) *Editor {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	e.extension = ext
	return e
}

// WithDir puts the temp file in dir rather than the system temp directory, for an editor
// configured to refuse files outside a workspace. An empty dir restores the default.
func (e *Editor) WithDir(dir string) *Editor {
	e.dir = dir
	return e
}

// Edit writes initial to a temp file, opens it in the editor, waits, and returns what came back.
//
// It returns [ErrEditAborted] when the content is unchanged or the buffer was emptied — the two
// ways a user says "never mind" — so a caller branches on one sentinel rather than diffing:
//
//	body, err := rotini.NewEditor().WithExtension(".md").Edit(ctx, draft)
//	switch {
//	case errors.Is(err, rotini.ErrEditAborted):
//	    rtx.RecordInfo("no changes")
//	    return
//	case err != nil:
//	    rtx.HaltWith(err)
//	    return
//	}
//
// The editor inherits the terminal — it is an interactive program and must own the screen — so
// this cannot run while a [Spinner] is drawing. Stop the indicator first.
//
// The temp file is removed on every path.
func (e *Editor) Edit(ctx context.Context, initial []byte) ([]byte, error) {
	name, args := e.resolve()
	if name == "" {
		return nil, UsageError(errors.New("rotini: no editor: set $VISUAL or $EDITOR"))
	}

	file, err := os.CreateTemp(e.dir, "rotini-*"+e.extension)
	if err != nil {
		return nil, fmt.Errorf("rotini: create edit buffer: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)

	if _, err := file.Write(initial); err != nil {
		// The close error is deliberately dropped: the write already failed, and that is the
		// failure worth reporting. Returning a close error instead would name the symptom.
		_ = file.Close()
		return nil, fmt.Errorf("rotini: write edit buffer: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("rotini: write edit buffer: %w", err)
	}

	cmd := exec.CommandContext(ctx, name, append(args, path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rotini: %s: %w", filepath.Base(name), err)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("rotini: read edit buffer: %w", err)
	}
	if len(bytes.TrimSpace(edited)) == 0 || bytes.Equal(edited, initial) {
		return nil, ErrEditAborted
	}
	return edited, nil
}

// resolve picks the editor command: the configured one, then $VISUAL, then $EDITOR, then a
// platform default.
//
// $VISUAL comes first because that is the convention it exists for: $EDITOR may be a line editor
// for a dumb terminal, $VISUAL is the full-screen one. Tools that ignore the distinction are the
// reason users set both to the same thing.
func (e *Editor) resolve() (name string, args []string) {
	command := e.command
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if command != "" {
			break
		}
		command = os.Getenv(env)
	}
	if command == "" {
		command = defaultEditor()
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

// defaultEditor is the last resort when nothing is configured.
func defaultEditor() string {
	if os.Getenv("OS") == "Windows_NT" {
		return "notepad"
	}
	return "vi"
}
