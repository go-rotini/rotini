package rotini

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// decodeINI is a small INI decoder for the tests: `[section]` headers nest the keys after them,
// `key = value` lines are strings, `;` and `#` start comments, and a key given twice is an error.
func decodeINI(data []byte) (any, error) {
	root := map[string]any{}
	cur := root
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || line[0] == ';' || line[0] == '#':
		case line[0] == '[' && strings.HasSuffix(line, "]"):
			name := strings.TrimSpace(line[1 : len(line)-1])
			if _, dup := root[name]; dup {
				return nil, iniError{n, 1, fmt.Sprintf("duplicate section %q", name)}
			}
			cur = map[string]any{}
			root[name] = cur
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				return nil, iniError{n, 1, "expected key = value"}
			}
			k = strings.TrimSpace(k)
			if _, dup := cur[k]; dup {
				return nil, iniError{n, 1, fmt.Sprintf("duplicate key %q", k)}
			}
			cur[k] = strings.TrimSpace(v)
		}
	}
	return root, nil
}

// iniError is a decode failure with its position.
type iniError struct {
	line, col int
	msg       string
}

func (e iniError) Error() string             { return fmt.Sprintf("line %d: %s", e.line, e.msg) }
func (e iniError) Position() (line, col int) { return e.line, e.col }
func (e iniError) Message() string           { return e.msg }

// locateINI finds a section or a section's key.
func locateINI(data []byte, path []string) (line, col int, ok bool) {
	section := ""
	for n, raw := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = strings.TrimSpace(l[1 : len(l)-1])
			if len(path) == 1 && section == path[0] {
				return n + 1, 1, true
			}
			continue
		}
		if k, _, found := strings.Cut(l, "="); found && len(path) == 2 && section == path[0] && strings.TrimSpace(k) == path[1] {
			return n + 1, strings.Index(raw, "=") + 3, true
		}
	}
	return 0, 0, false
}

func iniCodec() Feature {
	return Codec("ini", decodeINI, CodecExtensions(".INI"), CodecLocate(locateINI))
}

type acInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Replicas int    `rotini:"replicas" recon:"deploy.replicas"`
			Region   string `rotini:"region" recon:"deploy.region"`
		}
	}
}

func acContext(dir string, files ...ConfigFile) *Context {
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files})
	return wired(rtx, ConfigFiles(), Schemas(), YAML(), iniCodec())
}

const acINI = "; app settings\n[deploy]\nreplicas = 3\nregion = eu\n"

func TestCodec_readsConfigFiles(t *testing.T) {
	for _, tt := range []struct{ name, file, format string }{
		{"declared format", "app.conf", "ini"},
		{"by extension", "app.ini", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, tt.file, acINI)
			files := []ConfigFile{{Name: "app", Path: path, Format: tt.format}}
			var in acInputs
			if err := NewInputReader(InputSettings{ConfigFiles: files}).Read(acContext(dir, files...), &in); err != nil {
				t.Fatal(err)
			}
			if in.App.Config.Replicas != 3 || in.App.Config.Region != "eu" {
				t.Errorf("config = %+v", in.App.Config)
			}
		})
	}
}

