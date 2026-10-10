package rotini

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

// TestMaxLengthZero pins that maxLength: 0 is a bound, not an unset one: only an empty value
// passes, on the command line and from the environment.
func TestMaxLengthZero(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "name", Identifiers: []string{"--name"}, Type: "string", MaxLength: new(0)},
	}}
	var in struct {
		App struct {
			Flags struct {
				Name string `rotini:"name"`
			}
			Arguments struct{}
		}
	}
	err := NewParser().Parse(NewContextFor(def, []string{"--name", "x"}), &in)
	if want := "--name must be at most 0 characters long (got 1)"; err == nil || err.Error() != want {
		t.Errorf("--name x: err = %v, want %q", err, want)
	}
	if err := NewParser().Parse(NewContextFor(def, []string{"--name", ""}), &in); err != nil {
		t.Errorf(`--name "": err = %v, want accepted`, err)
	}

	var envIn struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Env       struct {
				Tag string `rotini:"tag" recon:"tag" env:"APP_TAG" maxlen:"0"`
			}
		}
	}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron([]string{"APP_TAG=x"})
	err = NewInputReader(InputSettings{}).Read(rtx, &envIn)
	if err == nil || !strings.Contains(err.Error(), "APP_TAG must be at most 0 characters long") {
		t.Errorf("APP_TAG=x: err = %v, want the bound", err)
	}
}

type timingRootInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
	}
}

type timingLeafInputs struct {
	App struct {
		Flags     struct{}
		Arguments struct{}
	}
	AppBuild struct {
		Flags     struct{}
		Arguments struct{}
		Config    struct {
			Port int `rotini:"port" recon:"port"`
		}
	}
}

type timingRoot struct {
	NoPreRun
	NoPostRun
	NoCascadingPostRun
	err *error
}

func (h *timingRoot) CascadingPreRun(_ context.Context, rtx *Context) {
	_, *h.err = rtx.Inputs[timingRootInputs]()
}

func (*timingRoot) Run(context.Context, *Context) {}

type timingLeaf struct {
	NoHooks
	err *error
}

func (h *timingLeaf) Run(_ context.Context, rtx *Context) {
	_, *h.err = rtx.Inputs[timingLeafInputs]()
	rtx.HaltWith(*h.err)
}

// TestInputs_configErrorTiming pins where a malformed configuration file is reported: by the
// command whose inputs read configuration, not by a root hook whose inputs read none.
func TestInputs_configErrorTiming(t *testing.T) {
	path := writeConfig(t, "port: [unclosed\n")
	var rootErr, leafErr error
	def := Definition{Name: "app", Handler: "App", Commands: []CommandDef{{Name: "build", Handler: "AppBuild"}}}
	p := NewProgramFunc(def, func(name string) (Handler, bool) {
		switch name {
		case "App":
			return &timingRoot{err: &rootErr}, true
		case "AppBuild":
			return &timingLeaf{err: &leafErr}, true
		}
		return nil, false
	}).WithInputSettings(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: path, Format: "yaml"}}}).
		WithoutSignalHandling().WithStdout(io.Discard).WithStderr(io.Discard)

	if code, _ := p.Run([]string{"build"}); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if rootErr != nil {
		t.Errorf("root hook: err = %v, want nil (its inputs read no configuration)", rootErr)
	}
	if leafErr == nil || !strings.Contains(leafErr.Error(), "could not parse configuration file "+path) {
		t.Errorf("leaf: err = %v, want the malformed file reported", leafErr)
	}
}

// TestComplete_digitOptionIsNotPending pins that a declared digit option is a bool, so the word
// after it completes the next argument, not a value for it.
func TestComplete_digitOptionIsNotPending(t *testing.T) {
	def := boundaryDef()
	def.Commands[2].Arguments[0].Enum = []string{"h1", "h2"}
	if got := complete(def, []string{"ssh", "-4", ""}, nil, nil); !slices.Equal(got, []string{"h1", "h2"}) {
		t.Errorf("ssh -4 <TAB> = %q, want the host argument's enum", got)
	}
	if got := complete(def, []string{"ssh", "-p", ""}, nil, nil); slices.Contains(got, "h1") {
		t.Errorf("ssh -p <TAB> = %q, want a pending value for -p", got)
	}
}
