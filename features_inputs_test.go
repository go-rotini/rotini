package rotini

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-rotini/recon"
	"github.com/go-rotini/yaml"
)

// everyFeatureContext is a bare Context, outside any program, with every feature.
func everyFeatureContext() *Context {
	rtx := newContext()
	rtx.clock.feats = allFeatures()
	rtx.view = rtx.view.withClock(&rtx.clock)
	return rtx
}

// everyFeatureView is a view of the process environment with every feature.
func everyFeatureView() *osView {
	return (*osView)(nil).withClock(&runClock{feats: allFeatures()})
}

type wrEnvOnly struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Env       struct {
			Region string `rotini:"region" recon:"region"`
		}
	}
}

type wrConfigOnly struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Endpoint string `rotini:"endpoint" recon:"api.endpoint"`
		}
	}
}

type wrFallback struct {
	App struct {
		Flags struct {
			Region string `rotini:"region" recon:"region" env:"WR_REGION"`
		}
		Arguments struct{}
	}
}

type wrExpand struct {
	App struct {
		Flags struct {
			Root string `rotini:"root" expand:"env"`
		}
		Arguments struct{}
	}
}

type wrRelative struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Dir string `rotini:"dir" recon:"dir" relativeto:"config"`
		}
	}
}

type wrStdinYAML struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
		Stdin     *stDocStdin `stdin:"yaml"`
	}
}

type wrPlain struct {
	App struct {
		Flags struct {
			Root string `rotini:"root"`
		}
		Arguments struct{}
	}
}

func wrDef() Definition {
	return Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "root", Identifiers: []string{"--root"}, Type: "string"},
		{Name: "region", Identifiers: []string{"--region"}, Type: "string"},
	}}
}

func wrFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestWiring_inputs reads each input channel with every feature it needs and then with one of
// them left out: the first run reads, the second fails with a WiringError naming the feature.
func TestWiring_inputs(t *testing.T) {
	yamlFile := wrFile(t, "app.yaml", "api:\n  endpoint: e\ndir: sub\nprofiles:\n  dev:\n    api:\n      endpoint: d\n")
	envFile := wrFile(t, "local.env", "REGION=west\n")
	t.Setenv("WR_ROOT", "/r")
	for _, tc := range []struct {
		name     string
		out      func() any
		meta     InputSettings
		argv     []string
		stdin    string
		features []Feature
		drop     string
	}{
		{"env", func() any { return &wrEnvOnly{} }, InputSettings{}, nil, "", []Feature{Env()}, "env"},
		{"config files", func() any { return &wrConfigOnly{} }, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: yamlFile}}}, nil, "",
			[]Feature{ConfigFiles(), YAML()}, "config-files"},
		{"config codec", func() any { return &wrConfigOnly{} }, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: yamlFile}}}, nil, "",
			[]Feature{ConfigFiles(), YAML()}, "codec:yaml"},
		{"profiles", func() any { return &wrConfigOnly{} },
			InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: yamlFile, Profiles: &ProfilesDef{Under: "profiles", Default: "dev"}}}}, nil, "",
			[]Feature{ConfigFiles(), YAML(), Profiles()}, "config-profiles"},
		{"schemas", func() any { return &wrConfigOnly{} },
			InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: yamlFile, Schema: `{"type":"object"}`}}}, nil, "",
			[]Feature{ConfigFiles(), YAML(), Schemas()}, "schemas"},
		{"env files", func() any { return &wrEnvOnly{} }, InputSettings{ConfigFiles: []ConfigFile{{Name: "local", Path: envFile, As: "env"}}}, nil, "",
			[]Feature{Env(), EnvFiles(), Dotenv()}, "env-files"},
		{"dotenv codec", func() any { return &wrEnvOnly{} }, InputSettings{ConfigFiles: []ConfigFile{{Name: "local", Path: envFile, As: "env"}}}, nil, "",
			[]Feature{Env(), EnvFiles(), Dotenv()}, "codec:dotenv"},
		{"fallbacks", func() any { return &wrFallback{} }, InputSettings{}, nil, "", []Feature{Fallbacks()}, "fallbacks"},
		{"argv expansion", func() any { return &wrExpand{} }, InputSettings{}, []string{"--root", "$WR_ROOT/x"}, "", []Feature{PathExpansion()}, "path-expansion"},
		{"relative to config", func() any { return &wrRelative{} }, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: yamlFile}}}, nil, "",
			[]Feature{ConfigFiles(), YAML(), PathExpansion()}, "path-expansion"},
		{"stdin", func() any { return &wrStdinYAML{} }, InputSettings{}, nil, "name: x\n", []Feature{Stdin("yaml"), YAML()}, "stdin:yaml"},
		{"stdin codec", func() any { return &wrStdinYAML{} }, InputSettings{}, nil, "name: x\n", []Feature{Stdin("yaml"), YAML()}, "codec:yaml"},
		{"stdin schema", func() any { return &wrStdinYAML{} }, InputSettings{StdinSchemas: map[string]string{"stDocStdin": `{"type":"object"}`}}, nil, "name: x\n",
			[]Feature{Stdin("yaml"), YAML(), Schemas()}, "schemas"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(fs []Feature) error {
				rtx := wired(NewContextFor(wrDef(), tc.argv), fs...)
				rtx.Stdin = strings.NewReader(tc.stdin)
				return NewInputReader(tc.meta).Read(rtx, tc.out())
			}
			if err := read(tc.features); err != nil {
				t.Fatalf("with every feature it needs: %v", err)
			}
			var less []Feature
			for _, f := range tc.features {
				if f.String() != tc.drop && !(strings.HasPrefix(tc.drop, "stdin:") && strings.HasPrefix(f.String(), "stdin(")) {
					less = append(less, f)
				}
			}
			err := read(less)
			we, ok := errors.AsType[*WiringError](err)
			if !ok {
				t.Fatalf("without %s: err = %v, want a *WiringError", tc.drop, err)
			}
			if we.Feature != tc.drop || !strings.Contains(we.Error(), "does not wire "+tc.drop) || CategoryOf(err) != CategoryInternal {
				t.Errorf("without %s: %#v (category %v)", tc.drop, we, CategoryOf(err))
			}
		})
	}
}

