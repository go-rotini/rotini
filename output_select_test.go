package rotini

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type selOwner struct {
	Login string `json:"login"`
	ID    int    `json:"id"`
}

type selTask struct {
	ID     int       `json:"id"`
	Title  string    `json:"title"`
	Status string    `json:"status,omitempty"`
	Owner  *selOwner `json:"owner,omitempty"`
}

type selList struct {
	Tasks []selTask `json:"tasks"`
	Total int       `json:"total"`
}

func selJSON(t *testing.T, s Selection) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSelectFields(t *testing.T) {
	t.Parallel()
	list := selList{Total: 2, Tasks: []selTask{
		{ID: 1, Title: "a", Status: "open", Owner: &selOwner{Login: "ann", ID: 7}},
		{ID: 2, Title: "b"},
	}}
	tests := []struct {
		name   string
		v      any
		at     string
		fields []string
		want   string
	}{
		{"struct", list.Tasks[0], "", []string{"id", "title"}, `{"id":1,"title":"a"}`},
		{"pointer", &list.Tasks[0], "", []string{"status"}, `{"status":"open"}`},
		{"slice", list.Tasks, "", []string{"id"}, `[{"id":1},{"id":2}]`},
		{"map", map[string]any{"a": 1, "b": 2}, "", []string{"b"}, `{"b":2}`},
		{"envelope keeps the rest", list, "tasks", []string{"title"}, `{"tasks":[{"title":"a"},{"title":"b"}],"total":2}`},
		{"a missing field is left out", list, "tasks", []string{"status"}, `{"tasks":[{"status":"open"},{}],"total":2}`},
		{"dot paths join under one parent", list.Tasks, "", []string{"owner.login", "owner.id", "id"}, `[{"id":1,"owner":{"id":7,"login":"ann"}},{"id":2}]`},
		{"an absent envelope property", map[string]any{"total": 0}, "tasks", []string{"id"}, `{"total":0}`},
		{"null items", map[string]any{"tasks": nil}, "tasks", []string{"id"}, `{"tasks":null}`},
		{"a null item", []any{nil, map[string]any{"id": 1}}, "", []string{"id"}, `[null,{"id":1}]`},
		{"a nil slice", []selTask(nil), "", []string{"id"}, `null`},
		{"big numbers stay exact", map[string]any{"n": uint64(18446744073709551615)}, "", []string{"n"}, `{"n":18446744073709551615}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := SelectFields(tt.v, tt.at, tt.fields)
			if err != nil {
				t.Fatal(err)
			}
			if got := selJSON(t, s); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSelectFields_reselect(t *testing.T) {
	t.Parallel()
	s, err := SelectFields(selTask{ID: 1, Title: "a", Status: "open"}, "", []string{"id", "title"})
	if err != nil {
		t.Fatal(err)
	}
	s, err = SelectFields(s, "", []string{"title"})
	if err != nil {
		t.Fatal(err)
	}
	if got := selJSON(t, s); got != `{"title":"a"}` || s.from.Name() != "selTask" {
		t.Errorf("got %s from %v", got, s.from)
	}
}

func TestSelectFields_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		v      any
		at     string
		fields []string
		want   string
	}{
		{"nil", nil, "", []string{"id"}, "SelectFields: no value"},
		{"at a scalar", selList{Total: 1}, "total", []string{"id"}, `SelectFields: at "total": total is a number, not an object or a list`},
		{"a scalar value", 3, "", []string{"id"}, `SelectFields: at "": the value is a number, not an object or a list`},
		{"an empty at segment", selList{}, "tasks.", []string{"id"}, `SelectFields: at "tasks." has an empty segment`},
		{"an empty field", selTask{}, "", []string{""}, `SelectFields: field "" has an empty segment`},
		{"a zero selection", Selection{}, "", []string{"id"}, "SelectFields: a zero Selection; make one with SelectFields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := SelectFields(tt.v, tt.at, tt.fields); err == nil || err.Error() != tt.want {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

type sortItem struct {
	N    any    `json:"n,omitempty"`
	Name string `json:"name"`
}

func sortNames(items []sortItem) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, " ")
}

func TestSortBy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items []sortItem
		desc  bool
		want  string
	}{
		{"ints", []sortItem{{3, "c"}, {1, "a"}, {2, "b"}}, false, "a b c"},
		{"ints descending", []sortItem{{3, "c"}, {1, "a"}, {2, "b"}}, true, "c b a"},
		{"floats and ints", []sortItem{{2.5, "b"}, {1, "a"}, {10, "c"}}, false, "a b c"},
		{"strings byte-wise", []sortItem{{"b", "b"}, {"B", "B"}, {"a", "a"}}, false, "B a b"},
		{"RFC 3339 times", []sortItem{{"2026-01-02T00:00:00+05:00", "b"}, {"2026-01-01T23:00:00Z", "c"}, {"2025-12-31T00:00:00Z", "a"}}, false, "a b c"},
		{"bools", []sortItem{{true, "t"}, {false, "f"}}, false, "f t"},
		{"missing and null last", []sortItem{{nil, "x"}, {2, "b"}, {nil, "y"}, {1, "a"}}, false, "a b x y"},
		{"missing last descending too", []sortItem{{nil, "x"}, {1, "a"}, {2, "b"}}, true, "b a x"},
		{"stable", []sortItem{{1, "a"}, {0, "z"}, {1, "b"}, {1, "c"}}, false, "z a b c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := SortBy(tt.items, "n", tt.desc); err != nil {
				t.Fatal(err)
			}
			if got := sortNames(tt.items); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortBy_nested(t *testing.T) {
	t.Parallel()
	items := []selTask{{ID: 1, Owner: &selOwner{Login: "zed"}}, {ID: 2}, {ID: 3, Owner: &selOwner{Login: "amy"}}}
	if err := SortBy(items, "owner.login", false); err != nil {
		t.Fatal(err)
	}
	if items[0].ID != 3 || items[1].ID != 1 || items[2].ID != 2 {
		t.Errorf("order = %d %d %d", items[0].ID, items[1].ID, items[2].ID)
	}
}

func TestSortBy_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items []sortItem
		field string
		want  string
	}{
		{"mixed kinds", []sortItem{{1, "a"}, {"x", "b"}}, "n", `SortBy: field "n" holds both numbers and strings`},
		{"a list", []sortItem{{[]int{1}, "a"}}, "n", `SortBy: field "n" holds a list, which does not sort`},
		{"an object", []sortItem{{map[string]int{"a": 1}, "a"}}, "n", `SortBy: field "n" holds a object, which does not sort`},
		{"an empty field", []sortItem{{1, "a"}}, "", `SortBy: field "" has an empty segment`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			before := sortNames(tt.items)
			if err := SortBy(tt.items, tt.field, false); err == nil || err.Error() != tt.want {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			if sortNames(tt.items) != before {
				t.Error("items changed on error")
			}
		})
	}
	if err := SortBy([]int{2, 1}, "n", false); err == nil || err.Error() != "SortBy: item 0 is a number, not an object" {
		t.Errorf("scalars: %v", err)
	}
	if err := SortBy([]sortItem(nil), "n", false); err != nil {
		t.Errorf("nil slice: %v", err)
	}
}

func TestPartialSchema(t *testing.T) {
	t.Parallel()
	got, err := partialSchema([]byte(`{"required":["a"],"properties":{"required":{"type":"string"},"a":{"required":["x"],"items":{"required":["y"]}}},` +
		`"definitions":{"T":{"required":["z"]}},"oneOf":[{"required":["w"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"definitions":{"T":{}},"oneOf":[{}],"properties":{"a":{"items":{}},"required":{"type":"string"}}}`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if _, err := partialSchema([]byte("{")); err == nil {
		t.Error("a broken schema should fail")
	}
}

func TestWriteOutput_selection(t *testing.T) {
	t.Parallel()
	sel, err := SelectFields(sampleList, "tasks", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ format, want string }{
		{"json", "{\n  \"tasks\": [\n    {\n      \"id\": 1\n    },\n    {\n      \"id\": 2\n    }\n  ]\n}\n"},
		{"yaml", "tasks:\n  - id: 1\n  - id: 2\n"},
		{"toml", "[[tasks]]\nid = 1\n\n[[tasks]]\nid = 2\n"},
		{"table", "table\n"},
	}
	for _, tt := range tests {
		rtx, out := outContext("list")
		rtx.WithOutputChecks(true) // the partial schema: required status is left out
		render := func(w io.Writer, format string, _ Selection) error {
			_, err := io.WriteString(w, format+"\n")
			return err
		}
		if err := rtx.WriteOutput(sel, tt.format, render); err != nil {
			t.Fatalf("%s: %v", tt.format, err)
		}
		if out.String() != tt.want {
			t.Errorf("%s wrote %q, want %q", tt.format, out, tt.want)
		}
	}
}

func TestWriteOutput_selectionChecks(t *testing.T) {
	t.Parallel()
	other, _ := SelectFields(outTask{ID: 1}, "", []string{"id"})
	bad, _ := SelectFields(outList{Tasks: []outTask{{ID: 1, Status: "doing"}}}, "tasks", []string{"status"})
	tests := []struct {
		name string
		do   func(rtx *Context) error
		want string
	}{
		{"selected from another type", func(rtx *Context) error { return rtx.WriteOutput(other, "json", nil) },
			"taskr list: the output written is a rotini.outTask, but the command declares rotini.outList"},
		{"off the schema", func(rtx *Context) error { return rtx.WriteOutput(bad, "json", nil) },
			"taskr list: output does not match its contract: output.tasks[0].status: value is not in enum"},
		{"a zero selection", func(rtx *Context) error { return rtx.WriteOutput(Selection{}, "json", nil) },
			"taskr list: the output written is a zero Selection; make one with SelectFields"},
		{"checked without writing", func(rtx *Context) error { return rtx.CheckOutput(bad) },
			"taskr list: output does not match its contract: output.tasks[0].status: value is not in enum"},
		{"a zero selection checked", func(rtx *Context) error { return rtx.CheckOutput(Selection{}) },
			"taskr list: the output is a zero Selection; make one with SelectFields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rtx, out := outContext("list")
			rtx.WithOutputChecks(true)
			err := tt.do(rtx)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v\nwant %q", err, tt.want)
			}
			if CategoryOf(err) != CategoryInternal {
				t.Errorf("category = %v, want internal", CategoryOf(err))
			}
			if out.Len() != 0 {
				t.Errorf("wrote %q", out)
			}
		})
	}
	rtx, _ := outContext("list")
	good, _ := SelectFields(sampleList, "tasks", []string{"status"})
	if err := rtx.CheckOutput(good); err != nil {
		t.Errorf("CheckOutput(selection) = %v", err)
	}
}

func TestWriteOutputItem_selection(t *testing.T) {
	t.Parallel()
	rtx, out := outContext("watch")
	rtx.WithOutputChecks(true)
	for _, task := range sampleList.Tasks {
		sel, err := SelectFields(task, "", []string{"status"})
		if err != nil {
			t.Fatal(err)
		}
		if err := rtx.WriteOutputItem(sel, "json", nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := "{\"status\":\"done\"}\n{\"status\":\"open\"}\n"; out.String() != want {
		t.Errorf("wrote %q, want %q", out, want)
	}
}
