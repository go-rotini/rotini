package codegen

import (
	"slices"
	"strings"
	"testing"
)

// TestFormatKeyOrderComplete walks every object the spec and conf schemas declare and fails on
// a key `rotini fmt` has no rank for, and on a reading-order entry no schema declares.
func TestFormatKeyOrderComplete(t *testing.T) {
	schemas, err := loadFmtSchemas()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[*fmtSchema]bool{}
	declared := map[string]map[string]bool{} // reading-order kind → keys its schemas declare
	var walk func(s *fmtSchema, path string)
	walk = func(s *fmtSchema, path string) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		rank := fmtRanks(s)
		for _, key := range s.props {
			if _, ok := rank[key]; !ok {
				t.Errorf("%s (%s): key %q has no place in fmt's key order", path, s.kind, key)
			}
		}
		if order, ok := fmtKeyOrders[s.kind]; ok {
			if declared[s.kind] == nil {
				declared[s.kind] = map[string]bool{}
			}
			for _, key := range s.props {
				declared[s.kind][key] = true
			}
			if dup := duplicate(order); dup != "" {
				t.Errorf("%s: key %q is listed twice", s.kind, dup)
			}
		}
		for _, key := range s.props {
			walk(s.prop[key], path+"."+key)
		}
		walk(s.items, path+"[]")
		walk(s.additional, path+".*")
	}
	walk(schemas.spec, "spec")
	walk(schemas.conf, "conf")
	walk(schemas.command, "command")

	used := map[string]bool{} // a key one of an order's kinds declares
	for kind, order := range fmtKeyOrders {
		if declared[kind] == nil {
			t.Errorf("no schema object is a %s; was the definition renamed?", kind)
			continue
		}
		for _, key := range order {
			if declared[kind][key] {
				used[strings.Join([]string{orderName(order), key}, ".")] = true
			}
		}
	}
	for _, order := range [][]string{commandKeyOrder, inputKeyOrder} {
		for _, key := range order {
			if key != "$schema" && !used[orderName(order)+"."+key] {
				t.Errorf("%s lists %q, which no schema declares", orderName(order), key)
			}
		}
	}
}

func orderName(order []string) string {
	if slices.Equal(order, commandKeyOrder) {
		return "commandKeyOrder"
	}
	return "inputKeyOrder"
}

func duplicate(keys []string) string {
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			return k
		}
		seen[k] = true
	}
	return ""
}

// TestFormatReadingOrder pins the reading order on a command, an input and a value schema.
func TestFormatReadingOrder(t *testing.T) {
	src := `command:
  commands:
    - name: run
  flags:
    - schema:
        default: 1
        enum: [1, 2]
        type: int
      role: force
      summary: s
      identifiers: [--n]
      name: n
  description: d
  summary: s
  name: app
  aliases: [a]
version: 1.0.0
`
	want := `version: 1.0.0
command:
  name: app
  aliases: [a]
  summary: s
  description: d
  flags:
    - name: n
      identifiers: [--n]
      summary: s
      role: force
      schema:
        type: int
        enum: [1, 2]
        default: 1
  commands:
    - name: run
`
	got, err := FormatYAML([]byte(src), FormatKindSpec, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("--- got\n%s\n--- want\n%s", got, want)
	}
}
