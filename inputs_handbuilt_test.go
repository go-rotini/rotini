package rotini

import (
	"testing"
)

// A hand-built layer's fields are attributed to the layer's Name, carry their value's text, and
// a secret's text is redacted.
func TestMergeInputsWithReport_handBuiltProvenance(t *testing.T) {
	t.Parallel()
	type loginInputs struct {
		Acme  acRootCmd
		Login struct {
			Flags struct {
				Token string `rotini:"token"`
			}
			Arguments struct{}
		}
	}
	login := NewContextFor(acmeDef(), []string{"login"})
	argv, err := login.ArgvInputs[loginInputs]()
	must(t, err)
	var vault loginInputs
	vault.Login.Flags.Token = "hunter2"
	_, rep := MergeInputsWithReport(argv, InputLayer[loginInputs]{Name: "vault", Values: vault, Set: PresenceOf(vault)})
	if got, _ := rep.Winner("Login.Flags.Token"); got != (InputSource{Layer: "vault", Raw: redactedValue}) {
		t.Errorf("secret Winner = %+v, want vault and [redacted]", got)
	}

	deploy := NewContextFor(acmeDef(), []string{"deploy"})
	args, err := deploy.ArgvInputs[acDeployInputs]()
	must(t, err)
	var prompt acDeployInputs
	prompt.Deploy.Flags.Env = "staging"
	_, rep = MergeInputsWithReport(args, InputLayer[acDeployInputs]{Name: "prompt", Values: prompt, Set: PresenceOf(prompt)})
	if got, _ := rep.Winner("Deploy.Flags.Env"); got != (InputSource{Layer: "prompt", Raw: "staging"}) {
		t.Errorf("Winner = %+v, want prompt and staging", got)
	}
	if h := rep.History("Deploy.Flags.Env"); len(h) != 1 || h[0].Layer != "prompt" {
		t.Errorf("History = %+v, want the prompt layer", h)
	}

	// A source the Presence names, and Raw it gives, are kept; with no Name it stays "custom".
	named := Presence{"Deploy.Flags.Env": {Layer: "wizard", Raw: "asked"}}
	_, rep = MergeInputsWithReport(args, InputLayer[acDeployInputs]{Name: "prompt", Values: prompt, Set: named})
	if got, _ := rep.Winner("Deploy.Flags.Env"); got != (InputSource{Layer: "wizard", Raw: "asked"}) {
		t.Errorf("named Winner = %+v, want wizard and asked", got)
	}
	_, rep = MergeInputsWithReport(args, InputLayer[acDeployInputs]{Values: prompt, Set: PresenceOf(prompt)})
	if got, _ := rep.Winner("Deploy.Flags.Env"); got.Layer != customLayer {
		t.Errorf("unnamed Winner = %+v, want Layer custom", got)
	}
}
