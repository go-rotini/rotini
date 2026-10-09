package rotini

import (
	"bytes"
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestWriteOutputTo(t *testing.T) {
	t.Parallel()
	rtx, stdout := outContext("list")
	var w bytes.Buffer
	if err := rtx.WriteOutputTo(&w, sampleList, "table", renderList); err != nil {
		t.Fatal(err)
	}
	if w.String() != "table: 2 tasks\n" || stdout.Len() != 0 {
		t.Errorf("writer = %q, stdout = %q", w.String(), stdout.String())
	}
	if err := rtx.WriteOutputTo(&w, outTask{}, "json", nil); err == nil || CategoryOf(err) != CategoryInternal {
		t.Errorf("the wrong type: %v", err)
	}
}

func TestWriteOutputItemTo(t *testing.T) {
	t.Parallel()
	rtx, stdout := outContext("watch")
	var w bytes.Buffer
	for i := range 2 {
		if err := rtx.WriteOutputItemTo(&w, outTask{ID: i, Status: "open"}, "json", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(w.String(), "\n"); got != 2 || stdout.Len() != 0 {
		t.Errorf("writer = %q, stdout = %q", w.String(), stdout.String())
	}
	err := rtx.WriteOutputItemTo(failingWriter{syscall.EPIPE}, outTask{}, "json", nil)
	if !errors.Is(err, syscall.EPIPE) || !strings.HasPrefix(err.Error(), "taskr watch: write output: ") {
		t.Errorf("a failed write: %v", err)
	}
}
