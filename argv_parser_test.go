package rotini

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// kvParser reads `name=value` words as flags and every other word after the command path as an
// argument: `app deploy replicas=3 region=eu web`.
type kvParser struct{}

func (kvParser) ParseArgv(rtx *Context) (ArgvValues, error) {
	var out ArgvValues
	for _, w := range rtx.Argv[len(rtx.CommandChain())-1:] {
		if name, value, ok := strings.Cut(w, "="); ok {
			out.Flags = append(out.Flags, ArgvFlag{Name: name, Value: value, Typed: name})
			continue
		}
		out.Args = append(out.Args, w)
	}
	return out, nil
}

// failingParser returns err from every parse.
type failingParser struct{ err error }

func (p failingParser) ParseArgv(*Context) (ArgvValues, error) { return ArgvValues{}, p.err }

type kvRootCmd struct {
	Flags struct {
		Help bool `rotini:"help"`
	}
	Arguments struct{}
}

type kvDeployCmd struct {
	Flags struct {
		Replicas int      `rotini:"replicas"`
		Region   string   `rotini:"region" recon:"deploy.region" env:"DEPLOY_REGION"`
		Verbose  int      `rotini:"verbose"`
		Target   string   `rotini:"target"`
		Tags     []string `rotini:"tag"`
	}
	Arguments struct {
		Service string `rotini:"service"`
	}
}

type kvInputs struct {
	App       kvRootCmd
	AppDeploy kvDeployCmd
}

func kvDef() Definition {
	one := 1.0
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "help", Identifiers: []string{"-h", "--help"}, Type: "bool", ShortCircuit: true}},
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy", Inputs: reflect.TypeFor[kvInputs](),
			Flags: []FlagDef{
				{Name: "replicas", Identifiers: []string{"-r", "--replicas"}, Type: "int", Minimum: &one, Default: "1"},
				{Name: "region", Identifiers: []string{"--region"}, Type: "string", Enum: []string{"eu", "us"}, Default: "us"},
				{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "count"},
				{Name: "target", Identifiers: []string{"--target"}, Type: "string", Required: true},
				{Name: "tag", Identifiers: []string{"--tag"}, Type: "[]string", Separator: ","},
			},
			Arguments: []ArgDef{{Name: "service", Type: "string"}},
		}},
	}
}

func kvContext(argv ...string) *Context {
	return NewContextFor(kvDef(), append([]string{"deploy"}, argv...)).WithParser(kvParser{}).WithEnviron(nil)
}

func TestArgvParser_readsTheAuthorsSyntax(t *testing.T) {
	t.Parallel()
	in, err := kvContext("replicas=3", "region=eu", "target=prod", "verbose=2", "tag=a,b", "tag=c", "web").Inputs[kvInputs]()
	if err != nil {
		t.Fatal(err)
	}
	f := in.AppDeploy.Flags
	if f.Replicas != 3 || f.Region != "eu" || f.Target != "prod" || f.Verbose != 2 || !slices.Equal(f.Tags, []string{"a", "b", "c"}) {
		t.Errorf("flags = %+v", f)
	}
	if in.AppDeploy.Arguments.Service != "web" {
		t.Errorf("service = %q, want web", in.AppDeploy.Arguments.Service)
	}
}

// The custom parser supplies only the argv layer: env, config and defaults fill the rest, in
// rotini's precedence.
func TestArgvParser_keepsPrecedence(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, "deploy:\n  region: eu\n")
	files := InputSettings{ConfigFiles: []ConfigFileDef{{Name: "app", Path: cfg, Format: "yaml"}}}
	cases := []struct {
		name   string
		argv   []string
		env    []string
		meta   InputSettings
		want   string
		winner string
	}{
		{"argv beats env and config", []string{"region=us"}, []string{"DEPLOY_REGION=eu"}, files, "us", "argv"},
		{"env beats config", nil, []string{"DEPLOY_REGION=us"}, files, "us", "env"},
		{"config beats the default", nil, nil, files, "eu", "config"},
		{"the default when nothing is given", nil, nil, InputSettings{}, "us", "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			newRtx := func() *Context {
				return kvContext(append([]string{"target=x"}, c.argv...)...).WithEnviron(c.env).WithInputSettings(c.meta)
			}
			in, err := newRtx().Inputs[kvInputs]()
			if err != nil || in.AppDeploy.Flags.Region != c.want {
				t.Errorf("Inputs: region %q, %v; want %q", in.AppDeploy.Flags.Region, err, c.want)
			}
			rin, report, err := newRtx().InputsWithReport[kvInputs]()
			if err != nil || rin.AppDeploy.Flags.Region != c.want {
				t.Errorf("InputsWithReport: region %q, %v; want %q", rin.AppDeploy.Flags.Region, err, c.want)
			}
			if win, ok := report.Winner("AppDeploy.Flags.Region"); !ok || win.Layer != c.winner {
				t.Errorf("winner = %+v, %v; want layer %q", win, ok, c.winner)
			}
		})
	}
}

