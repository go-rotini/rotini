package rotini

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// escapeTagItem escapes one item, key or value of a list or map tag, as codegen writes it.
func escapeTagItem(s string) string {
	return strings.NewReplacer(`\`, `\\`, `,`, `\,`, `=`, `\=`).Replace(s)
}

// writeTagList writes a list tag's value.
func writeTagList(items []string) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = escapeTagItem(it)
	}
	return strings.Join(out, ",")
}

// writeTagMap writes a map tag's value, keys sorted.
func writeTagMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = escapeTagItem(k) + "=" + escapeTagItem(m[k])
	}
	return strings.Join(out, ",")
}

// jsonDecodedStrings is what `_ = json.Unmarshal(s, &out)` leaves in a nil []string, and
// whether it reported no error.
func jsonDecodedStrings(s string) ([]string, bool) {
	var out []string
	err := json.Unmarshal([]byte(s), &out)
	return out, err == nil
}

// jsonDecodedMap is what `_ = json.Unmarshal(s, &out)` leaves in a nil map[string]string.
func jsonDecodedMap(s string) (map[string]string, bool) {
	var out map[string]string
	err := json.Unmarshal([]byte(s), &out)
	return out, err == nil
}

func TestSplitTagList(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", []string{""}, true},
		{"a", []string{"a"}, true},
		{"a,b", []string{"a", "b"}, true},
		{"a,", []string{"a", ""}, true},
		{`a\,b,c`, []string{"a,b", "c"}, true},
		{`a\\,b`, []string{`a\`, "b"}, true},
		{`a\=b=c`, []string{"a=b=c"}, true},
		{`a\b`, nil, false},
		{`a\`, nil, false},
		{"日本,語", []string{"日本", "語"}, true},
	} {
		got, ok := splitTagList(tt.in)
		if ok != tt.ok || !slices.Equal(got, tt.want) {
			t.Errorf("splitTagList(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSplitTagPairs(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want map[string]string
		ok   bool
	}{
		{"", map[string]string{}, true},
		{"a=b", map[string]string{"a": "b"}, true},
		{"a=b,c=d", map[string]string{"a": "b", "c": "d"}, true},
		{"a=", map[string]string{"a": ""}, true},
		{"a=b=c", map[string]string{"a": "b=c"}, true},
		{`a\=b=c`, map[string]string{"a=b": "c"}, true},
		{`a\,b=c\,d`, map[string]string{"a,b": "c,d"}, true},
		{"a=1,a=2", map[string]string{"a": "2"}, true},
		{"=b", nil, false},
		{"a", nil, false},
		{"a=b,", nil, false},
		{`a\=b`, nil, false},
		{`a=b\x`, nil, false},
	} {
		got, ok := splitTagPairs(tt.in)
		if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitTagPairs(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

var jsonTagSeeds = []string{
	`["a","b"]`, `[]`, `null`, ` [ "a" , "b" ] `, `["a",null]`, `["a",1,"b"]`, `["a",["x"],{"k":1},true]`,
	`"a"`, `1`, `{"a":"b"}`, `{"a":null,"b":2,"c":"d"}`, `{}`, `{"a":"1","a":"2"}`, `["a",]`, `[,]`, `["a"`,
	`["é😀\ud800x\udc00\\\"\/\b\f\n\r\t"]`, `["\'"]`, "[\"\x01\"]", "[\"\xff\xfe\"]",
	`["\ud800A"]`, `["\uZZZZ"]`, `[01]`, `[1.]`, `[-]`, `[1e5,-0.5E-3]`, `[nul]`, `[true false]`,
	"\ufeff[]", `[] x`, `{"a" "b"}`, `{"a":}`, `{1:"a"}`, `{"a":"b",}`, `tru`, `[[[]]]`,
	strings.Repeat("[", 10000) + strings.Repeat("]", 10000), strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
	`["a"` + strings.Repeat(`,[`, 9999) + strings.Repeat(`]`, 9999) + `]`,
	`["a"` + strings.Repeat(`,[`, 10000) + strings.Repeat(`]`, 10000) + `]`,
}

func TestJSONStrings_matchesEncodingJSON(t *testing.T) {
	for _, s := range jsonTagSeeds {
		checkJSONTagParity(t, s)
	}
}

func checkJSONTagParity(t *testing.T, s string) {
	t.Helper()
	got, ok := jsonStrings(s)
	want, wantOK := jsonDecodedStrings(s)
	if ok != wantOK || !reflect.DeepEqual(got, want) {
		t.Errorf("jsonStrings(%q) = %#v, %v; want %#v, %v", s, got, ok, want, wantOK)
	}
	gotMap, ok := jsonStringMap(s)
	wantMap, wantOK := jsonDecodedMap(s)
	if ok != wantOK || !reflect.DeepEqual(gotMap, wantMap) {
		t.Errorf("jsonStringMap(%q) = %#v, %v; want %#v, %v", s, gotMap, ok, wantMap, wantOK)
	}
}

// FuzzTagList checks that a list or map tag reads back what codegen wrote, and that the legacy
// JSON tags read as encoding/json decodes them.
func FuzzTagList(f *testing.F) {
	for _, s := range jsonTagSeeds {
		f.Add(s)
	}
	f.Add("a,b\x00c=d,=,\\")
	f.Fuzz(func(t *testing.T, s string) {
		checkJSONTagParity(t, s)

		items := strings.Split(s, "\x00")
		if got, ok := splitTagList(writeTagList(items)); !ok || !slices.Equal(got, items) {
			t.Fatalf("list %q: read back %q, %v", items, got, ok)
		}
		m := map[string]string{}
		for i := 0; i+1 < len(items); i += 2 {
			if items[i] != "" {
				m[items[i]] = items[i+1]
			}
		}
		got, ok := splitTagPairs(writeTagMap(m))
		if !ok || !reflect.DeepEqual(got, m) {
			t.Fatalf("map %q: read back %q, %v", m, got, ok)
		}
		// An unescaped tag value never panics, and parses only with known escapes.
		splitTagList(s)
		splitTagPairs(s)
	})
}

// legacyTagTimeSpec and legacyChannelEnum are the JSON readers the list tags replaced.
func legacyTagTimeSpec(tag reflect.StructTag) timeSpec {
	ts := timeSpec{layout: tag.Get("layout"), relative: tag.Get("relative")}
	if v := tag.Get("layouts"); v != "" {
		if err := json.Unmarshal([]byte(v), &ts.layouts); err != nil {
			ts.layouts = nil
		}
	}
	return ts
}

func legacyChannelEnum(tag reflect.StructTag) enumSet {
	var e enumSet
	if v := tag.Get("enum"); v != "" {
		_ = json.Unmarshal([]byte(v), &e.values)
	}
	e.ignoreCase = tag.Get("ignorecase") == "true"
	var aliases map[string]string
	if v := tag.Get("enumalias"); v != "" {
		_ = json.Unmarshal([]byte(v), &aliases)
	}
	var unlisted []string
	if v := tag.Get("enumunlisted"); v != "" {
		_ = json.Unmarshal([]byte(v), &unlisted)
	}
	if len(aliases) == 0 && len(unlisted) == 0 {
		return e
	}
	for _, m := range e.values {
		d := EnumValue{Value: m, Hidden: slices.Contains(unlisted, m)}
		for a, to := range aliases {
			if to == m {
				d.Aliases = append(d.Aliases, a)
			}
		}
		sort.Strings(d.Aliases)
		e.described = append(e.described, d)
	}
	return e
}

// TestChannelEnum_listAndJSONTags pins that a field's list tags and the JSON tags written
// before them read as the same enum, and that JSON tags read as they always did.
func TestChannelEnum_listAndJSONTags(t *testing.T) {
	values := []string{"red", "green,blue", `a\b`, "x=y", ""}
	aliases := map[string]string{"r": "red", "g,b": "green,blue", "k=v": "x=y"}
	hidden := []string{"x=y"}
	layouts := []string{"2006-01-02", "Jan 2, 2006", `a\b=c`}

	jsonOf := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return strconv.Quote(string(b))
	}
	legacy := reflect.StructTag(`enum:` + jsonOf(values) + ` enumalias:` + jsonOf(aliases) +
		` enumunlisted:` + jsonOf(hidden) + ` ignorecase:"true" layout:"2006-01-02" layouts:` + jsonOf(layouts))
	list := reflect.StructTag(`enumlist:` + strconv.Quote(writeTagList(values)) + ` enumaliases:` +
		strconv.Quote(writeTagMap(aliases)) + ` enumhide:` + strconv.Quote(writeTagList(hidden)) +
		` ignorecase:"true" layout:"2006-01-02" layoutlist:` + strconv.Quote(writeTagList(layouts)))

	want := legacyChannelEnum(legacy)
	if got := channelEnum(legacy); !reflect.DeepEqual(got, want) {
		t.Errorf("JSON tags: channelEnum = %+v, want %+v", got, want)
	}
	if got := channelEnum(list); !reflect.DeepEqual(got, want) {
		t.Errorf("list tags: channelEnum = %+v, want %+v", got, want)
	}
	wantTS := legacyTagTimeSpec(legacy)
	if got := tagTimeSpec(legacy); !reflect.DeepEqual(got, wantTS) {
		t.Errorf("JSON tags: tagTimeSpec = %+v, want %+v", got, wantTS)
	}
	if got := tagTimeSpec(list); !reflect.DeepEqual(got, wantTS) {
		t.Errorf("list tags: tagTimeSpec = %+v, want %+v", got, wantTS)
	}

	// A list tag wins over a JSON one; a malformed list tag reads as absent.
	both := reflect.StructTag(`enumlist:"a,b" enum:"[\"c\"]"`)
	if got := channelEnum(both).values; !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("enumlist beside enum: values %q, want [a b]", got)
	}
	bad := reflect.StructTag(`enumlist:"a\\x" enum:"[\"c\"]" layoutlist:"a\\x" layout:"x"`)
	if got := channelEnum(bad); got.declared() {
		t.Errorf("malformed enumlist: %+v, want no enum", got)
	}
	if got := tagTimeSpec(bad); got.layouts != nil || got.layout != "x" {
		t.Errorf("malformed layoutlist: %+v, want the first layout alone", got)
	}

	// Malformed JSON tags keep what JSON decoding kept.
	for _, tag := range []reflect.StructTag{
		`enum:"[\"a\",1,\"b\"]" enumalias:"{\"x\":\"a\",\"y\":2}"`,
		`enum:"[\"a\"" enumunlisted:"[\"a\",null]"`,
		`enum:"null" layouts:"[\"a\",1]"`,
	} {
		if got, want := channelEnum(tag), legacyChannelEnum(tag); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: channelEnum = %+v, want %+v", tag, got, want)
		}
		if got, want := tagTimeSpec(tag), legacyTagTimeSpec(tag); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tagTimeSpec = %+v, want %+v", tag, got, want)
		}
	}
}

// legacyInferScalar is inferScalar as written with encoding/json: an integer literal that
// fits is an int64, any other number a float64.
func legacyInferScalar(text string) any {
	switch text {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if text == "" || !strings.ContainsAny(text[:1], "-0123456789") || strings.TrimSpace(text) != text {
		return text
	}
	var n float64
	if err := json.Unmarshal([]byte(text), &n); err != nil {
		return text
	}
	var i int64
	if !strings.ContainsAny(text, ".eE") && json.Unmarshal([]byte(text), &i) == nil {
		return i
	}
	return n
}

var scalarSeeds = []string{
	"true", "false", "null", "", "5", "-0", "0", "007", "1_000", "0x10", "+1", ".5", "1.", "1e400", "-1e400",
	"1e-400", "1.5e3", "1E+3", "-", "--1", "1 ", " 1", "1 ", "1\t", "12abc", "1.2.3", "9007199254740993",
	"yes", "-.5", "0.0e0", "1e", "1e+", "١",
}

func TestInferScalar_matchesJSON(t *testing.T) {
	for _, s := range scalarSeeds {
		if got, want := inferScalar(s), legacyInferScalar(s); !reflect.DeepEqual(got, want) {
			t.Errorf("inferScalar(%q) = %#v, want %#v", s, got, want)
		}
	}
}

func FuzzInferScalar(f *testing.F) {
	for _, s := range scalarSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := inferScalar(s), legacyInferScalar(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("inferScalar(%q) = %#v, want %#v", s, got, want)
		}
	})
}
