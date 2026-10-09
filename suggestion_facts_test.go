package rotini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/jsonschema"
)

// TestSuggestionFacts_inputError pins the facts on an env-only and a config-only enum
// violation, and their absence on any other input failure.
func TestSuggestionFacts_inputError(t *testing.T) {
	bind := func(t *testing.T, meta InputSettings) error {
		t.Helper()
		var in tbChannelEnumInputs
		return NewInputReader(meta).Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	}
	check := func(t *testing.T, err error, token string, candidates []string) {
		t.Helper()
		var ie *InputError
		if !errors.As(err, &ie) || ie.Token != token || !slices.Equal(ie.Candidates, candidates) {
			t.Fatalf("err = %#v, want an *InputError with Token %q and Candidates %v", err, token, candidates)
		}
		gotToken, gotCandidates, ok := SuggestionFacts(err)
		if !ok || gotToken != token || !slices.Equal(gotCandidates, candidates) {
			t.Fatalf("SuggestionFacts = %q, %v, %v; want %q, %v, true", gotToken, gotCandidates, ok, token, candidates)
		}
	}
	t.Run("env", func(t *testing.T) {
		t.Setenv("MODE", "fats")
		err := bind(t, InputSettings{})
		check(t, err, "fats", []string{"fast", "slow"})
		if got := NewSuggestor().For(err); len(got) == 0 || got[0] != "fast" {
			t.Errorf("For = %v, want fast first", got)
		}
	})
	t.Run("config", func(t *testing.T) {
		cfg := writeConfig(t, "tier: gold1\n")
		check(t, bind(t, InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Format: "yaml"}}}), "gold1", []string{"gold", "silver"})
	})
	t.Run("other failures carry none", func(t *testing.T) {
		err := usageBind(channelEnv, "PORT", "PORT is required", nil)
		if err.Token != "" || err.Candidates != nil {
			t.Fatalf("a non-enum *InputError carries facts: %#v", err)
		}
		if _, _, ok := SuggestionFacts(err); ok {
			t.Fatal("SuggestionFacts found facts on a non-enum *InputError")
		}
	})
	t.Run("a secret value is redacted and never offered", func(t *testing.T) {
		err := checkChannelEnum(channelEnv, "MODE", enumSet{values: []string{"fast", "slow"}}, []string{"hunter2"}, true)
		var ie *InputError
		if !errors.As(err, &ie) || ie.Token != "[redacted]" || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("err = %#v, want Token [redacted] and no value in the message", err)
		}
		if _, _, ok := SuggestionFacts(err); ok {
			t.Fatal("SuggestionFacts offered a redacted token")
		}
	})
}

// TestSuggestionFacts_pluginError pins the candidates on a mistyped discovered sub-command:
// the dispatching command's visible names, aliases and declared plugins.
func TestSuggestionFacts_pluginError(t *testing.T) {
	def := Definition{
		Name: "acme", Handler: "App",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"},
		Commands: []CommandDef{
			{Name: "generate", Handler: "Gen", Aliases: []string{"gen"}},
			{Name: "secret", Handler: "Secret", Hidden: true},
		},
		Plugins: []PluginDef{{Name: "deploy", Binary: "acme-deploy-xyz-absent"}},
	}

	p, _, _ := pluginProgram(def, []string{"gnerate"})
	_, err := p.Run(p.args)
	var pe *PluginError
	want := []string{"generate", "gen", "deploy"} // the hidden "secret" is not offered
	if !errors.As(err, &pe) || pe.Name != "gnerate" || !slices.Equal(pe.Candidates, want) {
		t.Fatalf("err = %#v, want a *PluginError for gnerate with Candidates %v", err, want)
	}
	if got := NewSuggestor().For(err); len(got) == 0 || got[0] != "generate" {
		t.Errorf("For = %v, want generate first", got)
	}

	// A declared plugin that is not installed is an install problem, not a typo.
	p, _, _ = pluginProgram(def, []string{"deploy"})
	_, err = p.Run(p.args)
	if !errors.As(err, &pe) || pe.Kind != PluginNotFound || pe.Candidates != nil {
		t.Fatalf("declared plugin: err = %#v, want PluginNotFound without Candidates", err)
	}
	if _, _, ok := SuggestionFacts(err); ok {
		t.Error("SuggestionFacts found facts on a declared plugin that is not installed")
	}
}

