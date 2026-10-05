package rotini

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

type outTask struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

type outList struct {
	Tasks []outTask `json:"tasks"`
}

const outListSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","required":["tasks"],` +
	`"properties":{"tasks":{"type":"array","items":{"$ref":"#/definitions/Task"}}},` +
	`"definitions":{"Task":{"type":"object","required":["id","status"],"properties":{"id":{"type":"integer"},"status":{"type":"string","enum":["open","done"]}}}}}`

const outTaskSchema = `{"type":"object","required":["id"],"properties":{"id":{"type":"integer"},"status":{"type":"string","enum":["open","done"]}}}`

// outDef is a program with a command of each kind: a whole output, a stream of items, and one
// that declares no output.
func outDef() Definition {
	return Definition{
		Name: "taskr",
		Commands: []CommandDef{
			{Name: "list", Output: &OutputDef{Type: reflect.TypeFor[outList](), Schema: outListSchema}},
			{Name: "watch", Output: &OutputDef{Type: reflect.TypeFor[outTask](), Schema: outTaskSchema}},
			{Name: "free"},
		},
	}
}

func outContext(cmd string) (*Context, *bytes.Buffer) {
	rtx := NewContextFor(outDef(), []string{cmd})
	var out bytes.Buffer
	rtx.Stdout = &out
	return rtx, &out
}

var sampleList = outList{Tasks: []outTask{{ID: 1, Status: "done"}, {ID: 2, Status: "open"}}}

func renderList(w io.Writer, format string, v outList) error {
	fmt.Fprintf(w, "%s: %d tasks\n", format, len(v.Tasks))
	return nil
}

func TestWriteOutput_formats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		format, want string
	}{
		{"", "{\n  \"tasks\": [\n    {\n      \"id\": 1,\n      \"status\": \"done\"\n    },\n    {\n      \"id\": 2,\n      \"status\": \"open\"\n    }\n  ]\n}\n"},
		{"json", "{\n  \"tasks\": ["},
		{"yaml", "tasks:\n  - id: 1\n    status: done\n"},
		{"toml", "[[tasks]]\nid = 1\nstatus = \"done\""},
		{"table", "table: 2 tasks\n"},
	}
	for _, tt := range tests {
		rtx, out := outContext("list")
		if err := rtx.WriteOutput(sampleList, tt.format, renderList); err != nil {
			t.Fatalf("%q: %v", tt.format, err)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Errorf("%q wrote:\n%s\nwant it to contain:\n%s", tt.format, out, tt.want)
		}
	}
}

func TestWriteOutput_programBugs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cmd  string
		do   func(rtx *Context) error
		want string
	}{
		{"no renderer for a human format", "list", func(rtx *Context) error { return rtx.WriteOutput(sampleList, "table", nil) },
			`taskr list: no renderer was passed for the output format "table"`},
		{"the wrong type", "list", func(rtx *Context) error { return rtx.WriteOutput(outTask{}, "json", nil) },
			`taskr list: the output written is a rotini.outTask, but the command declares rotini.outList`},
		{"toml as a stream", "free", func(rtx *Context) error { return rtx.WriteOutputItem(outTask{}, "toml", nil) },
			"taskr free: write output as toml: toml cannot be written as a stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rtx, out := outContext(tt.cmd)
			err := tt.do(rtx)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if CategoryOf(err) != CategoryInternal {
				t.Errorf("category = %v, want internal: it is the program's bug", CategoryOf(err))
			}
			if out.Len() != 0 {
				t.Errorf("something was written: %q", out)
			}
		})
	}
}

func TestWriteOutput_accepts(t *testing.T) {
	t.Parallel()
	// A pointer to the declared type, and a value passed as any, are the declared type.
	rtx, _ := outContext("list")
	if err := rtx.WriteOutput(&sampleList, "json", nil); err != nil {
		t.Errorf("pointer: %v", err)
	}
	var v any = sampleList
	if err := rtx.WriteOutput(v, "json", nil); err != nil {
		t.Errorf("any: %v", err)
	}
	// A command that declares no output writes whatever it is given, json by default.
	rtx, out := outContext("free")
	if err := rtx.WriteOutput(map[string]int{"n": 1}, "", nil); err != nil || out.String() != "{\n  \"n\": 1\n}\n" {
		t.Errorf("undeclared: %v %q", err, out)
	}
	// Any format the handler passes is written: rotini's own three, or the renderer's.
	rtx, out = outContext("list")
	if err := rtx.WriteOutput(sampleList, "wide", renderList); err != nil || out.String() != "wide: 2 tasks\n" {
		t.Errorf("wide: %v %q", err, out)
	}
}

func TestWriteOutput_rendererError(t *testing.T) {
	t.Parallel()
	boom := errors.New("the terminal is too narrow")
	rtx, out := outContext("list")
	err := rtx.WriteOutput(sampleList, "table", func(w io.Writer, _ string, _ outList) error {
		fmt.Fprint(w, "partial")
		return boom
	})
	if err != boom { // the renderer's own error, returned as is
		t.Errorf("err = %v, want the renderer's own error", err)
	}
	if out.Len() != 0 {
		t.Errorf("a failed render wrote %q", out)
	}
}