// TestWiring_nothingNeeded reads inputs that need no feature with none wired, as with all.
func TestWiring_nothingNeeded(t *testing.T) {
	for _, fs := range [][]Feature{nil, {Env(), ConfigFiles(), Fallbacks(), PathExpansion(), Stdin(), JSON(), YAML()}} {
		var in wrPlain
		rtx := wired(NewContextFor(wrDef(), []string{"--root", "~/x"}), fs...)
		if err := NewInputReader(InputSettings{}).Read(rtx, &in); err != nil || in.App.Flags.Root != "~/x" {
			t.Errorf("features %v: root = %q, %v", fs, in.App.Flags.Root, err)
		}
	}
}

// TestWiring_runtimeConfigPath reads a configuration file whose path is chosen at run time:
// every document codec and dotenv, as the generated program wires them, read any of them.
func TestWiring_runtimeConfigPath(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "config", Identifiers: []string{"--config"}, Type: "string"}}}
	type cfgIn struct {
		App struct {
			Flags struct {
				Config string `rotini:"config"`
			}
			Arguments struct{}
			Config    struct {
				Endpoint string `rotini:"endpoint" recon:"endpoint"`
			}
		}
	}
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "app", PathFrom: &PathFromDef{Flag: "config"}}}}
	for name, body := range map[string]string{
		"c.yaml": "endpoint: e\n", "c.json": `{"endpoint":"e"}`, "c.toml": "endpoint = \"e\"\n",
		"c.jsonc": "{\"endpoint\": \"e\" // c\n}", "c.env": "endpoint=e\n",
	} {
		path := wrFile(t, name, body)
		var in cfgIn
		rtx := wiredFor(NewContextFor(def, []string{"--config", path}), def, meta, reflect.TypeFor[cfgIn]())
		if err := NewInputReader(meta).Read(rtx, &in); err != nil || in.App.Config.Endpoint != "e" {
			t.Errorf("%s: endpoint = %q, %v", name, in.App.Config.Endpoint, err)
		}
	}
}

