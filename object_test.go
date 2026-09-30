package rotini

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Shapes as codegen emits them for
//
//	schemas:
//	  DB:    {type: object, required: [host], properties: {host: string, port: integer ≥ 1, tls: boolean, pool: Pool, tags: [string]}}
//	  Pool:  {type: object, properties: {max: integer}}
//	  Mount: {type: object, required: [src], properties: {src: string, dst: string, readonly: boolean}}
type objPool struct {
	Max int `json:"max,omitempty"`
}

type objDB struct {
	Host string   `json:"host"`
	Port int      `json:"port,omitempty"`
	TLS  bool     `json:"tls,omitempty"`
	Pool objPool  `json:"pool,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

type objMount struct {
	Src      string `json:"src"`
	Dst      string `json:"dst,omitempty"`
	Readonly bool   `json:"readonly,omitempty"`
}

const (
	objDBSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","$ref":"#/definitions/DB","definitions":{` +
		`"DB":{"type":"object","required":["host"],"properties":{"host":{"type":"string"},"port":{"type":"integer","minimum":1},` +
		`"tls":{"type":"boolean"},"pool":{"$ref":"#/definitions/Pool"},"tags":{"type":"array","items":{"type":"string"}}}},` +
		`"Pool":{"type":"object","properties":{"max":{"type":"integer"}}}}}`
	objMountSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","$ref":"#/definitions/Mount","definitions":{` +
		`"Mount":{"type":"object","required":["src"],"properties":{"src":{"type":"string"},"dst":{"type":"string"},"readonly":{"type":"boolean"}}}}}`
)

type objInputs struct {
	App struct {
		Flags struct {
			DB    objDB      `rotini:"db" recon:"db" env:"DB"`
			Mount []objMount `rotini:"mount" recon:"mount"`
		}
		Arguments struct{}
	}
}

func objDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "db", Identifiers: []string{"--db"}, Type: "DB", ObjectSchema: objDBSchema, From: []string{"file"}},
		{Name: "mount", Identifiers: []string{"--mount"}, Type: "[]Mount", ObjectSchema: objMountSchema},
	}}
}

func parseObj(t *testing.T, argv ...string) (objInputs, error) {
	t.Helper()
	var in objInputs
	err := NewParser().Parse(NewContextFor(objDef(), argv), &in)
	return in, err
}

