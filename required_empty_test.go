package rotini

import (
	"os"
	"testing"
)

type reqEmptyInputs struct {
	App struct {
		Flags struct {
			Name string `rotini:"name"`
		}
		Arguments struct{}
		Env       struct {
			Region string `rotini:"region" recon:"region,required" env:"APP_REGION"`
		}
	}
}

func reqEmptyDef() Definition {
	return Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{{Name: "name", Identifiers: []string{"--name"}, Type: "string", Required: true}},
	}
}

// TestRequired_emptyValues pins what the `required` description promises: an empty value on the
// command line is provided, and so is an environment input set to the empty string; only an
// unset variable is missing. A flag's environment fallback set to the empty string counts as unset.
func TestRequired_emptyValues(t *testing.T) {
	t.Setenv("APP_REGION", "")
	for _, argv := range [][]string{{"--name", ""}, {"--name="}} {
		var in reqEmptyInputs
		if err := NewInputReader(InputSettings{}).Read(NewContextFor(reqEmptyDef(), argv), &in); err != nil {
			t.Errorf("%q: %v; an empty value is provided", argv, err)
		}
	}

	if err := os.Unsetenv("APP_REGION"); err != nil { // t.Setenv above restores it
		t.Fatal(err)
	}
	var in reqEmptyInputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(reqEmptyDef(), []string{"--name", "x"}), &in); err == nil {
		t.Error("an unset required environment input was not reported missing")
	}

	t.Setenv("API_TOKEN", "")
	var fallback tbReqInputs
	if err := NewInputReader(InputSettings{}).Read(NewContextFor(tbReqDef(), nil), &fallback); err == nil {
		t.Error("a required flag whose environment fallback is empty was not reported missing")
	}
}