// oldDecodeMessage is decodeMessage as it read errors before positions came through
// recon.PositionError.
func oldDecodeMessage(err error) string {
	if errors.Is(err, recon.ErrUnsupportedFormat) {
		return "the top level must be " + topLevelKind(err.Error())
	}
	if msg := recon.ParseMessage(err); msg != err.Error() {
		return msg
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return err.Error()
}

// TestDecodeErrors_matchRecon checks that every bundled decoder's failures report the position
// and message recon's parsers report, through the codecs and through rotini's own decoding.
func TestDecodeErrors_matchRecon(t *testing.T) {
	docs := map[string][]string{
		"json":   {`{"a":`, `{"a" 1}`, "{\n  \"a\": [1,\n  }\n}", `[1, 2`, `"x" y`, "\xef\xbb\xbf{\n\"a\":}", `{}{}`, `nope`, `1`},
		"yaml":   {"a: [1, 2\n", "a:\n  - b\n c: d\n", "\"unterminated\n", "a: b: c\n", "- 1\n", "a: *nope\n"},
		"jsonc":  {`{"a": 1,,}`, "{\n// c\n\"a\": }", `[1, 2`, `{"a" 1}`},
		"toml":   {"a = \n", "[x\nb = 1\n", "a = 1\na = 2\n"},
		"dotenv": {"A='x\n", "=x\n"},
	}
	fs := allFeatures()
	for format, bodies := range docs {
		c := fs.codec(format)
		for _, body := range bodies {
			data := []byte(body)
			var errs []error
			if _, err := c.codec().Decode(stripBOM(data)); err != nil {
				errs = append(errs, err)
			}
			if c.decodeAny != nil {
				if _, err := c.decodeAny(stripBOM(data)); err != nil {
					errs = append(errs, err)
				}
			}
			for _, err := range errs {
				wl, wc, wok := recon.ParsePosition(err, data)
				gl, gc, gok := parsePosition(err)
				if wl != gl || wc != gc || wok != gok {
					t.Errorf("%s %q: position = %d:%d %v, want %d:%d %v (%v)", format, body, gl, gc, gok, wl, wc, wok, err)
				}
				if got, want := decodeMessage(err), oldDecodeMessage(err); got != want {
					t.Errorf("%s %q: message = %q, want %q", format, body, got, want)
				}
				if got, want := parseMessage(err, nil), recon.ParseMessage(err); got != want {
					t.Errorf("%s %q: parse message = %q, want %q", format, body, got, want)
				}
			}
		}
	}
}

// TestJSONDecodeMessage_matchesRecon checks encoding/json's binding failures read as before.
func TestJSONDecodeMessage_matchesRecon(t *testing.T) {
	type rec struct {
		N int `json:"n"`
	}
	for _, body := range []string{`{"n":"x"}`, `{"n":1.5}`, `{"n":`, `[1]`, `{"n":1}x`, `{"n":{"a":1}}`} {
		var r rec
		err := json.Unmarshal([]byte(body), &r)
		if err == nil {
			continue
		}
		if got, want := jsonDecodeMessage(err), oldDecodeMessage(err); got != want {
			t.Errorf("%q: message = %q, want %q", body, got, want)
		}
	}
}

// A stdin document of any shape that gives a key twice is an error at the second occurrence,
// in every format rotini decodes itself.
func TestDecodeAny_duplicateKey(t *testing.T) {
	docs := map[string]string{
		"json":  "[{\"a\": 1,\n  \"a\": 2}]",
		"jsonc": "[{\"a\": 1,\n  \"a\": 2}]",
		"yaml":  "- a: 1\n  a: 2\n",
	}
	fs := allFeatures()
	var probe any
	if yaml.UnmarshalWithOptions([]byte("- a: 1\n  a: 2\n"), &probe, yaml.WithDisallowDuplicateKey()) == nil {
		delete(docs, "yaml") // this yaml release checks only typed maps
	}
	for format, body := range docs {
		_, err := fs.codec(format).decodeAny([]byte(body))
		if err == nil {
			t.Errorf("%s: no error, want a duplicate-key error", format)
			continue
		}
		line, _, ok := parsePosition(err)
		if !ok || line != 2 || decodeMessage(err) != `duplicate key "a"` {
			t.Errorf("%s: line %d (%v), message %q; want line 2, duplicate key \"a\"", format, line, ok, decodeMessage(err))
		}
	}
}
