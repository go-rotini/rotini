package rotini

import (
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

type needsKitchenInputs struct {
	App struct {
		Flags struct {
			Region string `rotini:"region" recon:"region" envfile:"REGION_FILE" env:"REGION"`
			Root   string `rotini:"root" expand:"home"`
		}
		Arguments struct{}
		Env       struct {
			Home *url.URL `rotini:"home" recon:"home" env:"HOME_URL"`
			Port int      `rotini:"port" recon:"port" env:"PORT" min:"1"`
		}
		Config struct {
			Zone  *time.Location                 `rotini:"zone" recon:"zone"`
			Hosts []netip.Addr                   `rotini:"hosts" recon:"hosts"`
			Name  string                         `rotini:"name" recon:"name" pattern:"^[a-z]+$"`
			Nest  struct{ MAC net.HardwareAddr } `rotini:"nest" recon:"nest"`
		}
		Stdin *struct{} `stdin:"jsonl,stream"`
	}
}

type needsPlainInputs struct {
	App struct {
		Flags struct {
			Verbose bool `rotini:"verbose"`
		}
		Arguments struct{}
	}
}

type needsMailInputs struct {
	App struct {
		Flags struct {
			To mail.Address   `rotini:"to"`
			Re *regexp.Regexp `rotini:"re"`
		}
		Arguments struct{}
	}
}

func TestFeatureNeeds(t *testing.T) {
	plain := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"}}}
	cases := []struct {
		name   string
		def    Definition
		meta   InputSettings
		inputs []reflect.Type
		want   []string
	}{
		{"nothing", plain, InputSettings{}, typesOf(needsPlainInputs{}), nil},
		{"program", Definition{
			Name: "app", ResponseFiles: &ResponseFilesDef{Prefix: "@"}, Multicall: &MulticallDef{},
			Flags:    []FlagDef{{Name: "dir", Identifiers: []string{"-C"}, Type: "string", Role: "chdir"}},
			Commands: []CommandDef{{Name: "sub", Commands: []CommandDef{{Name: "leaf", PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"}}}}},
		}, InputSettings{}, nil, []string{"plugins", "response-files", "chdir", "multicall"}},
		{"validation", Definition{
			Name:             "app",
			FlagGroups:       []FlagGroup{{Kind: FlagGroupOneOf, Flags: []string{"a", "b"}}},
			FlagDependencies: []FlagDependency{{When: "a", Requires: []string{"b"}}},
			Flags: []FlagDef{
				{Name: "a", Type: "int", Maximum: new(3.0)},
				{Name: "b", Type: "string", Pattern: "x"},
				{Name: "db", Type: "map[string]any", ObjectSchema: "{}"},
			},
			Arguments: []ArgDef{{Name: "src", Type: "[]inputfile"}, {Name: "url", Type: "map[string]*url.URL"}, {Name: "net", Type: "[]netip.Prefix"}},
		}, InputSettings{}, nil, []string{
			"object-flags", "schemas", "codec:json", "codec:yaml", "bounds", "patterns", "flag-groups",
			"flag-dependencies", "stream-paths", "values:url", "values:ip",
		}},
		{"settings", plain, InputSettings{
			ConfigFiles: []ConfigFile{
				{Name: "a", Path: "~bob/app.toml", Profiles: &ProfilesDef{}},
				{Name: "b", Discover: &DiscoverDef{File: "app.jsonc"}, Schema: "{}"},
				{Name: "c", As: "env", Path: ".env"},
			},
		}, nil, []string{"config-files", "config-profiles", "env-files", "path-expansion", "schemas", "codec:toml", "codec:jsonc", "codec:dotenv"}},
		{"runtime path", plain, InputSettings{ConfigFiles: []ConfigFile{{Name: "a", PathFrom: &PathFromDef{Flag: "config"}}}},
			nil, []string{"config-files", "path-expansion", "codec:json", "codec:yaml", "codec:toml", "codec:jsonc", "codec:dotenv"}},
		{"inputs types", plain, InputSettings{}, typesOf(needsKitchenInputs{}), []string{
			"stdin(jsonl)", "env", "config-files", "env-files", "fallbacks", "path-expansion", "codec:json", "bounds",
			"patterns", "values:url", "values:timezone", "values:mac", "values:ip",
		}},
		{"recorded inputs", Definition{Name: "app", Inputs: reflect.TypeFor[needsMailInputs]()}, InputSettings{}, nil,
			[]string{"values:email", "values:regexp"}},
		{"custom sources", plain, InputSettings{Sources: make([]recon.Source, 1), StdinSchemas: map[string]string{"S": "{}"}}, nil,
			[]string{"config-files", "schemas"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := featureNames(featureNeeds(c.def, c.meta, c.inputs...)); !slices.Equal(got, c.want) {
				t.Errorf("featureNeeds = %q\nwant           %q", got, c.want)
			}
		})
	}
}

// TestFeatureNeeds_wiredMatchesLegacy runs a command needing no features on a program wired
// with none, and checks it behaves as one with every feature.
func TestFeatureNeeds_wiredMatchesLegacy(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "verbose", Identifiers: []string{"-v"}, Type: "bool"}}}
	if needs := featureNeeds(def, InputSettings{}, typesOf(needsPlainInputs{})...); len(needs) != 0 {
		t.Fatalf("needs %q", featureNames(needs))
	}
	run := func(p *Program) (int, bool) {
		var verbose bool
		p.lookup = func(string) (Handler, bool) {
			return funcHandler{run: func(rtx *Context) {
				in, err := rtx.Inputs[needsPlainInputs]()
				if err != nil {
					rtx.HaltWith(err)
					return
				}
				verbose = in.App.Flags.Verbose
			}}, true
		}
		code, _ := p.Run([]string{"-v"})
		return code, verbose
	}
	lc, lv := run(NewProgramFunc(def, nil))
	wc, wv := run(NewWiredProgram(def, nil))
	if lc != 0 || wc != lc || wv != lv || !wv {
		t.Errorf("legacy %d/%v, wired %d/%v", lc, lv, wc, wv)
	}
}
