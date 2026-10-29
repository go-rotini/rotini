package rotini

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The conformance rows for a configuration value of the wrong shape (a list or map where an
// input takes one value) and for an empty environment variable over a .env file.

type shpInputs struct {
	App struct {
		Flags struct {
			Explain bool              `rotini:"explain"`
			Name    string            `rotini:"name" recon:"name" env:"APP_NAME"`
			Port    int               `rotini:"port" recon:"port" env:"APP_PORT"`
			Labels  map[string]string `rotini:"labels" recon:"labels"`
			Tags    []string          `rotini:"tags" recon:"tags"`
		}
		Arguments struct {
			Target string   `rotini:"target" recon:"target" env:"APP_TARGET"`
			Rest   []string `rotini:"rest" recon:"rest"`
		}
		Env struct {
			Region string `rotini:"region" recon:"region" env:"APP_REGION"`
		}
		Config struct {
			Host  string            `rotini:"host" recon:"host"`
			Owner map[string]string `rotini:"owner" recon:"owner"`
			Extra map[string]any    `rotini:"extra" recon:"extra"`
		}
	}
}

func shpDef() Definition {
	return Definition{
		Name: "app", Handler: "App", Inputs: reflect.TypeFor[shpInputs](),
		Flags: []FlagDef{
			{Name: "explain", Identifiers: []string{"--explain"}, Type: "bool", ShortCircuit: true},
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", Required: true},
			{Name: "port", Identifiers: []string{"--port"}, Type: "int"},
			{Name: "labels", Identifiers: []string{"--label"}, Type: "map[string]string"},
			{Name: "tags", Identifiers: []string{"--tag"}, Type: "[]string"},
		},
		Arguments: []ArgDef{
			{Name: "target", Type: "string", Default: "here"},
			{Name: "rest", Type: "[]string", Variadic: true},
		},
	}
}

// shpContext runs shpDef in a directory holding app.yaml (config, when not empty) and .env
// (dotenv, when not empty).
func shpContext(t *testing.T, config, dotenv string, env []string, argv ...string) *Context {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"app.yaml": config, ".env": dotenv} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	meta := InputSettings{ConfigFiles: []ConfigFile{
		{Name: "app", Path: "app.yaml", Format: "yaml"},
		{Name: "dotenv", Path: ".env", Format: "dotenv", As: "env"},
	}}
	return NewContextFor(shpDef(), argv).WithDir(dir).WithEnviron(env).WithInputSettings(meta)
}

// shpWantError checks that every reader reports a usage *InputError on channel whose message
// starts with want and names app.yaml.
func shpWantError(t *testing.T, rtx *Context, channel, want string) {
	t.Helper()
	for reader, err := range readerErrors[shpInputs](rtx, "files") {
		var ie *InputError
		if !errors.As(err, &ie) || ie.Channel != channel || !errors.Is(err, ErrUsage) ||
			!strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "app.yaml") {
			t.Errorf("%s = %v, want a %s usage error starting %q", reader, err, channel, want)
		}
	}
}

