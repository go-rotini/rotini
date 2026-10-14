package rotini

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ── wiring: a program missing a feature its tree needs ──────────────────────
//
// Each case builds a program with every feature featureNeeds finds for its fixture except the
// one under test, exercises that feature, and expects a *WiringError naming it: never a silent
// skip, never a different failure.

// wiringCase is one matrix entry for a feature a program needs but does not wire.
type wiringCase struct {
	id      string
	feature string // the missing feature, as Feature.String spells it
	ctor    string // its constructor, as the message names it
	def     Definition
	meta    InputSettings
	inputs  []reflect.Type
	argv    []string
	argv0   string
	// fault reports that the run records the error as a fault (its exit code is 1 and the
	// error is CategoryInternal); otherwise a handler's Inputs call returns it.
	fault bool
}

// wpInputs reads a configuration file whose path a flag supplies.
type wpInputs struct {
	App struct {
		Flags struct {
			Config string `rotini:"config"`
		}
		Arguments struct{}
		Config    struct {
			Name string `rotini:"name" recon:"name"`
		}
	}
}

func wiringConformanceCases() []wiringCase {
	root := func() Definition { return Definition{Name: "app", Handler: "App"} }
	plugins := root()
	plugins.Plugins = []PluginDef{{Name: "ctx", Binary: "app-ctx-not-installed"}}
	respFiles := root()
	respFiles.ResponseFiles = &ResponseFilesDef{Prefix: "@"}
	chdir := root()
	chdir.Flags = []FlagDef{{Name: "dir", Identifiers: []string{"-C"}, Type: "string", Role: roleChdir}}
	multicall := root()
	multicall.Multicall = &MulticallDef{}
	multicall.Commands = []CommandDef{{Name: "ls", Handler: "App"}}
	expand := root()
	expand.Flags = []FlagDef{{Name: "config", Identifiers: []string{"--config"}, Type: "string", Default: "~rotini-no-such-user/app.yaml"}}
	return []wiringCase{
		// WIRE-01: a declared plugin is dispatched.
		{id: "WIRE-01", feature: "plugins", ctor: "Plugins()", def: plugins, argv: []string{"ctx"}, fault: true},
		// WIRE-02: the program reads response files.
		{id: "WIRE-02", feature: "response-files", ctor: "ResponseFiles()", def: respFiles, fault: true},
		// WIRE-03: the root declares a chdir flag.
		{id: "WIRE-03", feature: "chdir", ctor: "Chdir()", def: chdir, argv: []string{"-C", "."}, fault: true},
		// WIRE-04: the program is multicall.
		{id: "WIRE-04", feature: "multicall", ctor: "Multicall()", def: multicall, argv0: "ls", fault: true},
		// WIRE-05: a config_source flag's declared default names another user's home.
		{
			id: "WIRE-05", feature: "path-expansion", ctor: "PathExpansion()", def: expand,
			meta: InputSettings{ConfigFiles: []ConfigFile{{
				Name: "app", Format: "yaml", PathFrom: &PathFromDef{Flag: "config"},
			}}},
			inputs: typesOf(wpInputs{}),
		},
	}
}

// wiringHandler runs read, if set, and halts with its error.
type wiringHandler struct {
	NoHooks
	read func(rtx *Context) error
}

func (h wiringHandler) Run(_ context.Context, rtx *Context) {
	if h.read == nil {
		return
	}
	if err := h.read(rtx); err != nil {
		rtx.HaltWith(err)
	}
}

// featuresWithout is featureNeeds less the feature named without, which must be among them.
func featuresWithout(t *testing.T, without string, def Definition, meta InputSettings, inputs ...reflect.Type) []Feature {
	t.Helper()
	all := featureNeeds(def, meta, inputs...)
	out := slices.DeleteFunc(slices.Clone(all), func(f Feature) bool { return f.String() == without })
	if len(out) == len(all) {
		t.Fatalf("featureNeeds = %v does not include %s; the case does not exercise it", all, without)
	}
	return out
}

func TestConformance_WiringMatrix(t *testing.T) {
	for _, c := range wiringConformanceCases() {
		t.Run(c.id, func(t *testing.T) {
			var got error
			var h wiringHandler
			if len(c.inputs) > 0 {
				h.read = func(rtx *Context) error {
					_, got = rtx.Inputs[wpInputs]()
					return got
				}
			}
			var stderr strings.Builder
			build := func(fs []Feature) *Program {
				p := NewWiredProgram(c.def, func(string) (Handler, bool) { return h, true }, fs...).
					WithInputSettings(c.meta).
					WithoutSignalHandling().
					WithStdout(io.Discard).
					WithStderr(&stderr)
				if c.argv0 != "" {
					p = p.WithArgv0(c.argv0)
				}
				return p
			}
			// With the feature wired, the same run reports no wiring error.
			if _, err := build(featureNeeds(c.def, c.meta, c.inputs...)).Run(c.argv); errors.As(err, new(*WiringError)) || errors.As(got, new(*WiringError)) {
				t.Fatalf("fully wired run: %v %v", err, got)
			}
			got = nil
			stderr.Reset()

			code, err := build(featuresWithout(t, c.feature, c.def, c.meta, c.inputs...)).Run(c.argv)
			if code != 1 {
				t.Errorf("exit %d, want 1; stderr: %s", code, stderr.String())
			}
			if !c.fault {
				err = got
			}
			we, ok := errors.AsType[*WiringError](err)
			if !ok {
				t.Fatalf("error %v (%T) is not a *WiringError; stderr: %s", err, err, stderr.String())
			}
			if we.Feature != c.feature {
				t.Errorf("Feature = %q, want %q", we.Feature, c.feature)
			}
			for _, want := range []string{"does not wire " + c.feature, "rotini." + c.ctor} {
				if !strings.Contains(we.Error(), want) {
					t.Errorf("message %q lacks %q", we.Error(), want)
				}
			}
			if !errors.Is(err, ErrInternal) {
				t.Errorf("error does not match ErrInternal: %v", err)
			}
			if c.fault {
				if cat := CategoryOf(err); cat != CategoryInternal {
					t.Errorf("CategoryOf = %v, want internal", cat)
				}
				if !strings.Contains(stderr.String(), "does not wire "+c.feature) {
					t.Errorf("stderr %q lacks the wiring error", stderr.String())
				}
			}
		})
	}
}

// TestConformance_WiringBaseline pins that a program whose tree needs no feature runs the same
// with none wired as with every feature: help, a dispatch reading inputs, and completion.
func TestConformance_WiringBaseline(t *testing.T) {
	def := guardDefinition()
	if fs := featureNeeds(def, InputSettings{}, reflect.TypeFor[guardRootInputs](), reflect.TypeFor[guardBuildInputs]()); len(fs) != 0 {
		t.Fatalf("featureNeeds = %v, want none", fs)
	}
	run := func(legacy bool, argv []string) (int, string) {
		var out strings.Builder
		p := guardProgram(legacy).WithStdout(&out).WithStderr(&out)
		code, _ := p.Run(argv)
		return code, out.String()
	}
	for _, argv := range [][]string{
		{"--help"},
		{"build", "--name", "x", "--count", "2", "--timeout", "5s", "--tag", "a", "tgt"},
		{"build", "--count", "nope", "tgt"},
		{"__complete", "bu"},
		{"__complete", "build", "--mode", ""},
	} {
		wc, wout := run(false, argv)
		lc, lout := run(true, argv)
		if wc != lc || wout != lout {
			t.Errorf("%q: wired = %d %q, legacy = %d %q", argv, wc, wout, lc, lout)
		}
	}
}