// Every spelling of the same object binds the same struct.
func TestObjectFlag_spellings(t *testing.T) {
	want := objDB{Host: "h", Port: 5, TLS: true, Pool: objPool{Max: 9}, Tags: []string{"a", "b"}}
	file := filepath.Join(t.TempDir(), "db.yaml")
	if err := os.WriteFile(file, []byte("host: h\nport: 5\ntls: true\npool:\n  max: 9\ntags: [a, b]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"--db", `{"host":"h","port":5,"tls":true,"pool":{"max":9},"tags":["a","b"]}`},
		{"--db", "host=h,port=5,tls=yes,pool.max=9,tags=a,tags=b"},
		{"--db", "host=h", "--db", "port=5,tls=true", "--db", "pool.max=9,tags=a,tags=b"}, // occurrences merge
		{"--db.host=h", "--db.port", "5", "--db.tls=on", "--db.pool.max=9", "--db", "tags=a,tags=b"},
		{"--db", "@" + file},
	} {
		in, err := parseObj(t, argv...)
		if err != nil {
			t.Errorf("%q: %v", argv, err)
			continue
		}
		if !reflect.DeepEqual(in.App.Flags.DB, want) {
			t.Errorf("%q → %+v, want %+v", argv, in.App.Flags.DB, want)
		}
	}
}

// A later occurrence of a key wins, and nested objects merge rather than replace.
func TestObjectFlag_laterKeyWins(t *testing.T) {
	in, err := parseObj(t, "--db", `{"host":"a","pool":{"max":1}}`, "--db", "host=b", "--db.port=2")
	if err != nil {
		t.Fatal(err)
	}
	if got := in.App.Flags.DB; got.Host != "b" || got.Port != 2 || got.Pool.Max != 1 {
		t.Errorf("DB = %+v", got)
	}
}

// Quotes keep a comma inside a value, around the value or around the whole pair.
func TestObjectFlag_quoting(t *testing.T) {
	for _, arg := range []string{`host="a,b"`, `"host=a,b"`} {
		in, err := parseObj(t, "--db", arg)
		if err != nil || in.App.Flags.DB.Host != "a,b" {
			t.Errorf("%s → %q, %v", arg, in.App.Flags.DB.Host, err)
		}
	}
	in, err := parseObj(t, "--db.host=x,y")
	if err != nil || in.App.Flags.DB.Host != "x,y" {
		t.Errorf("--db.host=x,y → %q, %v", in.App.Flags.DB.Host, err)
	}
}

// A list of objects takes one element per occurrence, in any spelling.
func TestObjectFlag_list(t *testing.T) {
	in, err := parseObj(t, "--mount", "src=a,dst=b", "--mount", `{"src":"c","readonly":true}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []objMount{{Src: "a", Dst: "b"}, {Src: "c", Readonly: true}}
	if !reflect.DeepEqual(in.App.Flags.Mount, want) {
		t.Errorf("Mount = %+v", in.App.Flags.Mount)
	}
	// Per-field flags address one object; for a list they would be ambiguous.
	if _, err := parseObj(t, "--mount.src=a"); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("--mount.src: err = %v, want unknown flag", err)
	}
}

// Validation is the named schema's, and every error names the flag and the key.
func TestObjectFlag_errors(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"--db", "port=5"}, `--db: missing required property "host"`},
		{[]string{"--db", "host=h,port=0"}, "--db: port: value must be >= 1"},
		{[]string{"--db", "host=h,bogus=1"}, `--db: unknown key "bogus" (known: host, pool, port, tags, tls)`},
		{[]string{"--db", `{"host":"h","bogus":1}`}, `unknown key "bogus"`},
		{[]string{"--db", "host=h,port=five"}, `--db: port: "five" is not an integer`},
		{[]string{"--db", `{"host":5}`}, "--db: host: value is not of type string"},
		{[]string{"--db", "host"}, `"host" is not key=value`},
		{[]string{"--db", "pool=3"}, "pool is an object — set its fields as pool.<key>=…"},
		{[]string{"--db", `{"host":`}, "could not read the value as JSON"},
		{[]string{"--db.nope=1"}, `--db: unknown key "nope"`},
		{[]string{"--mount", "dst=x"}, `--mount: missing required property "src"`},
	} {
		_, err := parseObj(t, tc.argv...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want it to contain %q", tc.argv, err, tc.want)
		}
	}
}

// A flag's env and config fallbacks feed the same decoding: JSON in a variable, a nested block
// or a list of mappings in a configuration file.
func TestObjectFlag_fallbacks(t *testing.T) {
	bind := func(t *testing.T, meta BindMeta) objInputs {
		t.Helper()
		var in objInputs
		if err := NewBinder(meta).Bind(NewContextFor(objDef(), nil), &in); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		return in
	}
	t.Run("env JSON", func(t *testing.T) {
		t.Setenv("DB", `{"host":"env","port":3}`)
		if got := bind(t, BindMeta{}).App.Flags.DB; got.Host != "env" || got.Port != 3 {
			t.Errorf("DB = %+v", got)
		}
	})
	t.Run("env key=value", func(t *testing.T) {
		t.Setenv("DB", "host=env,port=4")
		if got := bind(t, BindMeta{}).App.Flags.DB; got.Host != "env" || got.Port != 4 {
			t.Errorf("DB = %+v", got)
		}
	})
	t.Run("config block and list", func(t *testing.T) {
		cfg := writeConfig(t, "db:\n  host: cfg, with comma\n  port: 7\n  pool:\n    max: 2\nmount:\n  - src: a\n  - src: b\n    readonly: true\n")
		in := bind(t, BindMeta{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}})
		if got := in.App.Flags.DB; got.Host != "cfg, with comma" || got.Port != 7 || got.Pool.Max != 2 {
			t.Errorf("DB = %+v", got)
		}
		if !reflect.DeepEqual(in.App.Flags.Mount, []objMount{{Src: "a"}, {Src: "b", Readonly: true}}) {
			t.Errorf("Mount = %+v", in.App.Flags.Mount)
		}
	})
	t.Run("a bad env value names the variable", func(t *testing.T) {
		t.Setenv("DB", "port=0")
		var in objInputs
		err := NewBinder(BindMeta{}).Bind(NewContextFor(objDef(), nil), &in)
		if err == nil || !strings.Contains(err.Error(), "environment variable DB") {
			t.Errorf("err = %v", err)
		}
	})
}

// A default arrives as the JSON document codegen encodes it into.
func TestObjectFlag_default(t *testing.T) {
	def := objDef()
	def.Flags[0].Default = `{"host":"dflt","port":1}`
	def.Flags[1].Defaults = []string{`{"src":"x"}`, `{"src":"y"}`}
	var in objInputs
	if err := NewParser().Parse(NewContextFor(def, nil), &in); err != nil {
		t.Fatal(err)
	}
	if in.App.Flags.DB.Host != "dflt" || len(in.App.Flags.Mount) != 2 {
		t.Errorf("DB = %+v, Mount = %+v", in.App.Flags.DB, in.App.Flags.Mount)
	}
}

func TestSplitPairs(t *testing.T) {
	for in, want := range map[string][]string{
		"a=1,b=2":        {"a=1", "b=2"},
		"a=1, b=2":       {"a=1", "b=2"},
		`a="1,2",b=3`:    {"a=1,2", "b=3"},
		`"a=1,2",b=3`:    {"a=1,2", "b=3"},
		`a="say ""hi"""`: {`a=say "hi"`},
		"a=":             {"a="},
	} {
		got, err := splitPairs(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("splitPairs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := splitPairs(`a="open`); err == nil {
		t.Error("an unclosed quote was accepted")
	}
	for _, v := range []string{"plain", "a,b", `say "hi"`} {
		got, err := splitPairs(quotePair("k", v))
		if err != nil || len(got) != 1 || got[0] != "k="+v {
			t.Errorf("quotePair round trip of %q: %q, %v", v, got, err)
		}
	}
}

// TestInferScalar pins what a value means where the schema says nothing about it: exactly what
// the JSON spelling of the same value means, and nothing looser. The same patch used to store
// the number 5 when written as JSON and the text "5" when written as key=value.
func TestInferScalar(t *testing.T) {
	for text, want := range map[string]any{
		"true": true, "false": false, "null": nil,
		"5": float64(5), "-2.5": -2.5, "1e3": float64(1000), "0": float64(0),
		// Not JSON, so text: a shell user's "yes", a zero-padded id, Go's digit separators, hex.
		"yes": "yes", "007": "007", "1_000": "1_000", "0x10": "0x10", "True": "True",
		"": "", "v2": "v2", "-": "-", "5 ": "5 ", "NaN": "NaN",
	} {
		if got := inferScalar(text); got != want {
			t.Errorf("inferScalar(%q) = %#v, want %#v", text, got, want)
		}
	}
}

// TestObjectFlag_freeFormValuesTypeLikeJSON proves one meaning per value across an object flag's
// spellings: inside a free-form map, key=value and JSON agree.
func TestObjectFlag_freeFormValuesTypeLikeJSON(t *testing.T) {
	type patch struct {
		Spec map[string]any `json:"spec"`
	}
	fromPairs, err := decodeObject("spec.replicas=5,spec.paused=true,spec.image=v2", reflect.TypeFor[patch]())
	if err != nil {
		t.Fatal(err)
	}
	fromJSON, err := decodeObject(`{"spec":{"replicas":5,"paused":true,"image":"v2"}}`, reflect.TypeFor[patch]())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromPairs, fromJSON) {
		t.Errorf("key=value gave %#v, JSON gave %#v — one value, two meanings", fromPairs, fromJSON)
	}
}

// TestObjectFlag_uint8FieldFromPairs: every integer width takes key=value. uint8 alone fell
// through to text — the signed branch listed int8, the unsigned one skipped uint8 — and the
// value then failed to decode into the field.
func TestObjectFlag_uint8FieldFromPairs(t *testing.T) {
	type pool struct {
		Level uint8 `json:"level"`
		Small int8  `json:"small"`
	}
	doc, err := decodeObject("level=7,small=-3", reflect.TypeFor[pool]())
	if err != nil {
		t.Fatal(err)
	}
	var got pool
	if err := validateAndBindObject(reflect.ValueOf(&got).Elem(), doc, `{"type":"object"}`); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Level != 7 || got.Small != -3 {
		t.Errorf("got %+v, want level 7, small -3", got)
	}
}
