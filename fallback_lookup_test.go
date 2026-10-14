package rotini

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"
)

type flInputs struct {
	App struct {
		Flags struct {
			Host    string            `rotini:"host" recon:"host" env:"FL_HOST"`
			Port    int               `rotini:"port" recon:"port"`
			Verbose bool              `rotini:"verbose" recon:"verbose" env:"FL_VERBOSE,FL_V"`
			Wait    time.Duration     `rotini:"wait" recon:"net.wait"`
			Tags    []string          `rotini:"tags" recon:"tags" env:"FL_TAGS"`
			Labels  map[string]string `rotini:"labels" recon:"labels" env:"FL_LABELS"`
			Mode    string            `rotini:"mode" recon:"mode" env:"FL_MODE"`
			At      time.Time         `rotini:"at" recon:"at" env:"FL_AT"`
			Plain   string            `rotini:"plain"`
		}
		Arguments struct {
			Target string   `rotini:"target" recon:"target" env:"FL_TARGET"`
			Extra  string   `rotini:"extra" recon:"extra"`
			Rest   []string `rotini:"rest" recon:"rest" env:"FL_REST"`
		}
	}
}

func flDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "host", Identifiers: []string{"--host"}, Type: "string", Default: "localhost"},
			{Name: "port", Identifiers: []string{"-p", "--port"}, Type: "int", Default: "80"},
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"},
			{Name: "wait", Identifiers: []string{"--wait"}, Type: "time.Duration", Default: "1s"},
			{Name: "tags", Identifiers: []string{"--tag"}, Type: "[]string", Separator: ","},
			{Name: "labels", Identifiers: []string{"--label"}, Type: "map[string]string"},
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"fast", "slow"}, IgnoreCase: true},
			{Name: "at", Identifiers: []string{"--at"}, Type: "time.Time"},
			{Name: "plain", Identifiers: []string{"--plain"}, Type: "string"},
		},
		Arguments: []ArgDef{
			{Name: "target", Type: "string", Default: "here"},
			{Name: "extra", Type: "string"},
			{Name: "rest", Type: "[]string", Variadic: true, Separator: ":"},
		},
	}
}

// TestFallbackLookup_envMatchesRegistry reads the same command lines and environments through
// the direct environment lookup and through the registry, and requires the same inputs, errors
// and recorded origins.
func TestFallbackLookup_envMatchesRegistry(t *testing.T) {
	envs := []map[string]string{
		nil,
		{"FL_HOST": "h", "PORT": "8", "FL_VERBOSE": "true", "NET_WAIT": "3s", "FL_TAGS": "a,b", "FL_MODE": "FAST", "FL_TARGET": "t", "FL_REST": "x:y"},
		{"FL_HOST": "", "PORT": "", "FL_V": "1", "FL_TAGS": "", "FL_AT": "2024-01-02T03:04:05Z"},
		{"PORT": "notint", "FL_MODE": "medium"},
		{"FL_VERBOSE": "maybe"},
		{"NET_WAIT": "soon", "FL_LABELS": "k=v"},
		{"FL_AT": "yesterday", "FL_REST": "a"},
		{"FL_LABELS": "a=1,b=2", "FL_TARGET": ""},
		{"PX_PORT": "7", "PX_NET_WAIT": "4s", "PX_EXTRA": "e", "PORT": "8", "EXTRA": "u", "FL_V": "1"},
		{"PX_PORT": "bad", "PX_FL_HOST": "p", "FL_HOST": "h", "FL_VERBOSE": "", "FL_V": "true", "PX_TAGS": "q"},
		{"PX_EXTRA": "", "NET_WAIT": "5s", "PX_FL_V": "1", "FL_VERBOSE": "false"},
	}
	argvs := [][]string{
		nil,
		{"--host", "cli"},
		{"-p", "9", "--tag", "z", "--label", "q=r"},
		{"--mode", "slow", "arg"},
		{"arg", "r1", "r2"},
		{"--bogus"},
		{"-v", "--wait", "2m", "--at", "2020-01-01T00:00:00Z"},
		{"--plain", "x"},
	}
	typ := reflect.TypeFor[flInputs]()
	if !plainFallbackKeys(reflect.ValueOf(flInputs{})) {
		t.Fatal("flInputs keys should be plain")
	}
	read := func(meta InputSettings, argv []string) string {
		var in flInputs
		err := NewInputReader(meta).Read(NewContextFor(flDef(), argv), &in)
		return fmt.Sprintf("%+v | %s", in, errChain(err))
	}
	// With a prefix, PORT, NET_WAIT and EXTRA are read as PX_PORT, PX_NET_WAIT and PX_EXTRA, and
	// the explicit names, alternates included, are read as declared.
	for _, prefix := range []string{"", "PX"} {
		meta := InputSettings{EnvPrefix: prefix}
		for i, env := range envs {
			for j, argv := range argvs {
				t.Run(fmt.Sprintf("prefix%q/env%d/argv%d", prefix, i, j), func(t *testing.T) {
					for _, k := range []string{"FL_HOST", "PORT", "FL_VERBOSE", "FL_V", "NET_WAIT", "FL_TAGS", "FL_LABELS", "FL_MODE", "FL_AT", "FL_TARGET", "FL_REST", "EXTRA",
						"PX_PORT", "PX_NET_WAIT", "PX_EXTRA", "PX_FL_HOST", "PX_FL_V", "PX_TAGS"} {
						t.Setenv(k, "")
						if v, ok := env[k]; ok {
							t.Setenv(k, v)
						} else {
							os.Unsetenv(k)
						}
					}
					var fast, slow string
					direct := fallbackPathCounts.direct.Load()
					countPath(typ, func() { fast = read(meta, argv) })
					if fallbackPathCounts.direct.Load() == direct && !slices.Contains(argv, "--bogus") { // --bogus fails before any fallback is read
						t.Fatal("the read did not take the direct lookup")
					}
					viaRegistry(typ, true, func() { slow = read(meta, argv) })
					if fast != slow {
						t.Fatalf("direct lookup and registry differ:\n direct:   %s\n registry: %s", fast, slow)
					}
				})
			}
		}
	}
	// The prefix is in effect: PX_PORT binds, and PORT does not.
	t.Setenv("PORT", "8")
	t.Setenv("PX_PORT", "7")
	var in flInputs
	if err := NewInputReader(InputSettings{EnvPrefix: "PX"}).Read(NewContextFor(flDef(), nil), &in); err != nil || in.App.Flags.Port != 7 {
		t.Errorf("with prefix PX: port = %d, err = %v; want 7, nil", in.App.Flags.Port, err)
	}
}

func TestPlainFallbackKeys(t *testing.T) {
	type dup struct {
		App struct {
			Flags struct {
				A string `rotini:"a" recon:"k"`
				B string `rotini:"b" recon:"k"`
			}
		}
	}
	type nested struct {
		App struct {
			Flags struct {
				A string `rotini:"a" recon:"db"`
			}
			Arguments struct {
				B string `rotini:"b" recon:"db.host"`
			}
		}
	}
	type indexed struct {
		App struct {
			Flags struct {
				A string `rotini:"a" recon:"a[0]"`
			}
		}
	}
	for name, v := range map[string]any{"dup": dup{}, "nested": nested{}, "indexed": indexed{}} {
		if plainFallbackKeys(reflect.ValueOf(v)) {
			t.Errorf("%s: plainFallbackKeys = true, want false", name)
		}
	}
}
