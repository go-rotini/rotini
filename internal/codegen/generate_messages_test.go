package codegen

import (
	"path/filepath"
	"strings"
	"testing"
)

const messagesSpec = `version: 0.0.0
command:
  name: app
  flags:
    - name: replicas
      summary: how many instances
      identifiers: [-r, --replicas]
      schema: { type: int }
    - name: verbose
      summary: say more
      identifiers: [--verbose]
      schema: { type: bool }
    - name: secret
      summary: hidden
      identifiers: [--secret]
      hidden: true
      schema: { type: string }
  arguments:
    - name: service
      summary: which service
      schema: { type: string, complete: { kind: none, message: a service name from deploy.yaml } }
    - name: target
      summary: where it goes
      schema: { type: string, placeholder: HOST }
    - name: note
      schema: { type: string }
`

// messagesProgram resolves messagesSpec under a completion feature with the given messages
// mode and env ("" for none).
func messagesProgram(t *testing.T, mode, env string) *program {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, messagesSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.conf = &Conf{Generate: &GenerateConfig{Features: []Feature{{Type: "completion", Enabled: true, Messages: mode, MessagesEnv: env}}}}
	gp.resolveCompletionMessages()
	return gp
}

func TestCompletionMessages_resolved(t *testing.T) {
	message := func(gp *program, kind, name string) string {
		for _, f := range gp.rootInputs.Flags {
			if kind == "flag" && f.Name == name && f.Schema.Complete != nil {
				return f.Schema.Complete.Message
			}
		}
		for _, a := range gp.rootInputs.Arguments {
			if kind == "arg" && a.Name == name && a.Schema.Complete != nil {
				return a.Schema.Complete.Message
			}
		}
		return ""
	}
	tests := []struct {
		mode, kind, name, want string
	}{
		{"all", "flag", "replicas", "--replicas int: how many instances"},
		{"all", "flag", "verbose", ""}, // takes no value
		{"all", "flag", "secret", ""},  // hidden
		{"all", "arg", "service", "a service name from deploy.yaml"},
		{"all", "arg", "target", "HOST: where it goes"},
		{"all", "arg", "note", ""}, // no summary
		{"declared", "flag", "replicas", ""},
		{"declared", "arg", "service", "a service name from deploy.yaml"},
		{"", "arg", "service", ""}, // off
	}
	for _, tt := range tests {
		t.Run(tt.mode+" "+tt.name, func(t *testing.T) {
			if got := message(messagesProgram(t, tt.mode, ""), tt.kind, tt.name); got != tt.want {
				t.Errorf("message = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompletionMessages_literals(t *testing.T) {
	off := messagesProgram(t, "", "")
	if lit := renderDefinition(off); strings.Contains(lit, "Message:") || strings.Contains(lit, "CompletionMessages") {
		t.Errorf("messages off still emits them:\n%s", lit)
	}
	on := messagesProgram(t, "all", "APP_COMPLETION_MESSAGES")
	lit := renderDefinition(on)
	for _, want := range []string{
		`Complete: rotini.Completion{Kind: "none", Message: "a service name from deploy.yaml"}`,
		`Complete: rotini.Completion{Message: "--replicas int: how many instances"}`,
		`CompletionMessages: &rotini.CompletionMessagesDef{Env: "APP_COMPLETION_MESSAGES"}`,
	} {
		if !strings.Contains(lit, want) {
			t.Errorf("definition is missing %s\n%s", want, lit)
		}
	}
}

func TestCompletionMessages_switchIsListed(t *testing.T) {
	gp := messagesProgram(t, "all", "APP_COMPLETION_MESSAGES")
	root := flattenFeature(gp, manFeatureDesc)[0].data
	if len(root.Environment) == 0 || root.Environment[len(root.Environment)-1].Var != "APP_COMPLETION_MESSAGES" {
		t.Errorf("root man ENVIRONMENT = %+v, want APP_COMPLETION_MESSAGES listed", root.Environment)
	}
	if help := flattenFeature(gp, helpFeatureDesc)[0].data; len(help.Environment) != 0 {
		t.Errorf("help lists %+v; the switch belongs in the man page only", help.Environment)
	}
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"messages_env": "APP_COMPLETION_MESSAGES"`) {
		t.Errorf("contract is missing messages_env:\n%s", doc)
	}
	script, err := completionScript("app", "zsh", "APP_COMPLETION_MESSAGES")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(script, "#compdef app\n# Set APP_COMPLETION_MESSAGES=off to hide completion messages.\n") {
		t.Errorf("zsh script header = %q", script[:120])
	}
}

func TestLintUnshownMessages(t *testing.T) {
	spec := decodeSpecYAML(t, messagesSpec)
	on := &Conf{Generate: &GenerateConfig{Features: []Feature{{Type: "completion", Enabled: true, Messages: "declared"}}}}
	if got := lintUnshownMessages(spec, on); len(got) != 0 {
		t.Errorf("messages on: %v, want nothing", got)
	}
	for name, conf := range map[string]*Conf{
		"no completion feature": {},
		"completion off":        {Generate: &GenerateConfig{Features: []Feature{{Type: "completion", Messages: "all"}}}},
		"no messages":           {Generate: &GenerateConfig{Features: []Feature{{Type: "completion", Enabled: true}}}},
	} {
		t.Run(name, func(t *testing.T) {
			got := lintUnshownMessages(spec, conf)
			if _, warns := splitProblems(got); len(got) != 1 || len(warns) != 1 || !strings.Contains(got[0].Error(), `argument "service": sets `+"`complete.message`") {
				t.Errorf("problems = %v, want one warning for <service>", got)
			}
		})
	}
}