func TestArgvParser_namesTheCommandLineAsTheOrigin(t *testing.T) {
	t.Parallel()
	_, report, err := kvContext("target=x", "replicas=2", "web").InputsWithReport[kvInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if win, _ := report.Winner("AppDeploy.Flags.Replicas"); win.Layer != "argv" || win.Origin != "argv:replicas" || win.Raw != "2" {
		t.Errorf("replicas winner = %+v, want argv:replicas = 2", win)
	}
	if win, _ := report.Winner("AppDeploy.Arguments.Service"); win.Layer != "argv" || win.Origin != "argv:<service>" {
		t.Errorf("service winner = %+v, want argv:<service>", win)
	}
	layer, err := kvContext("target=x").ArgvInputs[kvInputs]()
	if err != nil || layer.Values.AppDeploy.Flags.Target != "x" || len(layer.Set) != 1 {
		t.Errorf("ArgvInputs = %+v, %v; want only target=x", layer, err)
	}
}

func TestArgvParser_shortCircuitWaivesRequirements(t *testing.T) {
	t.Parallel()
	for _, word := range []string{"help=true", "help=", "--help="} {
		in, err := kvContext(word).Inputs[kvInputs]()
		if err != nil || !in.App.Flags.Help {
			t.Errorf("%s: help %v, %v; want the missing target waived", word, in.App.Flags.Help, err)
		}
	}
	if _, err := kvContext("help=false").Inputs[kvInputs](); err == nil {
		t.Error("help=false waived the required target")
	}
}

func TestArgvParser_errorKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		argv []string
		kind ParseKind
		msg  string
	}{
		{[]string{"target=x", "colour=red"}, ParseKindUnknownFlag, `app deploy: unknown flag "colour"`},
		{[]string{"target=x", "replicas=many"}, ParseKindInvalidValue, "replicas"},
		{[]string{"target=x", "replicas=0"}, ParseKindConstraintViolation, "app deploy: replicas must be >= 1 (got 0)"},
		{[]string{"target=x", "region=mars"}, ParseKindEnumViolation, "region"},
		{[]string{"replicas=2"}, ParseKindMissingRequired, "--target"},
		{[]string{"target=x", "verbose=-1"}, ParseKindInvalidValue, `verbose counts occurrences; "-1" is not a count`},
		{[]string{"target=x", "web", "api"}, ParseKindTooManyArguments, "accepts at most 1 argument (got 2)"},
	}
	for _, c := range cases {
		_, err := kvContext(c.argv...).Inputs[kvInputs]()
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Kind != c.kind || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%v: %v (%#v); want a %s error containing %q", c.argv, err, pe, c.kind, c.msg)
		}
	}
}

type sentinelParseFailure struct{}

func (sentinelParseFailure) Error() string { return "words must be key=value" }

func TestArgvParser_errorsAreReportedAsTheyAre(t *testing.T) {
	t.Parallel()
	run := func(p ArgvParser) error {
		_, err := NewContextFor(kvDef(), []string{"deploy"}).WithParser(p).Inputs[kvInputs]()
		return err
	}

	err := run(failingParser{sentinelParseFailure{}})
	if CategoryOf(err) != CategoryUsage || !errors.As(err, new(sentinelParseFailure)) || err.Error() != "app deploy: words must be key=value" {
		t.Errorf("uncategorized: %v (category %v); want a usage error naming the path that reaches the cause", err, CategoryOf(err))
	}

	internal := InternalError(errors.New("parser misconfigured"))
	if err := run(failingParser{internal}); CategoryOf(err) != CategoryInternal || err.Error() != "parser misconfigured" {
		t.Errorf("categorized: %v; want it reported as it is", err)
	}

	own := &ParseError{Kind: ParseKindNeedsValue, Msg: "replicas needs a value", Flag: "replicas"}
	var pe *ParseError
	if err := run(failingParser{own}); !errors.As(err, &pe) || pe.Kind != ParseKindNeedsValue || err.Error() != "app deploy: replicas needs a value" {
		t.Errorf("*ParseError: %v; want its kind kept and the path added", err)
	}
}

func TestArgvParser_nilRestoresRotinisParser(t *testing.T) {
	t.Parallel()
	rtx := NewContextFor(kvDef(), []string{"deploy", "--target", "x", "-r", "4"}).WithParser(kvParser{}).WithParser(nil)
	if _, ok := rtx.Parser().(*Parser); !ok {
		t.Errorf("Parser() = %T, want rotini's *Parser", rtx.Parser())
	}
	in, err := rtx.Inputs[kvInputs]()
	if err != nil || in.AppDeploy.Flags.Replicas != 4 {
		t.Errorf("replicas %d, %v; want rotini's grammar to read -r 4", in.AppDeploy.Flags.Replicas, err)
	}
	var none *Parser
	if p, ok := NewProgram(kvDef(), nil).WithParser(none).newRunContext().Parser().(*Parser); !ok || p == nil {
		t.Errorf("a nil *Parser left Parser() = %v, want rotini's own", p)
	}
}