func TestWriteOutputItem(t *testing.T) {
	t.Parallel()
	rtx, out := outContext("watch")
	for _, item := range sampleList.Tasks {
		if err := rtx.WriteOutputItem(item, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := "{\"id\":1,\"status\":\"done\"}\n{\"id\":2,\"status\":\"open\"}\n"; out.String() != want {
		t.Errorf("json stream = %q, want %q", out, want)
	}
	rtx, out = outContext("watch")
	for _, item := range sampleList.Tasks {
		if err := rtx.WriteOutputItem(item, "yaml", nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := "---\nid: 1\nstatus: done\n---\nid: 2\nstatus: open\n"; out.String() != want {
		t.Errorf("yaml stream = %q, want %q", out, want)
	}
}

func TestWriteOutput_checks(t *testing.T) {
	t.Parallel()
	bad := outList{Tasks: []outTask{{ID: 1, Status: "done"}, {ID: 2, Status: "doing"}}}

	// Off by default: the value is written as is.
	rtx, out := outContext("list")
	if err := rtx.WriteOutput(bad, "json", nil); err != nil || !strings.Contains(out.String(), "doing") {
		t.Fatalf("unchecked: %v %q", err, out)
	}

	rtx, out = outContext("list")
	rtx.outputChecks = true
	err := rtx.WriteOutput(bad, "json", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "taskr list: output does not match its contract: output.tasks[1].status: ") {
		t.Fatalf("checked: err = %v", err)
	}
	if CategoryOf(err) != CategoryInternal || out.Len() != 0 {
		t.Errorf("checked: category %v, wrote %q", CategoryOf(err), out)
	}

	// CheckOutput checks without writing, whatever the program's setting.
	rtx, _ = outContext("list")
	if err := rtx.CheckOutput(bad); err == nil || !strings.Contains(err.Error(), "output.tasks[1].status") {
		t.Errorf("CheckOutput(bad) = %v", err)
	}
	if err := rtx.CheckOutput(sampleList); err != nil {
		t.Errorf("CheckOutput(good) = %v", err)
	}
	if err := rtx.CheckOutput(outTask{}); err == nil || !strings.Contains(err.Error(), "the output written is a rotini.outTask") {
		t.Errorf("CheckOutput(wrong type) = %v", err)
	}
	rtx, _ = outContext("free")
	if err := rtx.CheckOutput(sampleList); err == nil || err.Error() != "taskr free: declares no output to check against" {
		t.Errorf("CheckOutput on an undeclared output = %v", err)
	}
}

func TestProgramWithOutputChecks(t *testing.T) {
	t.Parallel()
	p := NewProgram(outDef(), nil).WithOutputChecks()
	if !p.newRunContext().outputChecks {
		t.Error("WithOutputChecks did not reach the run's Context")
	}
	if NewProgram(outDef(), nil).newRunContext().outputChecks {
		t.Error("output checks are on by default")
	}
}

func TestDecodeOutput(t *testing.T) {
	t.Parallel()
	p := NewProgram(outDef(), nil)

	list, err := DecodeOutput[outList](p, []byte("tasks:\n  - id: 1\n    status: done\n"), "yaml")
	if err != nil || len(list.Tasks) != 1 || list.Tasks[0].Status != "done" {
		t.Errorf("yaml: %+v %v", list, err)
	}
	list, err = DecodeOutput[outList](p, []byte("[[tasks]]\nid = 2\nstatus = \"open\"\n"), "toml")
	if err != nil || len(list.Tasks) != 1 || list.Tasks[0].ID != 2 {
		t.Errorf("toml: %+v %v", list, err)
	}
	items, err := DecodeOutput[[]outTask](p, []byte("{\"id\":1}\n{\"id\":2,\"status\":\"open\"}\n"), "json")
	if err != nil || len(items) != 2 || items[1].Status != "open" {
		t.Errorf("json stream: %+v %v", items, err)
	}
	items, err = DecodeOutput[[]outTask](p, []byte("---\nid: 1\n---\nid: 2\n"), "yaml")
	if err != nil || len(items) != 2 {
		t.Errorf("yaml stream: %+v %v", items, err)
	}
	// The declared type itself is one document, never a stream.
	one, err := DecodeOutput[outTask](p, []byte(`{"id":7}`), "json")
	if err != nil || one.ID != 7 {
		t.Errorf("one item: %+v %v", one, err)
	}

	for _, tt := range []struct {
		name string
		err  func() error
		want string
	}{
		{"off contract", func() error {
			_, err := DecodeOutput[outList](p, []byte(`{"tasks":[{"id":1,"status":"doing"}]}`), "json")
			return err
		}, "taskr list: output does not match its contract: output.tasks[0].status: "},
		{"a stream item off contract", func() error {
			_, err := DecodeOutput[[]outTask](p, []byte("{\"id\":1}\n{\"status\":\"open\"}\n"), "json")
			return err
		}, "item 1: taskr watch: output does not match its contract: output: "},
		{"a human format", func() error {
			_, err := DecodeOutput[outList](p, nil, "table")
			return err
		}, `DecodeOutput reads json, yaml or toml, not "table"`},
		{"a type no command declares", func() error {
			_, err := DecodeOutput[string](p, []byte(`"x"`), "json")
			return err
		}, "no command declares string as its output"},
		{"bad json", func() error {
			_, err := DecodeOutput[outList](p, []byte(`{`), "json")
			return err
		}, "taskr list: decode the output as json: "},
	} {
		if err := tt.err(); err == nil || !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want prefix %q", tt.name, err, tt.want)
		}
	}
}

func TestOutputPath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":                "output",
		"/tasks/2/status": "output.tasks[2].status",
		"/a~1b/c~0d":      "output.a/b.c~d",
	} {
		if got := outputPath(in); got != want {
			t.Errorf("outputPath(%q) = %q, want %q", in, got, want)
		}
	}
}
