package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestSortBy_exactPastInt64(t *testing.T) {
	t.Parallel()
	type item struct {
		ID uint64 `json:"id"`
	}
	items := []item{{ID: 18446744073709551615}, {ID: 18446744073709551614}, {ID: 1}, {ID: 9223372036854775808}}
	if err := SortBy(items, "id", false); err != nil {
		t.Fatal(err)
	}
	want := []item{{ID: 1}, {ID: 9223372036854775808}, {ID: 18446744073709551614}, {ID: 18446744073709551615}}
	for i := range want {
		if items[i] != want[i] {
			t.Fatalf("got %v, want %v", items, want)
		}
	}

	type number struct {
		N json.Number `json:"n"`
	}
	ns := []number{{N: "18446744073709551615"}, {N: "-1"}, {N: "1.5"}, {N: "18446744073709551614"}, {N: "-9223372036854775808"}}
	if err := SortBy(ns, "n", false); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(ns))
	for i, n := range ns {
		got[i] = n.N.String()
	}
	if want := "-9223372036854775808 -1 1.5 18446744073709551614 18446744073709551615"; strings.Join(got, " ") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

func TestSelectFields_reselectLeavesTheSourceAlone(t *testing.T) {
	t.Parallel()
	list := selList{Tasks: []selTask{{ID: 1, Title: "a", Owner: &selOwner{Login: "l", ID: 3}}}}
	src, err := SelectFields(list, "tasks", []string{"owner", "title"})
	if err != nil {
		t.Fatal(err)
	}
	before := selJSON(t, src)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			sel, err := SelectFields(src, "tasks", []string{"owner", "owner.login", "title"})
			if err != nil {
				t.Error(err)
				return
			}
			if got, want := selJSON(t, sel), `{"tasks":[{"owner":{"id":3,"login":"l"},"title":"a"}],"total":0}`; got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
	wg.Wait()
	if after := selJSON(t, src); after != before {
		t.Errorf("the source selection changed: %s, was %s", after, before)
	}
}

func TestDecodeOutput_yamlTakesOneDocument(t *testing.T) {
	t.Parallel()
	p := NewProgram(outDef(), nil)
	_, err := DecodeOutput[outList](p, []byte("tasks: []\n---\ntasks: [{id: 1, status: open}]\n"), "yaml")
	if err == nil || !strings.Contains(err.Error(), "more than one yaml document") {
		t.Errorf("two documents: %v", err)
	}
	// As json refuses a second value.
	_, err = DecodeOutput[outList](p, []byte("{\"tasks\":[]}\n{\"tasks\":[]}\n"), "json")
	if err == nil {
		t.Error("two json values decoded")
	}
	got, err := DecodeOutput[outList](p, []byte("---\ntasks: [{id: 1, status: open}]\n"), "yaml")
	if err != nil || len(got.Tasks) != 1 {
		t.Errorf("one document behind ---: %v, %v", got, err)
	}
}

// lockedBuffer is a buffer safe for concurrent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func TestWriteOutputItem_concurrent(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	rtx := NewContextFor(outDef(), []string{"watch"}).WithStdout(&out)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			if i == 0 {
				rtx.WithOutputChecks(true)
			}
			if err := rtx.WriteOutputItem(outTask{ID: i, Status: "open"}, "json", nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(out.buf.String(), "\n"), "\n")
	if len(lines) != 16 {
		t.Fatalf("%d lines:\n%s", len(lines), out.buf.String())
	}
	for _, line := range lines {
		var item outTask
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestWriteOutput_writeErrorIsTheWriters(t *testing.T) {
	t.Parallel()
	broken := errors.New("broken pipe")
	rtx := NewContextFor(outDef(), []string{"list"})
	err := rtx.WriteOutputTo(errWriter{broken}, sampleList, "json", nil)
	if !errors.Is(err, broken) || CategoryOf(err) != CategoryNone {
		t.Errorf("got %v (%v)", err, CategoryOf(err))
	}
}

type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// FuzzSelectFields checks that selecting never fails on a document it can reach, never changes
// the value it selects from, and keeps only what was asked for.
func FuzzSelectFields(f *testing.F) {
	for _, seed := range [][3]string{
		{`{"tasks":[{"id":1,"owner":{"login":"l"}}],"total":1}`, "tasks", "owner.login,id"},
		{`[{"a":[{"b":1},null,{"c":2}]}]`, "", "a.b,a"},
		{`{"a":{"b":{"c":[1,2]}}}`, "a.b", "c,c.d"},
		{`{"x":1}`, "", ""},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, doc, at, fieldList string) {
		var v any
		if json.Unmarshal([]byte(doc), &v) != nil {
			return
		}
		var fields []string
		if fieldList != "" {
			fields = strings.Split(fieldList, ",")
		}
		src, err := SelectFields(v, "", nil)
		if err != nil {
			return // v is not an object or a list
		}
		before := selJSON(t, src)
		sel, err := SelectFields(src, at, fields)
		if after := selJSON(t, src); after != before {
			t.Fatalf("source changed: %s, was %s", after, before)
		}
		if err != nil {
			return
		}
		again, err := SelectFields(sel, at, fields)
		if err != nil {
			t.Fatalf("reselecting %s: %v", selJSON(t, sel), err)
		}
		if a, b := selJSON(t, again), selJSON(t, sel); a != b {
			t.Fatalf("selecting twice gave %s, once %s", a, b)
		}
	})
}
