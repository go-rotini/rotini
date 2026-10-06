package rotini

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

// msgDef is a program with completion messages on: a free-text argument with a static
// message, an enum flag with one, a flag and a hidden argument without.
func msgDef(env string) Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			{Name: "env", Identifiers: []string{"--env"}, Type: "string", Enum: []string{"dev", "prod"}, Complete: Completion{Kind: "none", Message: "--env string: which environment"}},
			{Name: "verbose", Identifiers: []string{"--verbose"}, Type: "bool"},
		},
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy",
			Arguments: []ArgDef{{Name: "service", Type: "string", Complete: Completion{Kind: "none", Message: "a service name from deploy.yaml"}}},
		}},
		CompletionMessages: &CompletionMessagesDef{Env: env},
	}
}

// msgHandlers' deploy completer adds what the test sets, and offers what it sets.
type msgHandlers struct {
	add   []string
	offer []string
}

type msgDeploy struct{ h *msgHandlers }

func (msgDeploy) Run(context.Context, *Context)              {}
func (msgDeploy) CascadingPreRun(context.Context, *Context)  {}
func (msgDeploy) PreRun(context.Context, *Context)           {}
func (msgDeploy) PostRun(context.Context, *Context)          {}
func (msgDeploy) CascadingPostRun(context.Context, *Context) {}
func (d msgDeploy) CompleteArgValue(rtx *Context, _, _ string) []string {
	for _, m := range d.h.add {
		rtx.AddCompletionMessage(m)
	}
	return d.h.offer
}

func (h *msgHandlers) AppDeploy() Handler { return msgDeploy{h} }

// messagesOf runs a completion and returns the result's messages.
func messagesOf(t *testing.T, p *Program, words ...string) []string {
	t.Helper()
	var got CompletionResult
	if _, err := p.Complete(words, func(_ io.Writer, r CompletionResult) error { got = r; return nil }); err != nil {
		t.Fatal(err)
	}
	return got.Messages
}

func TestCompletionMessages_static(t *testing.T) {
	p := NewProgram(msgDef(""), &msgHandlers{})
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"a free-text argument with nothing to offer", []string{"deploy", ""}, []string{"a service name from deploy.yaml"}},
		{"an enum filtered to nothing", []string{"--env", "prd"}, []string{"--env string: which environment"}},
		{"an enum with candidates shows none", []string{"--env", "d"}, nil},
		{"flag names never", []string{"--"}, nil},
		{"command names never", []string{"de"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := messagesOf(t, p, c.words...); !slices.Equal(got, c.want) {
				t.Errorf("Messages = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCompletionMessages_dynamic(t *testing.T) {
	h := &msgHandlers{add: []string{"showing 2 of 9;\ttype more", "\x1b[1mbold\x1b[0m\nsecond line"}, offer: []string{"api", "web"}}
	got := messagesOf(t, NewProgram(msgDef(""), h), "deploy", "")
	want := []string{"showing 2 of 9; type more", "bold second line"}
	if !slices.Equal(got, want) {
		t.Errorf("Messages = %q, want %q (in order, beside candidates, one plain line each)", got, want)
	}

	h = &msgHandlers{add: []string{"could not read deploy.yaml"}}
	if got := messagesOf(t, NewProgram(msgDef(""), h), "deploy", ""); !slices.Equal(got, []string{"could not read deploy.yaml"}) {
		t.Errorf("Messages = %q, want the added message in place of the static one", got)
	}

	// Outside a completion request, adding a message does nothing.
	rtx := NewContextFor(msgDef(""), nil)
	rtx.AddCompletionMessage("ignored")
	if rtx.completionMessages != nil {
		t.Error("AddCompletionMessage recorded outside completion")
	}
}

func TestCompletionMessages_switch(t *testing.T) {
	off := msgDef("")
	off.CompletionMessages = nil
	h := &msgHandlers{add: []string{"added"}}
	if got := messagesOf(t, NewProgram(off, h), "deploy", ""); got != nil {
		t.Errorf("feature off: Messages = %q, want none, and the added message dropped", got)
	}

	for _, v := range []string{"0", "false", "OFF", " Off "} {
		t.Setenv("APP_MESSAGES", v)
		if got := messagesOf(t, NewProgram(msgDef("APP_MESSAGES"), &msgHandlers{}), "deploy", ""); got != nil {
			t.Errorf("APP_MESSAGES=%q: Messages = %q, want none", v, got)
		}
	}
	for _, v := range []string{"", "1", "on", "anything"} {
		t.Setenv("APP_MESSAGES", v)
		if got := messagesOf(t, NewProgram(msgDef("APP_MESSAGES"), &msgHandlers{}), "deploy", ""); len(got) != 1 {
			t.Errorf("APP_MESSAGES=%q: Messages = %q, want the static message", v, got)
		}
	}

	t.Setenv("APP_MESSAGES", "off")
	p := NewProgram(msgDef("APP_MESSAGES"), &msgHandlers{}).WithCompletionMessages(func(*Context) bool { return true })
	if got := messagesOf(t, p, "deploy", ""); len(got) != 1 {
		t.Errorf("WithCompletionMessages(true) over APP_MESSAGES=off: Messages = %q, want shown", got)
	}
	p = NewProgram(msgDef(""), &msgHandlers{}).WithCompletionMessages(func(*Context) bool { return false })
	if got := messagesOf(t, p, "deploy", ""); got != nil {
		t.Errorf("WithCompletionMessages(false): Messages = %q, want none", got)
	}
	if got := messagesOf(t, p.WithCompletionMessages(nil), "deploy", ""); len(got) != 1 {
		t.Errorf("WithCompletionMessages(nil): Messages = %q, want the default restored", got)
	}
}

func TestCompletionMessages_formats(t *testing.T) {
	h := &msgHandlers{add: []string{"two of many"}, offer: []string{"api"}}
	got := completeIn(t, nil, msgDef(""), h, "deploy", "")
	if want := "api\n:rotini:message two of many\n:rotini:none\n"; got != want {
		t.Errorf("rotini format = %q, want %q (candidates, messages, directive)", got, want)
	}
	got = completeIn(t, PluginCompletion, msgDef(""), h, "deploy", "")
	if want := "api\n_activeHelp_ two of many\n:4\n"; got != want {
		t.Errorf("plugin format = %q, want %q", got, want)
	}

	t.Setenv("APP_MESSAGES", "off")
	got = completeIn(t, PluginCompletion, msgDef("APP_MESSAGES"), h, "deploy", "")
	if strings.Contains(got, "_activeHelp_") {
		t.Errorf("plugin format with messages switched off = %q", got)
	}
}

// TestCompletionMessages_noFeatureIsUnchanged pins that a program without the feature answers
// byte for byte as before.
func TestCompletionMessages_noFeatureIsUnchanged(t *testing.T) {
	def := msgDef("")
	def.CompletionMessages = nil
	var out bytes.Buffer
	p := NewProgram(def, &msgHandlers{add: []string{"x"}})
	p.stdout = &out
	if _, err := p.Complete([]string{"deploy", ""}, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != ":rotini:none\n" {
		t.Errorf("out = %q, want only the directive", out.String())
	}
}
