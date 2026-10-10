package cfgedit

import (
	"errors"
	"strings"
	"testing"
)

func TestIndexYAMLSpans(t *testing.T) {
	src := "# head\n" +
		"a: plain # c\n" +
		"b: 'single ''q'''\n" +
		"c: \"dq \\\" x\"\n" +
		"d: |\n  one\n\n  two\n" +
		"e: {x: it's, y: [1, 2]}\n" +
		"f:\n" +
		"g: &anc\n  - - n1\n    - n2\n  - z\n" +
		"h: *anc\n" +
		"i: multi\n  line\n"
	doc, err := IndexYAML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	text := func(n *YAMLNode) string { return src[n.Start:n.End] }
	want := map[string]string{
		"a": "plain",
		"b": "'single ''q'''",
		"c": `"dq \" x"`,
		"d": "|\n  one\n\n  two",
		"e": "{x: it's, y: [1, 2]}",
		"f": "",
		"g": "&anc\n  - - n1\n    - n2\n  - z",
		"h": "*anc",
		"i": "multi\n  line",
	}
	for _, p := range doc.Root.Pairs {
		if got := text(p.Value); got != want[p.Key.Value] {
			t.Errorf("%s: span %q, want %q", p.Key.Value, got, want[p.Key.Value])
		}
		if src[p.Key.Start:p.Key.End] != p.Key.Value || src[p.Colon] != ':' {
			t.Errorf("%s: key span %q, colon %q", p.Key.Value, src[p.Key.Start:p.Key.End], src[p.Colon])
		}
	}
	g := doc.Root.Pairs[6].Value
	if g.Anchor != "anc" || g.Kind != YAMLSequence || g.Col != 2 || len(g.Items) != 2 {
		t.Fatalf("g: %+v", g)
	}
	inner := g.Items[0].Value
	if inner.Kind != YAMLSequence || inner.Col != 4 || len(inner.Items) != 2 || text(inner.Items[1].Value) != "n2" {
		t.Errorf("compact inner sequence: %+v", inner)
	}
	if f := doc.Root.Pairs[5].Value; !f.Empty || f.Start != doc.Root.Pairs[5].Colon+1 {
		t.Errorf("empty value: %+v", f)
	}
	if doc.Column(doc.Root.Pairs[1].Key.Start) != 0 || doc.LineEnd(0) != len("# head") || doc.NextLine(0) != len("# head\n") {
		t.Error("line helpers")
	}
}

func TestIndexYAMLRefusals(t *testing.T) {
	for _, src := range []string{
		"? a\n: 1\n",
		"a: 1\r b: 2\n",
		"a: \x01\n",
		"& a: 1\n",
		"!tag\na: 1\n",
		"a: | x\n  y\n",
		"a: [\n",
		"a: \"open\n",
	} {
		_, err := IndexYAML([]byte(src))
		var le *LayoutError
		if !errors.As(err, &le) && !strings.Contains(fmtErr(err), "parse yaml") {
			t.Errorf("%q: %v", src, err)
		}
	}
	if _, err := IndexYAML([]byte("a: 1\n---\nb: 2\n")); !errors.Is(err, ErrMultipleDocuments) {
		t.Errorf("multi-document: %v", err)
	}
	if doc, err := IndexYAML([]byte("# nothing\n")); err != nil || doc.Root != nil {
		t.Errorf("comments only: %v", err)
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