// Parser.ParseArgv reports rotini's reading as values, raw; a wrapper that returns them reads
// exactly as rotini does.
func TestParser_ParseArgv(t *testing.T) {
	t.Parallel()
	argv := []string{"deploy", "--target", "x", "-vv", "--region=eu", "--tag", "a,b", "--help=false", "web"}
	rtx := NewContextFor(kvDef(), argv)
	got, err := NewParser().ParseArgv(rtx)
	if err != nil {
		t.Fatal(err)
	}
	want := ArgvValues{
		Flags: []ArgvFlag{
			{Name: "target", Value: "x", Typed: "--target"},
			{Name: "verbose", Value: "1", Typed: "-v"},
			{Name: "verbose", Value: "1", Typed: "-v"},
			{Name: "region", Value: "eu", Typed: "--region"},
			{Name: "tag", Value: "a,b", Typed: "--tag"},
			{Name: "help", Value: "false", Typed: "--help"},
		},
		Args: []string{"web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseArgv =\n%+v\nwant\n%+v", got, want)
	}

	native, err := NewContextFor(kvDef(), argv).Inputs[kvInputs]()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := NewContextFor(kvDef(), argv).WithParser(wrapParser{}).Inputs[kvInputs]()
	if err != nil || !reflect.DeepEqual(native, wrapped) {
		t.Errorf("wrapped = %+v, %v; want %+v", wrapped, err, native)
	}

	if _, err := NewParser().ParseArgv(NewContextFor(kvDef(), []string{"deploy", "--nope"})); err == nil {
		t.Error("ParseArgv accepted an unknown flag")
	}
}

// wrapParser reads with rotini's grammar and returns what it read.
type wrapParser struct{}

func (wrapParser) ParseArgv(rtx *Context) (ArgvValues, error) { return NewParser().ParseArgv(rtx) }

func TestArgvParser_composedChild(t *testing.T) {
	t.Parallel()
	var got fMidOwn
	var gotErr error
	p := NewProgram(fDef(), fProg{inMid: func(rtx *Context) { got, gotErr = rtx.Inputs[fMidOwn]() }}).
		WithParser(kvParser{}).WithStdout(io.Discard).WithStderr(io.Discard)
	if _, err := p.Run([]string{"mid", "leaf", "midonly=true"}); err != nil {
		t.Fatal(err)
	}
	if gotErr != nil || !got.Mid.Flags.MidOnly {
		t.Errorf("composed child read %+v, %v; want mid's own midonly", got, gotErr)
	}
}

func TestArgvParser_concurrentRuns(t *testing.T) {
	t.Parallel()
	type result struct {
		replicas int
		err      error
	}
	results := make([]result, 32)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			rtx := NewContextFor(kvDef(), []string{"deploy", "target=x", fmt.Sprintf("replicas=%d", i+1)}).WithParser(kvParser{}).WithEnviron(nil)
			in, err := rtx.Inputs[kvInputs]()
			results[i] = result{in.AppDeploy.Flags.Replicas, err}
		})
	}
	wg.Wait()
	for i, r := range results {
		if r.err != nil || r.replicas != i+1 {
			t.Errorf("run %d: replicas %d, %v", i, r.replicas, r.err)
		}
	}
}

func TestArgvParser_readsOnlyTheCommandLineFacts(t *testing.T) {
	t.Parallel()
	rtx := kvContext("target=x", "--", "web")
	if n, ok := rtx.DashIndex(); ok || n != 0 {
		t.Errorf("DashIndex = %d, %v; a custom parser reports no --", n, ok)
	}
	if err := kvContext("help=true").UnknownCommand(); err != nil {
		t.Errorf("UnknownCommand = %v, want nil", err)
	}
}

// FileInputs is the deprecated name of ConfigInputs: the same layer, named "config".
func TestFileInputs_forwardsToConfigInputs(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, "deploy:\n  region: eu\n")
	meta := InputSettings{ConfigFiles: []ConfigFileDef{{Name: "app", Path: cfg, Format: "yaml"}}}
	config, err := kvContext().WithInputSettings(meta).ConfigInputs[kvInputs]()
	if err != nil {
		t.Fatal(err)
	}
	files, err := kvContext().WithInputSettings(meta).ConfigInputs[kvInputs]()
	if err != nil || !reflect.DeepEqual(files.Values, config.Values) || !reflect.DeepEqual(files.Set, config.Set) || files.Name != "config" || files.Values.AppDeploy.Flags.Region != "eu" {
		t.Errorf("FileInputs = %+v, %v; want the ConfigInputs layer %+v", files.Values, err, config.Values)
	}
}