func TestCodec_errors(t *testing.T) {
	schema := `{"type":"object","properties":{"deploy":{"type":"object","properties":{"replicas":{"type":"integer"}}}}}`
	tests := []struct {
		name, body, schema string
		kind               InputErrorKind
		want               string
	}{
		{"decode error at its position", "[deploy]\nreplicas\n", "", InputErrorKindMalformedDocument, "app.ini:2:1: expected key = value"},
		{"duplicate key", "[deploy]\nregion = a\nregion = b\n", "", InputErrorKindMalformedDocument, `duplicate key "region"`},
		{"schema violation located", "[deploy]\nregion = eu\nreplicas = three\n", schema, InputErrorKindSchemaViolation,
			`app.ini:3:12 is invalid: deploy.replicas: value is not of type integer (got "three")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, "app.ini", tt.body)
			files := []ConfigFile{{Name: "app", Path: path, Schema: tt.schema}}
			var in struct {
				App struct {
					Flags     struct{}
					Arguments struct{}
					Config    struct {
						Region string `rotini:"region" recon:"deploy.region"`
					}
				}
			}
			err := NewInputReader(InputSettings{ConfigFiles: files}).Read(acContext(dir, files...), &in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant it to contain %s", err, tt.want)
			}
			wantKind(t, err, channelConfig, tt.kind)
		})
	}
}

func TestCodec_missing(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.conf", acINI)
	files := []ConfigFile{{Name: "app", Path: path, Format: "ini"}}
	rtx := wired(NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir), ConfigFiles(), YAML())
	var in acInputs
	err := NewInputReader(InputSettings{ConfigFiles: files}).Read(rtx, &in)
	var we *WiringError
	if !errors.As(err, &we) || we.Feature != "codec:ini" {
		t.Fatalf("err = %v (%T), want a *WiringError for codec:ini", err, err)
	}
	want := "configuration file " + path + ` is ini, but the program does not wire codec:ini; ini is not a format rotini ships, so add rotini.Codec("ini", …) to the program's features (Program.WithFeatures)`
	if we.Error() != want {
		t.Errorf("err = %q\nwant %q", we.Error(), want)
	}
}

func TestCodec_topLevelMustBeAMapping(t *testing.T) {
	c := Codec("list", func([]byte) (any, error) { return []any{"a"}, nil })
	s := newFeatureSet([]Feature{c})
	if _, err := s.codec("list").codec().Decode([]byte("a")); err == nil || !strings.Contains(err.Error(), "list root must be a mapping, got []interface {}") {
		t.Errorf("Decode = %v", err)
	}
	null := func([]byte) (any, error) { return nil, nil } //nolint:nilnil // a null document
	if m, err := newFeatureSet([]Feature{Codec("empty", null)}).codec("empty").codec().Decode(nil); err != nil || len(m) != 0 {
		t.Errorf("a null document = %v, %v; want the empty mapping", m, err)
	}
}

func TestCodec_writeRefused(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.ini", acINI)
	f := ConfigFile{Name: "app", Path: path, Keys: []ConfigKey{{Key: "deploy.region", Type: "string"}}}
	rtx := wired(cwContext(dir, nil, f), ConfigFiles(), iniCodec())
	err := SetConfigValue(rtx, "app", "deploy.region", "us")
	want := "configuration file " + path + " is ini, which this program reads but doesn't write; edit it by hand"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if ie, ok := asError[*InputError](err); !ok || ie.Kind != InputErrorKindWriteRefused || CategoryOf(err) != CategoryUsage {
		t.Errorf("err = %v, want a usage error of kind write-refused", err)
	}
	if got := cwRead(t, path); got != acINI {
		t.Errorf("the file changed: %q", got)
	}
}

type acStdin struct {
	Deploy struct {
		Region string `json:"region"`
	} `json:"deploy"`
}

func TestCodec_stdin(t *testing.T) {
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Stdin     *acStdin `stdin:"ini"`
		}
	}
	rtx := wired(stRTX(strings.NewReader("[deploy]\nregion = eu\n")), Stdin("ini"), iniCodec())
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil {
		t.Fatal(err)
	}
	if in.App.Stdin == nil || in.App.Stdin.Deploy.Region != "eu" {
		t.Errorf("stdin = %+v", in.App.Stdin)
	}

	rtx = wired(stRTX(strings.NewReader("[deploy]\nregion\n")), Stdin("ini"), iniCodec())
	err := NewInputReader(InputSettings{}).Read(rtx, &in)
	if err == nil || !strings.Contains(err.Error(), "could not parse stdin as ini at line 2, column 1: expected key = value") {
		t.Errorf("err = %v", err)
	}

	rtx = wired(stRTX(strings.NewReader("[deploy]\n")), Stdin("ini"))
	var we *WiringError
	if err := NewInputReader(InputSettings{}).Read(rtx, &in); !errors.As(err, &we) || !strings.Contains(we.Msg, `add rotini.Codec("ini", …)`) {
		t.Errorf("err = %v, want a *WiringError naming rotini.Codec", err)
	}
}

func TestCodec_replacesABuiltIn(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}
	called := false
	own := Codec("yaml", func(data []byte) (any, error) {
		called = true
		return map[string]any{"name": "own"}, nil
	})
	p := NewProgramFunc(def, nil).WithFeatures(own, Env())
	if p.feats == allFeatures() || !p.feats.all || p.feats.env == nil {
		t.Fatal("WithFeatures did not give a legacy program its codec on a set of its own with every feature")
	}
	if c := allFeatures().codec("yaml"); c.author {
		t.Error("the shared set with every feature changed")
	}
	c := p.feats.codec("yaml")
	if c == nil || !c.author {
		t.Fatal("the program's yaml codec is not its own")
	}
	if _, err := c.codec().Decode([]byte("x: 1")); err != nil || !called {
		t.Errorf("Decode = %v, called %v", err, called)
	}
	if p.WithFeatures(iniCodec()); p.feats.codec("yaml") != c || p.feats.codec("ini") == nil {
		t.Error("a second WithFeatures dropped the first's codec")
	}

	// A built-in wired after a program's own codec of its name doesn't replace it.
	s := newFeatureSet([]Feature{own, YAML()})
	if !s.codec("yaml").author || len(s.codecs) != 1 {
		t.Error("a built-in replaced the program's own codec")
	}
}

