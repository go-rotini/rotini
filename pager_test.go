package rotini

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// THE contract that keeps `mycli list | grep x` working: with no terminal to page
// into, the text goes straight to the writer.
func TestPager_passesThroughOnNonTerminal(t *testing.T) {
	var buf bytes.Buffer
	if err := NewPager(&buf).Page(context.Background(), "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "line one\nline two\n" {
		t.Errorf("passthrough = %q, want the text newline-terminated", buf.String())
	}
}

func TestPager_emptyTextWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := NewPager(&buf).Page(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty page wrote %q", buf.String())
	}
}

// A pager that cannot start is not an error: displaying the output unpaged beats
// not displaying it.
func TestPager_unstartableCommandStillShowsOutput(t *testing.T) {
	var buf bytes.Buffer
	err := NewPager(&buf).WithEnabled(true).
		WithCommand("definitely-not-a-real-pager-xyz").
		Page(context.Background(), "important")
	if err != nil {
		t.Errorf("a broken pager returned %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "important") {
		t.Errorf("output lost when the pager failed: %q", buf.String())
	}
}

// $PAGER is honored, and the conventional "no paging" values mean passthrough.
func TestPager_resolvesCommand(t *testing.T) {
	p := NewPager(&bytes.Buffer{})
	t.Setenv("PAGER", "less -R")
	if name, args := p.resolve(); name != "less" || len(args) != 1 || args[0] != "-R" {
		t.Errorf("resolve() = (%q, %v), want less -R", name, args)
	}
	t.Setenv("PAGER", "cat")
	if name, _ := p.resolve(); name != "" {
		t.Errorf("PAGER=cat should mean no paging, got %q", name)
	}
	t.Setenv("PAGER", "")
	if name, _ := p.resolve(); name != "less" {
		t.Errorf("default pager = %q, want less", name)
	}
	// An explicit command beats the environment.
	t.Setenv("PAGER", "less")
	if name, _ := p.WithCommand("more").resolve(); name != "more" {
		t.Errorf("WithCommand did not override $PAGER")
	}
}

// A real pager run: `cat` is refused as a pager, so use `sh -c` to prove the text
// actually reaches a child process's stdin.
func TestPager_pipesThroughTheCommand(t *testing.T) {
	var buf bytes.Buffer
	err := NewPager(&buf).WithEnabled(true).WithCommand("sh -c cat").
		Page(context.Background(), "paged text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "paged text") {
		t.Errorf("pager output = %q, want the piped text", buf.String())
	}
}