// TestSuggestionFacts_resolution pins the tree search: each type, wrapping, joins in order,
// and every way it declines.
func TestSuggestionFacts_resolution(t *testing.T) {
	parse := &ParseError{Kind: ParseKindUnknownFlag, Token: "--vebose", Candidates: []string{"--verbose"}}
	input := &InputError{Token: "fats", Candidates: []string{"fast"}, usage: true}
	plugin := &PluginError{Name: "gnerate", Candidates: []string{"generate"}, cat: CategoryUsage}

	found := map[string]struct {
		err   error
		token string
	}{
		"parse":                      {parse, "--vebose"},
		"input":                      {input, "fats"},
		"plugin":                     {plugin, "gnerate"},
		"wrapped":                    {fmt.Errorf("context: %w", input), "fats"},
		"usage-wrapped":              {UsageError(plugin), "gnerate"},
		"joined, first with facts":   {errors.Join(errors.New("plain"), input, parse), "fats"},
		"joined, factless one first": {errors.Join(&ParseError{Kind: ParseKindMissingRequired}, plugin), "gnerate"},
		"joined, redacted one first": {errors.Join(&ParseError{Token: "[redacted]", Candidates: []string{"a"}}, parse), "--vebose"},
	}
	for name, c := range found {
		t.Run(name, func(t *testing.T) {
			if token, _, ok := SuggestionFacts(c.err); !ok || token != c.token {
				t.Fatalf("SuggestionFacts = %q, %v; want %q, true", token, ok, c.token)
			}
		})
	}

	declined := map[string]error{
		"nil":              nil,
		"plain":            errors.New("plain"),
		"empty token":      &InputError{Candidates: []string{"a"}},
		"empty candidates": &PluginError{Name: "x"},
		"redacted":         &ParseError{Token: "[redacted]", Candidates: []string{"a"}},
	}
	for name, err := range declined {
		t.Run(name, func(t *testing.T) {
			if token, candidates, ok := SuggestionFacts(err); ok || token != "" || candidates != nil {
				t.Fatalf("SuggestionFacts = %q, %v, %v; want nothing", token, candidates, ok)
			}
		})
	}
}

// TestStructuredReporter_tokenFromFacts pins that the JSON token field also reports an env or
// config enum value and a mistyped plugin command.
func TestStructuredReporter_tokenFromFacts(t *testing.T) {
	def := Definition{Name: "acme", Handler: "App", PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"}, Commands: []CommandDef{{Name: "generate", Handler: "Gen"}}}
	p, _, errb := pluginProgram(def, []string{"gnerate"})
	p.WithReporter(StructuredReporter(func(*Context) bool { return true }))
	_, _ = p.Run(p.args)

	var line struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(errb.Bytes()), &line); err != nil {
		t.Fatalf("stderr is not one JSON line: %v\n%s", err, errb)
	}
	if line.Error["token"] != "gnerate" {
		t.Errorf("token = %v, want gnerate (line %v)", line.Error["token"], line.Error)
	}

	raw, err := os.ReadFile("schema-error.json")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := schema.Validate(bytes.TrimSpace(errb.Bytes())); err != nil || !res.Valid {
		t.Errorf("line does not match schema-error.json: %s (%v %+v)", errb, err, res)
	}
}

// TestSuggestionFacts_pluginFailuresThatAreNotTypos pins that a plugin that was found but timed
// out or could not start carries no candidates: nothing was mistyped.
func TestSuggestionFacts_pluginFailuresThatAreNotTypos(t *testing.T) {
	writeFakeBinary(t, "acme-slow", "#!/bin/sh\nsleep 5\n")
	writeFakeBinary(t, "acme-broken", "#!/no/such/interpreter\n")
	def := Definition{
		Name: "acme", Handler: "App",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "acme-"},
		Commands:        []CommandDef{{Name: "generate", Handler: "Gen"}},
		Plugins:         []PluginDef{{Name: "slow", Binary: "acme-slow", Timeout: 50 * time.Millisecond}},
	}
	for word, kind := range map[string]PluginErrorKind{"slow": PluginTimeout, "broken": PluginStartFailed} {
		p, _, _ := pluginProgram(def, []string{word})
		_, err := p.Run(p.args)
		var pe *PluginError
		if !errors.As(err, &pe) || pe.Kind != kind || pe.Candidates != nil {
			t.Errorf("%s: err = %#v, want a %v *PluginError without candidates", word, err, kind)
		}
		if _, _, ok := SuggestionFacts(err); ok {
			t.Errorf("%s: SuggestionFacts found facts", word)
		}
	}
}

func FuzzSuggestionFacts(f *testing.F) {
	f.Add("--vebose", "--verbose,--version", 0)
	f.Add("[redacted]", "a", 1)
	f.Add("", "", 2)
	f.Fuzz(func(t *testing.T, token, list string, shape int) {
		candidates := strings.Split(list, ",")
		errs := []error{
			&ParseError{Token: token, Candidates: candidates},
			&InputError{Token: token, Candidates: candidates},
			&PluginError{Name: token, Candidates: candidates},
		}
		err := errs[((shape%3)+3)%3]
		for range ((shape % 4) + 4) % 4 {
			err = errors.Join(errors.New("x"), fmt.Errorf("wrap: %w", err))
		}
		got, gotCandidates, ok := SuggestionFacts(err)
		if ok && (got != token || got == "[redacted]" || len(gotCandidates) == 0) {
			t.Fatalf("SuggestionFacts = %q, %v for token %q", got, gotCandidates, token)
		}
		_ = NewSuggestor().For(err)
	})
}
