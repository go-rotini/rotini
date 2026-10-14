package rotini

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A word written detached after a secret optional-value flag stays out of the error, both in
// the "takes its value attached" hint and in an unknown-command message.
func TestDetachedSecretNotEchoed(t *testing.T) {
	token := FlagDef{Name: "token", Identifiers: []string{"--token"}, Type: "string", Secret: true, ImplicitValue: "prompt"}
	for _, def := range []Definition{
		{Name: "app", Flags: []FlagDef{token}},
		{Name: "app", Flags: []FlagDef{token}, Commands: []CommandDef{{Name: "sub"}}},
	} {
		argv := []string{"--token", "hunter2"}
		var in struct{}
		err := NewParser().Parse(NewContextFor(def, argv), &in)
		if err == nil {
			t.Fatal("parse succeeded, want an extra-word error")
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("error %q repeats the secret", err)
		}
		if !strings.Contains(err.Error(), "--token takes its value attached: --token=[redacted]") {
			t.Errorf("error %q, want the redacted hint", err)
		}
		var pe *ParseError
		if errors.As(err, &pe) && strings.Contains(pe.Token, "hunter2") {
			t.Errorf("token %q repeats the secret", pe.Token)
		}
	}
}

type secretCreds struct {
	User string `json:"user"`
	Pin  int    `json:"pin,omitempty"`
}

// A secret object flag's value stays out of its decode errors, given literally or from a file.
func TestSecretObjectFlagNotEchoed(t *testing.T) {
	const schema = `{"$schema":"http://json-schema.org/draft-07/schema#","$ref":"#/definitions/Creds","definitions":{` +
		`"Creds":{"type":"object","properties":{"user":{"type":"string","enum":["admin"]},"pin":{"type":"integer"}}}}}`
	type inputs struct {
		App struct {
			Flags struct {
				Creds secretCreds `rotini:"creds"`
			}
		}
	}
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{
		{Name: "creds", Identifiers: []string{"--creds"}, Type: "Creds", ObjectSchema: schema, Secret: true, From: []string{"value", "file"}},
	}}
	file := t.TempDir() + "/creds"
	if err := os.WriteFile(file, []byte("user=admin,pin=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"hunter2", "user=admin,pin=hunter2", `{"user":"admin","pin":hunter2}`, `{"user":"hunter2"}`, "@" + file} {
		var in inputs
		err := NewParser().Parse(NewContextFor(def, []string{"--creds", v}), &in)
		if err == nil {
			t.Errorf("%s: parsed, want an error", v)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error %q repeats the secret", v, err)
		}
	}
}
