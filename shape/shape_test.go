package shape

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/rotini"
)

type task struct {
	ID     int       `json:"id"`
	Title  string    `json:"title"`
	Status string    `json:"status,omitempty"`
	Due    time.Time `json:"due,omitzero"`
	Tags   []string  `json:"tags,omitempty"`
	Note   *string   `json:"note,omitempty"`
	Secret string    `json:"-"`
	hidden string
}

type taskList struct {
	Tasks []task `json:"tasks"`
	Total int    `json:"total"`
}

// selection encodes itself, as a projection of an output does.
type selection struct{ fields map[string]any }

func (s selection) MarshalJSON() ([]byte, error) { return json.Marshal(s.fields) }

func mustParse(t *testing.T, src string) Template {
	t.Helper()
	var tmpl Template
	if err := tmpl.UnmarshalText([]byte(src)); err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return tmpl
}

func execute(t *testing.T, src string, v any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := mustParse(t, src).Execute(&buf, v); err != nil {
		t.Fatalf("execute %q: %v", src, err)
	}
	return buf.String()
}

func TestExecute(t *testing.T) {
	due := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	note := "soon"
	list := taskList{
		Tasks: []task{
			{ID: 1, Title: "write the spec", Status: "done", Due: due, Tags: []string{"a", "b"}, Note: &note, Secret: "s", hidden: "h"},
			{ID: 2, Title: "ship it"},
		},
		Total: 2,
	}
	tests := []struct {
		name, src string
		v         any
		want      string
	}{
		{"struct", `{{.title}}`, list.Tasks[0], "write the spec"},
		{"pointer", `{{.id}}`, &list.Tasks[1], "2"},
		{"slice", `{{range .}}{{.id}} {{end}}`, list.Tasks, "1 2 "},
		{"map", `{{.b}}{{.a}}`, map[string]int{"a": 1, "b": 2}, "21"},
		{"int keys", `{{index . "7"}}`, map[int]string{7: "seven"}, "seven"},
		{"envelope", `{{range .tasks}}{{.title}}{{"\n"}}{{end}}{{.total}}`, list, "write the spec\nship it\n2"},
		{"time", `{{.due}}`, list.Tasks[0], "2026-10-08T09:00:00Z"},
		{"self-encoding", `{{.title}}`, selection{map[string]any{"title": "picked"}}, "picked"},
		{"omitempty as zero", `[{{.status}}][{{.tags}}][{{.note}}][{{.due}}]`, list.Tasks[1], "[][<no value>][<no value>][0001-01-01T00:00:00Z]"},
		{"pointer field", `{{.note}}`, list.Tasks[0], "soon"},
		{"if on an empty field", `{{if .status}}set{{else}}unset{{end}}`, list.Tasks[1], "unset"},
		{"eq on a string", `{{if eq .status "done"}}yes{{end}}`, list.Tasks[0], "yes"},
		{"float", `{{.}}`, 1e21, "1e+21"},
		{"small float", `{{.}}`, 0.5, "0.5"},
		{"bytes", `{{.}}`, []byte("hi"), "aGk="},
		{"json.Number", `{{.}}`, json.Number("12.50"), "12.50"},
		{"nil", `{{.}}`, nil, "<no value>"},
		{"json", `{{json .}}`, list.Tasks[1], `{"due":"0001-01-01T00:00:00Z","id":2,"note":null,"status":"","tags":null,"title":"ship it"}`},
		{"join", `{{join ", " .tags}}`, list.Tasks[0], "a, b"},
		{"join mixed", `{{join "|" .}}`, []any{"x", 1, true, nil, map[string]int{"k": 1}}, `x|1|true||{"k":1}`},
		{"join pipeline", `{{.tags | join "+"}}`, list.Tasks[0], "a+b"},
		{"join nil", `[{{join "," .tags}}]`, list.Tasks[1], "[]"},
		{"printf", `{{printf "%-4s|" .status}}`, list.Tasks[0], "done|"},
		{"len", `{{len .tasks}}`, list, "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := execute(t, tt.src, tt.v); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecuteNamesJSONPropertiesOnly(t *testing.T) {
	v := task{ID: 1, Title: "x", Secret: "s", hidden: "h"}
	for _, src := range []string{`{{.Title}}`, `{{.Secret}}`, `{{.hidden}}`, `{{.titel}}`} {
		var buf bytes.Buffer
		err := mustParse(t, src).Execute(&buf, v)
		if err == nil {
			t.Errorf("%s: no error", src)
			continue
		}
		if !errors.Is(err, rotini.ErrUsage) {
			t.Errorf("%s: %v is not a usage error", src, err)
		}
		if strings.HasPrefix(err.Error(), "template: template") {
			t.Errorf("%s: message keeps text/template's prefix: %v", src, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: wrote %q on failure", src, buf.String())
		}
	}
}

func TestExecuteWritesNothingOnFailure(t *testing.T) {
	var buf bytes.Buffer
	err := mustParse(t, `{{.title}} then {{.nope}}`).Execute(&buf, task{Title: "x"})
	if !errors.Is(err, rotini.ErrUsage) {
		t.Fatalf("got %v, want a usage error", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
	if want := `template:1:18: executing "template" at <.nope>: map has no entry for key "nope"`; err.Error() != want {
		t.Errorf("message %q, want %q", err.Error(), want)
	}
}

func TestExecuteFunctionErrorsAreUsageErrors(t *testing.T) {
	var buf bytes.Buffer
	err := mustParse(t, `{{join "," .title}}`).Execute(&buf, task{Title: "x"})
	if !errors.Is(err, rotini.ErrUsage) {
		t.Fatalf("got %v, want a usage error", err)
	}
}

func TestExecuteValueWithoutJSONForm(t *testing.T) {
	type bad struct {
		F func() `json:"f"`
	}
	tests := []any{bad{F: func() {}}, map[float64]int{1: 1}, 1i}
	for _, v := range tests {
		var buf bytes.Buffer
		err := mustParse(t, `{{.}}`).Execute(&buf, v)
		if err == nil {
			t.Errorf("%T: no error", v)
			continue
		}
		if errors.Is(err, rotini.ErrUsage) {
			t.Errorf("%T: %v is a usage error, but the program is at fault", v, err)
		}
	}
}

type node struct {
	Name string `json:"name"`
	Next *node  `json:"next"`
}

func TestExecuteCycle(t *testing.T) {
	n := &node{Name: "a"}
	n.Next = n
	var buf bytes.Buffer
	if err := mustParse(t, `{{.name}}`).Execute(&buf, n); err == nil {
		t.Fatal("a cyclic value executed")
	}
}

type Base struct {
	ID   int    `json:"id"`
	Kind string `json:"kind"`
}

type inner struct {
	Deep string `json:"deep"`
}

type withEmbedded struct {
	Base
	*inner
	Kind  string `json:"kind"`
	Count int    `json:"count,string"`
	Named Base   `json:"named"`
}

func TestExecuteFollowsJSONFieldRules(t *testing.T) {
	v := withEmbedded{Base: Base{ID: 7, Kind: "base"}, Kind: "outer", Count: 3, Named: Base{ID: 9}}
	got := execute(t, `{{.id}} {{.kind}} {{.count}} {{.named.id}} [{{.deep}}]`, v)
	if want := `7 outer 3 9 [<no value>]`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	v.inner = &inner{Deep: "d"}
	if got := execute(t, `{{.deep}}`, v); got != "d" {
		t.Errorf("promoted through a pointer: got %q", got)
	}
}

func TestZeroTemplate(t *testing.T) {
	var tmpl Template
	if !tmpl.IsZero() {
		t.Fatal("zero Template is not zero")
	}
	if err := tmpl.UnmarshalText(nil); err != nil || !tmpl.IsZero() {
		t.Fatalf("empty text: %v, zero %v", err, tmpl.IsZero())
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, task{}); err != nil || buf.Len() != 0 {
		t.Fatalf("zero Template wrote %q, %v", buf.String(), err)
	}
}

func TestParseError(t *testing.T) {
	for _, src := range []string{`{{`, `{{.title`, `{{nosuchfunc .}}`, `{{end}}`} {
		var tmpl Template
		err := tmpl.UnmarshalText([]byte(src))
		if err == nil {
			t.Errorf("%q parsed", src)
			continue
		}
		if !strings.HasPrefix(err.Error(), "template:1:") {
			t.Errorf("%q: message %q", src, err)
		}
		if !tmpl.IsZero() {
			t.Errorf("%q: a failed parse left a template", src)
		}
	}
}

func TestParseFailureKeepsPreviousValue(t *testing.T) {
	tmpl := mustParse(t, `{{.title}}`)
	if err := tmpl.UnmarshalText([]byte(`{{`)); err == nil {
		t.Fatal("bad template parsed")
	}
	if tmpl.String() != `{{.title}}` {
		t.Errorf("a failed parse changed the template to %q", tmpl.String())
	}
}

func TestMarshalTextRoundTrip(t *testing.T) {
	src := `{{range .tasks}}{{.title | printf "%q"}}{{end}}`
	tmpl := mustParse(t, src)
	text, err := tmpl.MarshalText()
	if err != nil || string(text) != src || tmpl.String() != src {
		t.Fatalf("MarshalText %q, %v; String %q", text, err, tmpl.String())
	}
	var again Template
	if err := again.UnmarshalText(text); err != nil || again.String() != src {
		t.Fatalf("round trip: %q, %v", again.String(), err)
	}
	b, err := json.Marshal(struct {
		Format Template `json:"Format"`
	}{tmpl})
	if err != nil || string(b) != `{"Format":"{{range .tasks}}{{.title | printf \"%q\"}}{{end}}"}` {
		t.Fatalf("json: %s, %v", b, err)
	}
}

func TestRender(t *testing.T) {
	render := Render[task](mustParse(t, `{{.id}}: {{.title}}`))
	var buf bytes.Buffer
	for _, item := range []task{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}} {
		if err := render(&buf, "template", item); err != nil {
			t.Fatal(err)
		}
	}
	if want := "1: a\n2: b\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRenderError(t *testing.T) {
	var buf bytes.Buffer
	err := Render[task](mustParse(t, `{{.nope}}`))(&buf, "template", task{})
	if !errors.Is(err, rotini.ErrUsage) {
		t.Fatalf("got %v, want a usage error", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
}

func FuzzTemplate(f *testing.F) {
	for _, seed := range []string{
		`{{.title}}`, `{{range .tasks}}{{.id}}{{end}}`, `{{json .}}`, `{{join "," .tags}}`,
		`{{define "x"}}{{template "x" .}}{{end}}{{template "x" .}}`, `{{call .title}}`, `{{.title.Int64}}`,
		`{{index .tasks 9}}`, `{{printf "%v" .}}`, `{{`, ``,
	} {
		f.Add(seed)
	}
	data := taskList{Tasks: []task{{ID: 1, Title: "a", Tags: []string{"x"}}}, Total: 1}
	f.Fuzz(func(t *testing.T, src string) {
		var tmpl Template
		if err := tmpl.UnmarshalText([]byte(src)); err != nil {
			return
		}
		if tmpl.String() != src {
			t.Fatalf("String %q, want %q", tmpl.String(), src)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil && !errors.Is(err, rotini.ErrUsage) {
			t.Fatalf("%q: %v is not a usage error", src, err)
		}
	})
}