func shapeConformanceCases() []dataCase {
	return []dataCase{
		{"CFG-13", func(t *testing.T) { // a list or map for a config input that takes one value is an error naming the key and file
			shpWantError(t, shpContext(t, "name: n\nhost: [a, b]\n", "", nil), channelConfig, "config key host takes one value, not a list (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\nhost: {a: b}\n", "", nil), channelConfig, "config key host takes one value, not a map (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\nowner: {team: [a, b]}\n", "", nil), channelConfig, "config key owner takes one value per key, not a list (key team) (from configuration file ")
			in, err := shpContext(t, "name: n\nhost: h\nowner: {team: core}\nextra: {ids: [1, 2]}\n", "", nil).Inputs[shpInputs]()
			c := in.App.Config
			if err != nil || c.Host != "h" || c.Owner["team"] != "core" || fmt.Sprint(c.Extra["ids"]) != "[1 2]" {
				t.Errorf("config = %+v, %v", c, err)
			}
		}},
		{"FLAG-29", func(t *testing.T) { // a list or map for a single-value flag's config fallback is an error; argv repeats keep the last value
			shpWantError(t, shpContext(t, "name: [a, b]\n", "", nil), channelFlag, "--name takes one value, not a list (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\nport: {a: 1}\n", "", nil), channelFlag, "--port takes one value, not a map (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\nlabels: {team: [a, b]}\n", "", nil), channelFlag, "--label takes one value per key, not a list (key team) (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\ntags: [[a], b]\n", "", nil), channelFlag, "--tag takes one value per item, not a list (item 1) (from configuration file ")
			// The command line overrides the bad value, and a short-circuit flag skips it.
			for _, argv := range [][]string{{"--name", "x", "--name", "y"}, {"--explain"}} {
				in, err := shpContext(t, "name: [a, b]\n", "", nil, argv...).Inputs[shpInputs]()
				if want := map[bool]string{true: "y", false: ""}[len(argv) > 1]; err != nil || in.App.Flags.Name != want {
					t.Errorf("%v: name = %q, %v; want %q", argv, in.App.Flags.Name, err, want)
				}
			}
			in, err := shpContext(t, "name: n\nlabels: {team: core, tier: {x: 1}}\ntags: [a, b]\n", "", nil).Inputs[shpInputs]()
			f := in.App.Flags
			if err != nil || f.Labels["tier.x"] != "1" || !reflect.DeepEqual(f.Tags, []string{"a", "b"}) {
				t.Errorf("flags = %+v, %v", f, err)
			}
		}},
		{"ARG-21", func(t *testing.T) { // a list or map for a single-value argument is an error; a variadic argument takes a list
			shpWantError(t, shpContext(t, "name: n\ntarget: [a, b]\n", "", nil), channelArgument, "<target> takes one value, not a list (from configuration file ")
			shpWantError(t, shpContext(t, "name: n\ntarget: {a: b}\n", "", nil), channelArgument, "<target> takes one value, not a map (from configuration file ")
			in, err := shpContext(t, "name: n\ntarget: t\nrest: [a, b]\n", "", nil).Inputs[shpInputs]()
			if a := in.App.Arguments; err != nil || a.Target != "t" || !reflect.DeepEqual(a.Rest, []string{"a", "b"}) {
				t.Errorf("arguments = %+v, %v", a, err)
			}
		}},
		{"ENV-14", func(t *testing.T) { // a variable set to "" leaves the .env file's value, for both readers and the report
			env := []string{"APP_NAME=", "APP_REGION=", "APP_PORT="}
			rtx := shpContext(t, "port: 9\n", "APP_NAME=dot\nAPP_REGION=eu\n", env)
			in, err := rtx.Inputs[shpInputs]()
			merged, rep, rerr := rtx.InputsWithReport[shpInputs]()
			for reader, got := range map[string]struct {
				in  shpInputs
				err error
			}{"Inputs": {in, err}, "InputsWithReport": {merged, rerr}} {
				a := got.in.App
				if got.err != nil || a.Flags.Name != "dot" || a.Env.Region != "eu" || a.Flags.Port != 9 {
					t.Errorf("%s: name %q, region %q, port %d, %v", reader, a.Flags.Name, a.Env.Region, a.Flags.Port, got.err)
				}
			}
			if w, _ := rep.Winner("App.Flags.Name"); w.Origin != "env:APP_NAME" || w.Raw != "dot" {
				t.Errorf("name winner = %+v", w)
			}
			if w, _ := rep.Winner("App.Flags.Port"); w.Origin != "config:app#port" {
				t.Errorf("port winner = %+v, want the config file past the empty variable", w)
			}
			// The .env file's bad value is reported as coming from it.
			_, err = shpContext(t, "name: n\n", "APP_PORT=abc\n", []string{"APP_PORT="}).Inputs[shpInputs]()
			if err == nil || !strings.Contains(err.Error(), "(from environment variable APP_PORT (from .env))") {
				t.Errorf("bad .env value: %v", err)
			}
			// Without a .env value, an empty variable is still no value for a flag.
			if _, err := shpContext(t, "", "", []string{"APP_NAME="}).Inputs[shpInputs](); err == nil || !strings.Contains(err.Error(), "--name") {
				t.Errorf("empty variable alone: %v, want --name missing", err)
			}
		}},
	}
}

func TestConformance_ShapeMatrix(t *testing.T) {
	wiringModes(t, func(t *testing.T) {
		for _, c := range shapeConformanceCases() {
			t.Run(c.id, c.check)
		}
	})
}
