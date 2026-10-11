package codegen

import (
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// readTagList splits a list tag value the way the input reader does: "\" escapes "\", "," or
// "="; any other escape, or a trailing "\", makes the whole value unreadable.
func readTagList(s string) ([]string, bool) {
	var out []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 >= len(s) || (s[i+1] != '\\' && s[i+1] != ',' && s[i+1] != '=') {
				return nil, false
			}
			i++
			b.WriteByte(s[i])
		case ',':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(out, b.String()), true
}

// readTagPairs splits a map tag value: pairs on unescaped commas, each at its first unescaped
// "="; a pair without one, or with an empty key, makes the whole value unreadable.
func readTagPairs(s string) (map[string]string, bool) {
	out := map[string]string{}
	if s == "" {
		return out, true
	}
	var key, cur strings.Builder
	inValue := false
	flush := func() bool {
		if !inValue || key.Len() == 0 {
			return false
		}
		out[key.String()] = cur.String()
		key.Reset()
		cur.Reset()
		inValue = false
		return true
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 >= len(s) || (s[i+1] != '\\' && s[i+1] != ',' && s[i+1] != '=') {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
		case '=':
			if inValue {
				cur.WriteByte(c)
				continue
			}
			key.WriteString(cur.String())
			cur.Reset()
			inValue = true
		case ',':
			if !flush() {
				return nil, false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if !flush() {
		return nil, false
	}
	return out, true
}

// Enum values, aliases and layouts survive the list encoding whatever they contain, once the
// tag is quoted and read back through reflect.
func TestTagLists_roundTrip(t *testing.T) {
	lists := [][]string{
		{""},
		{"a"},
		{"a,b", `c\d`, "e=f", `\`, `,`, "=", `"q"`, "`t`", "ü"},
		{"", "", "x"},
		{`a\,b`, `\\`, "a b"},
	}
	for _, items := range lists {
		tag := reflect.StructTag("enumlist:" + strconv.Quote(tagList(items)))
		v, ok := tag.Lookup("enumlist")
		if !ok {
			t.Fatalf("%q: tag does not read back", items)
		}
		got, ok := readTagList(v)
		if !ok || !slices.Equal(got, items) {
			t.Errorf("list %q encoded as %q read back as %q (ok %v)", items, v, got, ok)
		}
	}
	maps1 := []map[string]string{
		{"s": "slow"},
		{"a=b": "c,d", `e\`: "", "x": "=", "k": `\=`},
		{"b": "2", "a": "1"},
	}
	for _, m := range maps1 {
		tag := reflect.StructTag("enumaliases:" + strconv.Quote(tagPairs(m)))
		v, _ := tag.Lookup("enumaliases")
		got, ok := readTagPairs(v)
		if !ok || !maps.Equal(got, m) {
			t.Errorf("map %q encoded as %q read back as %q (ok %v)", m, v, got, ok)
		}
	}
	if got := tagPairs(map[string]string{"b": "2", "a": "1"}); got != "a=1,b=2" {
		t.Errorf("pairs are written in key order: %q", got)
	}
}

func FuzzTagListWriter(f *testing.F) {
	f.Add("a", "b,c", `d\e=f`)
	f.Fuzz(func(t *testing.T, a, b, c string) {
		items := []string{a, b, c}
		got, ok := readTagList(tagList(items))
		if !ok || !slices.Equal(got, items) {
			t.Fatalf("%q read back as %q (ok %v)", items, got, ok)
		}
		m := map[string]string{a: b}
		if a != "" {
			got, ok := readTagPairs(tagPairs(m))
			if !ok || !maps.Equal(got, m) {
				t.Fatalf("%q read back as %q (ok %v)", m, got, ok)
			}
		}
	})
}