func TestCodec_extensionsLaterWins(t *testing.T) {
	own := Codec("own-json", decodeINI, CodecExtensions(".json"))
	s := newFeatureSet([]Feature{JSON(), own})
	c, err := s.codecFor("", filepath.Join("x", "app.JSON"))
	if err != nil || c == nil || c.name != "own-json" {
		t.Errorf("codecFor = %v, %v; want the codec wired last", c, err)
	}
}

// An extension given with or without its dot, in any case, is stored dotted and lower-case,
// and a file with that extension is read with the codec.
func TestCodec_extensionsWithOrWithoutDot(t *testing.T) {
	c := &codecEntry{}
	CodecExtensions("ini", ".INI", "Cfg", "", ".")(c)
	if want := []string{".ini", ".cfg"}; !slices.Equal(c.exts, want) {
		t.Errorf("exts = %q, want %q", c.exts, want)
	}
	for _, ext := range []string{"ini", ".ini"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := pfWrite(t, dir, "app.INI", acINI)
			files := []ConfigFile{{Name: "app", Path: path}}
			rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files})
			rtx = wired(rtx, ConfigFiles(), Schemas(), Codec("ini", decodeINI, CodecExtensions(ext)))
			var in acInputs
			if err := NewInputReader(InputSettings{ConfigFiles: files}).Read(rtx, &in); err != nil {
				t.Fatal(err)
			}
			if in.App.Config.Replicas != 3 || in.App.Config.Region != "eu" {
				t.Errorf("config = %+v", in.App.Config)
			}
		})
	}
}

func TestCodec_panics(t *testing.T) {
	for _, tt := range []struct {
		name   string
		decode func([]byte) (any, error)
	}{{"", decodeINI}, {"INI", decodeINI}, {"1ini", decodeINI}, {"in_i", decodeINI}, {"ini", nil}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Codec(%q) did not panic", tt.name)
				}
			}()
			Codec(tt.name, tt.decode)
		}()
	}
	if f := Codec("my-format2", decodeINI); f.String() != "codec:my-format2" {
		t.Errorf("String = %q", f.String())
	}
}

// A program's own format keeps profiles and provenance as the built-in formats have them.
func TestCodec_profilesAndProvenance(t *testing.T) {
	dir := t.TempDir()
	path := pfWrite(t, dir, "app.conf", `{"deploy":{"region":"eu"},"profiles":{"prod":{"deploy":{"region":"us"}}}}`)
	files := []ConfigFile{{Name: "app", Path: path, Format: "own-json", Profiles: &ProfilesDef{Under: "profiles", Flag: "profile"}}}
	own := Codec("own-json", func(data []byte) (any, error) {
		var v any
		err := json.Unmarshal(data, &v)
		return v, err
	})
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "profile", Identifiers: []string{"--profile"}, Type: "string"}}}
	rtx := wired(NewContextFor(def, []string{"--profile", "prod"}).WithDir(dir).WithInputSettings(InputSettings{ConfigFiles: files}),
		ConfigFiles(), Profiles(), own)
	type inputs struct {
		App struct {
			Flags struct {
				Profile string `rotini:"profile"`
			}
			Arguments struct{}
			Config    struct {
				Region string `rotini:"region" recon:"deploy.region"`
			}
		}
	}
	in, report, err := rtx.InputsWithReport[inputs]()
	if err != nil {
		t.Fatal(err)
	}
	if in.App.Config.Region != "us" {
		t.Errorf("region = %q, want the profile's", in.App.Config.Region)
	}
	var b strings.Builder
	if err := report.Explain(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `region = "us" from deploy.region in app.conf (profile prod)`) {
		t.Errorf("Explain = %q, want it to name the file and the profile", b.String())
	}
}

// An object flag's JSON or YAML value decodes with a program's own codec of that name.
func TestCodec_objectFlags(t *testing.T) {
	own := Codec("yaml", func([]byte) (any, error) { return map[string]any{"host": "own"}, nil })
	s := newFeatureSet([]Feature{YAML(), own})
	m, err := decodeObject(s, "host: a\nport: 1\n", nil)
	if err != nil || m["host"] != "own" {
		t.Errorf("decodeObject = %v, %v; want the program's codec's", m, err)
	}
}
