package rotini

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type wireCompleter struct{ NoHooks }

func (wireCompleter) Run(context.Context, *Context) {}

func (wireCompleter) CompleteArgValue(*Context, string, string) []string {
	return []string{
		"svc-a\tfirst line\nsecond line",
		"evil\n:rotini:none",
		":rotini:option nospace",
		"_activeHelp_ fake",
		"bell\a",
		"ok\t\x1b[1mbold\x1b[0m\tand\ra tab",
	}
}

func completeWire(t *testing.T, def Definition, lookup HandlerLookup, format CompletionFormat, words ...string) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := NewProgramFunc(def, lookup).WithStdout(&out).Complete(words, format); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A dynamic candidate can't inject lines into the completion protocol: a value holding a
// control character or starting with a protocol marker is dropped, and a description keeps its
// first line, without styling or control characters.
func TestComplete_candidatesStayOneLine(t *testing.T) {
	def := Definition{Name: "app", Handler: "App", Arguments: []ArgDef{{Name: "svc"}}}
	lookup := func(string) (Handler, bool) { return wireCompleter{}, true }
	if got, want := completeWire(t, def, lookup, nil, ""), "svc-a\tfirst line\nok\tbold and a tab\n:rotini:option keep-order\n"; got != want {
		t.Errorf("rotini format:\n%q\nwant\n%q", got, want)
	}
	if got, want := completeWire(t, def, lookup, PluginCompletion, ""), "svc-a\tfirst line\nok\tbold and a tab\n:32\n"; got != want {
		t.Errorf("plugin format:\n%q\nwant\n%q", got, want)
	}
}

// An ancestor's local flag offers no value in the separate-word form, as in the inline one.
func TestComplete_localAncestorPendingValue(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{
			Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Local: true,
			Enum: []string{"fast", "slow"}, Complete: Completion{Kind: "directory"},
		}},
		Commands: []CommandDef{{Name: "sub", Handler: "Sub"}},
	}
	inline := completeWire(t, def, nil, nil, "sub", "--mode=")
	if sep := completeWire(t, def, nil, nil, "sub", "--mode", ""); sep != inline {
		t.Errorf("sub --mode <TAB> = %q, want what --mode= gives: %q", sep, inline)
	}
	if got := completeWire(t, def, nil, nil, "--mode", ""); !strings.Contains(got, "fast") {
		t.Errorf("--mode <TAB> on its own command = %q, want its values", got)
	}
}

// With plugin discovery on, a word starting with "-" is a positional, as dispatch reads it.
func TestComplete_discoveryDashWordIsPositional(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		PluginDiscovery: &PluginDiscoveryDef{Prefix: "app-"},
		PluginPath:      t.TempDir(),
		Arguments: []ArgDef{
			{Name: "n", Type: "int"},
			{Name: "mode", Type: "string", Enum: []string{"fast", "slow"}},
		},
	}
	if got := completeWire(t, def, nil, nil, "-5", ""); got != "fast\nslow\n" {
		t.Errorf("-5 <TAB> = %q, want the <mode> values", got)
	}
	if got := completeWire(t, def, nil, nil, "x/../y", ""); got != "fast\nslow\n" {
		t.Errorf("x/../y <TAB> = %q, want the <mode> values: a path is no plugin token", got)
	}
	if got := completeWire(t, def, nil, nil, "5", ""); got != "" {
		t.Errorf("5 <TAB> = %q, want nothing after a plugin token", got)
	}
}

// An answer matched without regard to case says so on its option line, so a shell that filters
// by the typed word keeps the candidates whose case differs; a case-sensitive one doesn't.
func TestComplete_ignoreCaseOption(t *testing.T) {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "mode", Identifiers: []string{"--mode"}, Type: "string", Enum: []string{"fast", "slow", "Safe"}, IgnoreCase: true},
			{Name: "strict", Identifiers: []string{"--strict"}, Type: "string", Enum: []string{"slow", "Safe"}},
		},
		Arguments: []ArgDef{{Name: "speed", Type: "string", Enum: []string{"slow", "Safe"}, IgnoreCase: true}},
		Commands:  []CommandDef{{Name: "sync", Handler: "Sync"}},
	}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"--mode", "S"}, "slow\nSafe\n:rotini:option keep-order ignore-case\n"},
		{[]string{"--mode=s"}, "--mode=slow\n--mode=Safe\n:rotini:option keep-order ignore-case\n"},
		{[]string{"--mode", "x"}, ":rotini:none\n"}, // the enum is the whole vocabulary
		{[]string{"--strict", "s"}, "slow\n"},
		{[]string{"S"}, "slow\nSafe\n:rotini:option keep-order ignore-case\n"},
	} {
		if got := completeWire(t, def, nil, nil, tc.words...); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.words, got, tc.want)
		}
	}
}
