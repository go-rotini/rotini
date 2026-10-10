package rotini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// runFn is a handler whose Run calls fn; its other hooks do nothing.
type runFn struct {
	NoHooks
	fn func(ctx context.Context, rtx *Context)
}

func (h runFn) Run(ctx context.Context, rtx *Context) {
	if h.fn != nil {
		h.fn(ctx, rtx)
	}
}

// fnLookup finds each command's handler in fns by handler name; a missing name runs nothing.
func fnLookup(fns map[string]func(context.Context, *Context)) HandlerLookup {
	return func(name string) (Handler, bool) { return runFn{fn: fns[name]}, true }
}

type todoRootCmd struct {
	Flags struct {
		Verbose bool `rotini:"verbose"`
		Trace   bool `rotini:"trace"`
	}
	Arguments struct{}
}

type todoInputs struct{ Todo todoRootCmd }

type todoListInputs struct {
	Todo todoRootCmd
	List struct {
		Flags struct {
			Status string `rotini:"status"`
			Token  string `rotini:"token"`
		}
		Arguments struct{}
	}
}

// bindRoot and bindList bind the invoked command's argv and halt with what went wrong.
func bindRoot(_ context.Context, rtx *Context) {
	if _, err := rtx.Inputs[todoInputs](); err != nil {
		rtx.HaltWith(err)
	}
}

func bindList(_ context.Context, rtx *Context) {
	if _, err := rtx.Inputs[todoListInputs](); err != nil {
		rtx.HaltWith(err)
	}
}

// structuredLine runs argv under a StructuredReporter that is always on and returns the one
// error line it wrote, checked against schema-error.json.
func structuredLine(t *testing.T, p *Program, argv ...string) map[string]any {
	t.Helper()
	var errb bytes.Buffer
	p.WithStdout(&bytes.Buffer{}).WithStderr(&errb).WithoutSignalHandling().
		WithReporter(StructuredReporter(func(*Context) bool { return true }))
	_, _ = p.Run(argv)
	line := bytes.TrimSpace(errb.Bytes())

	raw, err := os.ReadFile("schema-error.json")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := schema.Validate(line); err != nil || !res.Valid {
		t.Errorf("line does not match schema-error.json: %s (%v %+v)", line, err, res)
	}
	var got struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("stderr is not one JSON line: %v\n%s", err, line)
	}
	return got.Error
}

func candidatesOf(body map[string]any) []string {
	raw, _ := body["candidates"].([]any)
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		s, _ := c.(string)
		out = append(out, s)
	}
	return out
}

func TestStructuredReporter_candidates(t *testing.T) {
	def := Definition{
		Name: "todo", Handler: "Todo",
		Flags: []FlagDef{
			{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"},
			{Name: "trace", Identifiers: []string{"--trace"}, Type: "bool", Hidden: true},
		},
		Commands: []CommandDef{
			{Name: "add", Handler: "Add"},
			{Name: "list", Handler: "List", Aliases: []string{"ls"}, Flags: []FlagDef{
				{Name: "status", Identifiers: []string{"--status"}, Type: "string", Enum: []string{"open", "done"}},
				{Name: "token", Identifiers: []string{"--token"}, Type: "string", Enum: []string{"a1", "b2"}, Secret: true},
			}},
			{Name: "purge", Handler: "Purge", Hidden: true},
		},
	}
	fns := map[string]func(context.Context, *Context){"Todo": bindRoot, "List": bindList}
	cases := []struct {
		name, token string
		argv        []string
		want        []string
	}{
		{"unknown command", "lst", []string{"lst"}, []string{"add", "list", "ls"}},
		{"unknown flag", "--vebose", []string{"--vebose"}, []string{"--verbose"}},
		{"enum value", "opn", []string{"list", "--status", "opn"}, []string{"open", "done"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := structuredLine(t, NewProgramFunc(def, fnLookup(fns)), c.argv...)
			if body["token"] != c.token || !slices.Equal(candidatesOf(body), c.want) {
				t.Errorf("token %v, candidates %v; want %q, %v (line %v)", body["token"], body["candidates"], c.token, c.want, body)
			}
		})
	}

	t.Run("a secret enum offers nothing", func(t *testing.T) {
		body := structuredLine(t, NewProgramFunc(def, fnLookup(fns)), "list", "--token", "zz")
		if _, has := body["candidates"]; has {
			t.Errorf("candidates on a secret input: %v", body)
		}
		if tok, _ := body["token"].(string); tok == "zz" {
			t.Errorf("the secret value leaked as the token: %v", body)
		}
	})

	t.Run("an error without facts has no candidates", func(t *testing.T) {
		fail := map[string]func(context.Context, *Context){"Todo": func(_ context.Context, rtx *Context) {
			rtx.HaltWith(errors.New("plain"))
		}}
		body := structuredLine(t, NewProgramFunc(def, fnLookup(fail)))
		if _, has := body["candidates"]; has {
			t.Errorf("candidates without a token: %v", body)
		}
	})
}

// An env or config enum value (*InputError) and a mistyped plugin command (*PluginError) carry
// their candidates too.
func TestStructuredReporter_candidatesFromInputAndPluginErrors(t *testing.T) {
	_, stderr, _ := runReporter(wantsJSON, []string{"list", "--json"}, Outcome{Errors: []error{
		&InputError{Token: "fats", Candidates: []string{"fast", "slow"}, usage: true},
	}}, 0)
	var line struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &line); err != nil {
		t.Fatal(err)
	}
	if line.Error["token"] != "fats" || !slices.Equal(candidatesOf(line.Error), []string{"fast", "slow"}) {
		t.Errorf("line = %v", line.Error)
	}

	def := Definition{Name: "acme", Handler: "App", PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, Commands: []CommandDef{{Name: "generate", Handler: "Gen"}}}
	p, _, errb := pluginProgram(def, []string{"gnerate"})
	p.WithReporter(StructuredReporter(func(*Context) bool { return true }))
	_, _ = p.Run(p.args)
	if err := json.Unmarshal(bytes.TrimSpace(errb.Bytes()), &line); err != nil {
		t.Fatal(err)
	}
	if line.Error["token"] != "gnerate" || !slices.Equal(candidatesOf(line.Error), []string{"generate"}) {
		t.Errorf("plugin line = %v", line.Error)
	}
}

// A parse error's own token wins; candidates from another error in the tree, for another token,
// are left out rather than paired with the wrong word.
func TestStructuredReporter_candidatesMatchTheToken(t *testing.T) {
	err := errors.Join(
		&ParseError{Kind: ParseKindMissingRequired, Msg: "missing", Token: "x"},
		&InputError{Token: "fats", Candidates: []string{"fast"}, usage: true},
	)
	_, stderr, _ := runReporter(wantsJSON, []string{"list", "--json"}, Outcome{Errors: []error{err}}, 0)
	var line struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &line); err != nil {
		t.Fatal(err)
	}
	if line.Error["token"] != "x" {
		t.Errorf("token = %v, want the parse error's", line.Error["token"])
	}
	if _, has := line.Error["candidates"]; has {
		t.Errorf("candidates paired with another error's token: %v", line.Error)
	}
}
