package rotini

import (
	"testing"
)

// fallbackHandlers' completers answer cands and, when fallback is set, choose it for the request.
type fallbackHandlers struct {
	cands    []string
	fallback *Completion
}

type fallbackDeploy struct {
	dynStub
	h fallbackHandlers
}

func (f fallbackDeploy) answer(rtx *Context) []string {
	if f.h.fallback != nil {
		rtx.SetCompletionFallback(*f.h.fallback)
	}
	return f.h.cands
}

func (f fallbackDeploy) CompleteArgValue(rtx *Context, _, _ string) []string {
	return f.answer(rtx)
}

func (f fallbackDeploy) CompleteFlagValue(rtx *Context, _, _ string) []string {
	return f.answer(rtx)
}

func (h fallbackHandlers) AppDeploy() Handler { return fallbackDeploy{h: h} }

// fallbackDef declares hint on deploy's argument and its --target flag.
func fallbackDef(hint Completion) Definition {
	return Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy",
			Flags:     []FlagDef{{Name: "target", Identifiers: []string{"--target"}, Type: "string", Complete: hint}},
			Arguments: []ArgDef{{Name: "service", Type: "string", Complete: hint}},
		}},
	}
}

// TestSetCompletionFallback_protocol pins the directive line a completer's per-request fallback
// writes, for each kind, for an argument and a flag value.
func TestSetCompletionFallback_protocol(t *testing.T) {
	static := Completion{Kind: "none"}
	cases := []struct {
		name     string
		fallback *Completion
		want     string
	}{
		{"unset keeps the declared hint", nil, ":rotini:none\n"},
		{"file", &Completion{Kind: "file"}, ":rotini:file\n"},
		{"extensions", &Completion{Kind: "file", Extensions: []string{"yaml", "yml"}}, ":rotini:file yaml yml\n"},
		{"directory", &Completion{Kind: "directory"}, ":rotini:directory\n"},
		{"none", &Completion{Kind: "none"}, ":rotini:none\n"},
		{"executable", &Completion{Kind: "executable"}, ":rotini:executable\n"},
		{"zero is the shell's default", &Completion{}, ""},
	}
	for _, c := range cases {
		h := fallbackHandlers{cands: []string{}, fallback: c.fallback}
		for _, words := range [][]string{{"deploy", ""}, {"deploy", "--target", ""}, {"deploy", "--target="}} {
			if got := completeIn(t, nil, fallbackDef(static), h, words...); got != c.want {
				t.Errorf("%s %q: %q, want %q", c.name, words, got, c.want)
			}
		}
	}

	// Without a declared hint, an unset fallback writes no directive.
	if got := completeIn(t, nil, fallbackDef(Completion{}), fallbackHandlers{cands: []string{}}, "deploy", ""); got != "" {
		t.Errorf("no hint, no fallback: %q, want nothing", got)
	}
	// With candidates, the fallback rides after them, as a declared hint does.
	h := fallbackHandlers{cands: []string{"web", "api"}, fallback: &Completion{Kind: "directory"}}
	if got := completeIn(t, nil, fallbackDef(static), h, "deploy", ""); got != "web\napi\n:rotini:option keep-order\n:rotini:directory\n" {
		t.Errorf("candidates and a fallback: %q", got)
	}
	// The declared message stays; the fallback's own is not used.
	msgDef := fallbackDef(Completion{Kind: "none", Message: "a service name"})
	msgDef.CompletionMessages = &CompletionMessagesDef{}
	h = fallbackHandlers{cands: []string{}, fallback: &Completion{Kind: "file", Message: "ignored"}}
	if got := completeIn(t, nil, msgDef, h, "deploy", ""); got != ":rotini:message a service name\n:rotini:file\n" {
		t.Errorf("message: %q", got)
	}

	// Outside a request it does nothing.
	newContext().SetCompletionFallback(Completion{Kind: "file"})
}

// TestSetCompletionFallback_likeDeclared pins that a fallback a completer chooses is written
// exactly as the same hint declared in the spec, in both built-in formats, with and without
// candidates.
func TestSetCompletionFallback_likeDeclared(t *testing.T) {
	kinds := []Completion{
		{},
		{Kind: "file"},
		{Kind: "file", Extensions: []string{"yaml", "yml"}},
		{Kind: "directory"},
		{Kind: "none"},
		{Kind: "host"},
	}
	for _, format := range []struct {
		name string
		f    CompletionFormat
	}{{"rotini", nil}, {"plugin", PluginCompletion}} {
		for _, k := range kinds {
			for _, cands := range [][]string{{}, {"web"}, {"web", "api"}} {
				declared := completeIn(t, format.f, fallbackDef(k), fallbackHandlers{cands: cands}, "deploy", "")
				fb := k
				chosen := completeIn(t, format.f, fallbackDef(Completion{Kind: "none"}), fallbackHandlers{cands: cands, fallback: &fb}, "deploy", "")
				if chosen != declared {
					t.Errorf("%s %+v %q: chosen %q, declared %q", format.name, k, cands, chosen, declared)
				}
			}
		}
	}
}
