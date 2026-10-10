package codegen

import (
	"reflect"
	"testing"
)

// TestExampleSegments pins how an example line is split into the words of each command.
func TestExampleSegments(t *testing.T) {
	w := func(texts ...string) []exampleWord {
		out := make([]exampleWord, len(texts))
		for i, s := range texts {
			out[i] = exampleWord{text: s}
		}
		return out
	}
	opaque := func(s string) exampleWord { return exampleWord{text: s, opaque: true} }
	for _, tc := range []struct {
		line     string
		want     [][]exampleWord
		unclosed bool
	}{
		{line: "taskr add 'buy milk' --due=today", want: [][]exampleWord{w("taskr", "add", "buy milk", "--due=today")}},
		{line: `$ taskr add "say \"hi\"" a\ b`, want: [][]exampleWord{w("taskr", "add", `say "hi"`, "a b")}},
		{line: "A=1 B=2 taskr list | grep x && taskr done 3; taskr ls", want: [][]exampleWord{w("taskr", "list"), w("grep", "x"), w("taskr", "done", "3"), w("taskr", "ls")}},
		{line: "taskr list > out.txt 2>&1", want: [][]exampleWord{w("taskr", "list")}},
		{line: "taskr list 2>/dev/null", want: [][]exampleWord{w("taskr", "list")}},
		{line: "taskr list &> log", want: [][]exampleWord{w("taskr", "list")}},
		{line: "taskr get <id> --status=<status>", want: [][]exampleWord{{{text: "taskr"}, {text: "get"}, opaque("<id>"), opaque("--status=<status>")}}},
		{line: `taskr add "$TITLE" *.txt`, want: [][]exampleWord{{{text: "taskr"}, {text: "add"}, opaque("$TITLE"), opaque("*.txt")}}},
		{line: "taskr add \\\n  title", want: [][]exampleWord{w("taskr", "add", "title")}},
		{line: `taskr add "open`, unclosed: true},
		{line: "taskr add 'open", unclosed: true},
	} {
		got, unclosed := exampleSegments(tc.line)
		if unclosed != tc.unclosed || (!tc.unclosed && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("exampleSegments(%q) = %v, %v; want %v, %v", tc.line, got, unclosed, tc.want, tc.unclosed)
		}
	}
}
