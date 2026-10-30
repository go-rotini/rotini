package rotini

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// A stream whose items come in several kinds, as `rotini generate` writes it: a sealed
// interface, a struct per kind, and an OutputDef naming the tag and each kind.
type (
	evOutput   interface{ isEvOutput() }
	evProgress struct {
		Kind    string `json:"kind"`
		Percent int    `json:"percent"`
	}
	evResult struct {
		Kind string `json:"kind"`
		OK   bool   `json:"ok"`
	}
	evStray struct {
		Kind string `json:"kind"`
	}
)

func (evProgress) isEvOutput() {}
func (evResult) isEvOutput()   {}
func (evStray) isEvOutput()    {}

const (
	evProgressSchema = `{"type":"object","required":["kind","percent"],"properties":{"kind":{"type":"string","const":"progress"},"percent":{"type":"integer","minimum":0,"maximum":100}}}`
	evResultSchema   = `{"type":"object","required":["kind","ok"],"properties":{"kind":{"type":"string","const":"result"},"ok":{"type":"boolean"}}}`
)

func evDef() Definition {
	return Definition{
		Name: "runr",
		Commands: []CommandDef{{Name: "watch", Output: &OutputDef{
			Type:   reflect.TypeFor[evOutput](),
			Schema: `{"oneOf":[` + evProgressSchema + `,` + evResultSchema + `]}`,
			Stream: true,
			Tag:    "kind",
			Kinds: []OutputKind{
				{Value: "progress", Type: reflect.TypeFor[evProgress](), Schema: evProgressSchema},
				{Value: "result", Type: reflect.TypeFor[evResult](), Schema: evResultSchema},
			},
		}}},
	}
}

func evContext() (*Context, *bytes.Buffer) {
	var out bytes.Buffer
	return NewContextFor(evDef(), []string{"watch"}).WithStdout(&out), &out
}

// TestWriteOutputItem_kinds pins that an item of any kind is written with its tag filled in
// from its type, by value, by pointer and through the interface.
func TestWriteOutputItem_kinds(t *testing.T) {
	t.Parallel()
	rtx, out := evContext()
	rtx.WithOutputChecks(true)
	var asInterface evOutput = evResult{OK: true}
	for _, write := range []func() error{
		func() error { return rtx.WriteOutputItem(evProgress{Percent: 50}, "", nil) },
		func() error { return rtx.WriteOutputItem(&evProgress{Kind: "progress", Percent: 90}, "", nil) },
		func() error { return rtx.WriteOutputItem(asInterface, "", nil) },
	} {
		if err := write(); err != nil {
			t.Fatal(err)
		}
	}
	want := `{"kind":"progress","percent":50}` + "\n" + `{"kind":"progress","percent":90}` + "\n" + `{"kind":"result","ok":true}` + "\n"
	if out.String() != want {
		t.Errorf("stream = %q, want %q", out, want)
	}
	// The item passed in is left as it was.
	p := &evProgress{Percent: 10}
	if err := rtx.WriteOutputItem(p, "yaml", nil); err != nil || p.Kind != "" {
		t.Errorf("writing changed the caller's item: %+v %v", p, err)
	}
	if !strings.HasSuffix(out.String(), "---\nkind: progress\npercent: 10\n") {
		t.Errorf("yaml item = %q", out)
	}
}

// TestWriteOutputItem_kindsRefused pins the program bugs: a tag naming another kind, a type
// that is no kind, a nil item, a whole value, and an item off its kind's shape.
func TestWriteOutputItem_kindsRefused(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		write func(*Context) error
		want  string
	}{
		{"a tag naming another kind", func(rtx *Context) error {
			return rtx.WriteOutputItem(evProgress{Kind: "result"}, "", nil)
		}, `runr watch: the item is a rotini.evProgress, the "progress" kind, but its kind is "result"`},
		{"a type that is no kind", func(rtx *Context) error {
			return rtx.WriteOutputItem(evStray{}, "", nil)
		}, `runr watch: the item written is a rotini.evStray, which is not one of the kinds the command declares ("progress", "result")`},
		{"a nil item", func(rtx *Context) error {
			var item evOutput
			return rtx.WriteOutputItem(item, "", nil)
		}, `runr watch: the item written is nil; write one of its kinds ("progress", "result")`},
		{"a whole value", func(rtx *Context) error {
			return rtx.WriteOutput(evResult{}, "", nil)
		}, "runr watch: declares a stream (output_stream); write each item with WriteOutputItem"},
		{"off its kind's shape", func(rtx *Context) error {
			rtx.WithOutputChecks(true)
			return rtx.WriteOutputItem(evProgress{Percent: 101}, "", nil)
		}, "runr watch: output does not match its contract: output.percent: "},
	} {
		rtx, out := evContext()
		err := tt.write(rtx)
		if err == nil || !strings.HasPrefix(err.Error(), tt.want) || CategoryOf(err) != CategoryInternal {
			t.Errorf("%s: err = %v, want internal error %q", tt.name, err, tt.want)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q", tt.name, out)
		}
	}
}

