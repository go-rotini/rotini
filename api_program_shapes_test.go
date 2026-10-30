package rotini_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/rotinitest"
)

// The hand-built program below is what `rotini generate` writes for this spec, typed by hand;
// the generated program gives the same messages for the failing cases:
//
//	command:
//	  name: app
//	  flags:
//	    - {name: verbose, identifiers: [-v, --verbose], schema: {type: bool}, cascading: true}
//	  commands:
//	    - name: greet
//	      aliases: [hi]
//	      flags:
//	        - {name: name, identifiers: [-n, --name], schema: {type: string, variable: APP_NAME}}
//	        - {name: count, identifiers: [-c, --count], schema: {type: int, default: 1, minimum: 1}}
//	      arguments:
//	        - {name: who, schema: {type: string, required: true}}

type shapeAppInputs struct {
	App shapeAppCommand
}

type shapeAppCommand struct {
	Flags struct {
		Verbose bool `rotini:"verbose"`
	}
	Arguments struct{}
}

type shapeGreetInputs struct {
	App      shapeAppCommand
	AppGreet struct {
		Flags struct {
			Name  string `rotini:"name" recon:"name" env:"APP_NAME"`
			Count int    `rotini:"count"`
		}
		Arguments struct {
			Who string `rotini:"who"`
		}
	}
}

func shapeDef() rotini.Definition {
	return rotini.Definition{
		Name:    "app",
		Handler: "App",
		Flags:   []rotini.FlagDef{{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"}},
		Inputs:  reflect.TypeFor[shapeAppInputs](),
		Usage:   "app [flags] <command>",
		Commands: []rotini.CommandDef{{
			Name: "greet", Handler: "AppGreet", Aliases: []string{"hi"},
			Flags: []rotini.FlagDef{
				{Name: "name", Identifiers: []string{"-n", "--name"}, Type: "string"},
				{Name: "count", Identifiers: []string{"-c", "--count"}, Type: "int", Default: "1", Minimum: new(float64(1))},
			},
			Arguments: []rotini.ArgDef{{Name: "who", Type: "string", Required: true}},
			Inputs:    reflect.TypeFor[shapeGreetInputs](),
			Usage:     "app greet [flags] <who>",
		}},
	}
}

type shapeRoot struct{ rotini.NoHooks }

func (shapeRoot) Run(_ context.Context, rtx *rotini.Context) {
	if _, err := rtx.Inputs[shapeAppInputs](); err != nil {
		rtx.HaltWith(err)
	}
}

type shapeGreet struct{ rotini.NoHooks }

func (shapeGreet) Run(_ context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[shapeGreetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	g := in.AppGreet
	fmt.Fprintf(rtx.Stdout, "%s %s x%d verbose=%t\n", g.Arguments.Who, g.Flags.Name, g.Flags.Count, in.App.Flags.Verbose)
}

type shapeSet struct{}

func (shapeSet) App() rotini.Handler      { return shapeRoot{} }
func (shapeSet) AppGreet() rotini.Handler { return shapeGreet{} }

func shapeLookup(name string) (rotini.Handler, bool) {
	switch name {
	case "App":
		return shapeRoot{}, true
	case "AppGreet":
		return shapeGreet{}, true
	}
	return nil, false
}

// TestProgramShapes_constructorsAgree runs one hand-built program through every constructor:
// reflection (NewProgram), a lookup with every feature (NewProgramFunc), and a lookup with only
// the features the generated NewProgram passes for this spec (NewWiredProgram). Each run's code,
// stdout and stderr must match.
func TestProgramShapes_constructorsAgree(t *testing.T) {
	t.Parallel()
	builds := map[string]func() *rotini.Program{
		"NewProgram":     func() *rotini.Program { return rotini.NewProgram(shapeDef(), shapeSet{}) },
		"NewProgramFunc": func() *rotini.Program { return rotini.NewProgramFunc(shapeDef(), shapeLookup) },
		"NewWiredProgram": func() *rotini.Program {
			return rotini.NewWiredProgram(shapeDef(), shapeLookup, rotini.Fallbacks(), rotini.Bounds())
		},
	}
	cases := []struct {
		argv []string
		env  []string
		want string // the NewProgram run's stdout+stderr, which every shape must equal
		code int
	}{
		{argv: []string{"greet", "ada"}, want: "ada  x1 verbose=false\n"},
		{argv: []string{"-v", "hi", "--name=x", "-c", "3", "ada"}, want: "ada x x3 verbose=true\n"},
		{argv: []string{"greet", "ada"}, env: []string{"APP_NAME=env"}, want: "ada env x1 verbose=false\n"},
		{argv: []string{"greet", "--name", "flag", "ada"}, env: []string{"APP_NAME=env"}, want: "ada flag x1 verbose=false\n"},
		{argv: []string{"greet"}, want: "Error: missing required input: <who>\n", code: 1},
		{argv: []string{"greet", "-c", "0", "ada"}, want: "Error: -c must be >= 1 (got 0)\n", code: 1},
		{argv: []string{"greet", "--bogus", "ada"}, want: "Error: unknown flag \"--bogus\"\n", code: 1},
		{argv: []string{"nope"}, want: "Error: unknown command \"nope\" for \"app\"\n", code: 1},
	}
	for _, c := range cases {
		var first string
		for _, name := range []string{"NewProgram", "NewProgramFunc", "NewWiredProgram"} {
			var out bytes.Buffer
			env := c.env
			if env == nil {
				env = []string{}
			}
			code, _ := builds[name]().WithStdout(&out).WithStderr(&out).WithEnviron(env).
				WithDir(t.TempDir()).WithoutSignalHandling().Run(c.argv)
			if code != c.code {
				t.Errorf("%s %q: code %d, want %d; output %q", name, c.argv, code, c.code, out.String())
			}
			if c.want != "" && out.String() != c.want {
				t.Errorf("%s %q: output %q, want %q", name, c.argv, out.String(), c.want)
			}
			if first == "" {
				first = out.String()
			} else if out.String() != first {
				t.Errorf("%s %q: output %q differs from NewProgram's %q", name, c.argv, out.String(), first)
			}
		}
	}
}

// TestProgramShapes_rotinitestMatchesRun: rotinitest.Run of a typed inputs value gives what the
// equivalent hand-typed command line gives, for a hand-built program (with rotinitest.Path).
func TestProgramShapes_rotinitestMatchesRun(t *testing.T) {
	t.Parallel()
	var in shapeGreetInputs
	in.App.Flags.Verbose = true
	in.AppGreet.Flags.Name = "x"
	in.AppGreet.Flags.Count = 3
	in.AppGreet.Arguments.Who = "-ada"
	res := rotinitest.Run(t, rotini.NewProgramFunc(shapeDef(), shapeLookup), in)
	if res.Code != 0 || string(res.Stdout) != "-ada x x3 verbose=true\n" || len(res.Stderr) != 0 {
		t.Errorf("rotinitest: code %d stdout %q stderr %q argv %q", res.Code, res.Stdout, res.Stderr, res.Argv)
	}
	var out bytes.Buffer
	code, err := rotini.NewProgramFunc(shapeDef(), shapeLookup).WithStdout(&out).WithoutSignalHandling().
		WithEnviron(res.Env).Run(res.Argv)
	if code != res.Code || err != nil || out.String() != string(res.Stdout) {
		t.Errorf("Run(%q): code %d err %v stdout %q; rotinitest gave %d %q", res.Argv, code, err, out.String(), res.Code, res.Stdout)
	}
}