// TestWriteOutputItem_kindsSelected pins that an item selected from one of the kinds is
// checked against that kind, with a selected but empty tag filled in as WriteOutputItem fills
// a whole item's, and a selected tag naming another kind refused, checks on or off.
func TestWriteOutputItem_kindsSelected(t *testing.T) {
	t.Parallel()
	selected := func(v any, fields ...string) Selection {
		s, err := SelectFields(v, "", fields)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, checks := range []bool{false, true} {
		rtx, out := evContext()
		rtx.WithOutputChecks(checks)
		for _, s := range []Selection{
			selected(evProgress{Percent: 5}, "kind", "percent"),
			selected(&evProgress{Kind: "progress", Percent: 6}, "kind"),
			selected(evResult{OK: true}, "ok"),
		} {
			if err := rtx.WriteOutputItem(s, "", nil); err != nil {
				t.Fatalf("checks %v: %v", checks, err)
			}
		}
		want := `{"kind":"progress","percent":5}` + "\n" + `{"kind":"progress"}` + "\n" + `{"ok":true}` + "\n"
		if out.String() != want {
			t.Errorf("checks %v: stream = %q, want %q", checks, out, want)
		}

		rtx, out = evContext()
		rtx.WithOutputChecks(checks)
		err := rtx.WriteOutputItem(selected(evProgress{Kind: "result"}, "kind"), "", nil)
		if want := `runr watch: the item is a rotini.evProgress, the "progress" kind, but its kind is "result"`; err == nil || err.Error() != want || CategoryOf(err) != CategoryInternal {
			t.Errorf("checks %v: a selected tag naming another kind: %v", checks, err)
		}
		if out.Len() != 0 {
			t.Errorf("checks %v: wrote %q", checks, out)
		}
		if err := rtx.WriteOutputItem(selected(evStray{}, "kind"), "", nil); err == nil || !strings.Contains(err.Error(), "which is not one of the kinds") {
			t.Errorf("checks %v: a selection of no kind: %v", checks, err)
		}
	}

	rtx, out := evContext()
	rtx.WithOutputChecks(true)
	if err := rtx.WriteOutputItem(selected(evProgress{Percent: 101}, "percent"), "", nil); err == nil || !strings.Contains(err.Error(), "output.percent") || out.Len() != 0 {
		t.Errorf("a selection off its kind's shape: %v, wrote %q", err, out)
	}
	if err := rtx.CheckOutput(selected(evProgress{Percent: 7}, "kind", "percent")); err != nil {
		t.Errorf("CheckOutput(a selection with an empty tag) = %v", err)
	}
	if err := rtx.CheckOutput(selected(evResult{Kind: "progress"}, "kind")); err == nil || !strings.Contains(err.Error(), `the "result" kind, but its kind is "progress"`) {
		t.Errorf("CheckOutput(a selection with a mismatched tag) = %v", err)
	}
	if err := rtx.CheckOutput(selected(evStray{}, "kind")); err == nil || !strings.Contains(err.Error(), "which is not one of the kinds") {
		t.Errorf("CheckOutput(a selection of no kind) = %v", err)
	}
}

// TestCheckOutput_kinds pins that CheckOutput checks an item against its own kind, its tag
// filled in as WriteOutputItem fills it.
func TestCheckOutput_kinds(t *testing.T) {
	t.Parallel()
	rtx, _ := evContext()
	if err := rtx.CheckOutput(evResult{OK: true}); err != nil {
		t.Errorf("CheckOutput(result) = %v", err)
	}
	if err := rtx.CheckOutput(evProgress{Percent: -1}); err == nil || !strings.Contains(err.Error(), "output.percent") {
		t.Errorf("CheckOutput(progress off its shape) = %v", err)
	}
	if err := rtx.CheckOutput(evResult{Kind: "progress"}); err == nil || !strings.Contains(err.Error(), `the "result" kind, but its kind is "progress"`) {
		t.Errorf("CheckOutput(a mismatched tag) = %v", err)
	}
}

// TestDecodeOutput_kinds pins that each item decodes into the type its tag names, and the
// errors for an item of an unknown kind, an untagged item, and an item off its kind's shape.
func TestDecodeOutput_kinds(t *testing.T) {
	t.Parallel()
	p := NewProgram(evDef(), nil)
	stream := `{"kind":"progress","percent":40}` + "\n" + `{"kind":"result","ok":true}` + "\n"
	items, err := DecodeOutput[[]evOutput](p, []byte(stream), "json")
	if err != nil {
		t.Fatal(err)
	}
	want := []evOutput{evProgress{Kind: "progress", Percent: 40}, evResult{Kind: "result", OK: true}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %#v, want %#v", items, want)
	}
	items, err = DecodeOutput[[]evOutput](p, []byte("---\nkind: result\nok: false\n---\nkind: progress\npercent: 1\n"), "yaml")
	if err != nil || len(items) != 2 || items[1] != (evProgress{Kind: "progress", Percent: 1}) {
		t.Errorf("yaml: %#v %v", items, err)
	}
	for _, tt := range []struct {
		name, data, want string
	}{
		{"an unknown kind", `{"kind":"done"}`, `runr watch: item 0 is of an unknown kind: kind "done" is none of "progress", "result"`},
		{"no tag", `{"ok":true}`, `runr watch: item 0 has no "kind" saying which kind it is`},
		{"off its kind's shape", `{"kind":"result","ok":true}` + "\n" + `{"kind":"progress","percent":"half"}`, "item 1: runr watch: output does not match its contract: output.percent: "},
	} {
		if _, err := DecodeOutput[[]evOutput](p, []byte(tt.data), "json"); err == nil || !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want prefix %q", tt.name, err, tt.want)
		}
	}
	if _, err := DecodeOutput[[]evProgress](p, []byte(stream), "json"); err == nil ||
		err.Error() != "runr watch writes a stream of rotini.evOutput, of which rotini.evProgress is one kind; decode it into []rotini.evOutput" {
		t.Errorf("decoding into one kind: %v", err)
	}
	if _, err := DecodeOutput[evOutput](p, []byte(stream), "json"); err == nil || !strings.Contains(err.Error(), "decode it into []rotini.evOutput") {
		t.Errorf("decoding a stream into one item: %v", err)
	}
}
