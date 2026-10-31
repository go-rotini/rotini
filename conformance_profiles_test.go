package rotini

import (
	"errors"
	"strings"
	"testing"
)

// The profile rows of the input conformance matrix: a configuration file with named profiles,
// each channel read against the selected profile and the file's shared keys.

func profileConformanceCases() []dataCase {
	read := func(t *testing.T, env []string, argv ...string) (pfInputs, error) {
		t.Helper()
		path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
		return pfRead(t, env, pfSettings(pfFileAt(path)), argv...)
	}
	return []dataCase{
		{"PROF-01", func(t *testing.T) { // a config input reads the selected profile's key over the shared one
			in, err := read(t, nil, "--profile", "prod")
			if err != nil || in.App.Config.Host != "prod.example" {
				t.Errorf("host = %q, %v", in.App.Config.Host, err)
			}
		}},
		{"PROF-02", func(t *testing.T) { // a key the profile doesn't hold is read from the shared keys
			in, err := read(t, nil, "--profile", "prod")
			if err != nil || in.App.Config.DBPort != 5432 {
				t.Errorf("db.port = %d, %v", in.App.Config.DBPort, err)
			}
		}},
		{"PROF-03", func(t *testing.T) { // a flag's configuration fallback reads the profile
			in, err := read(t, nil, "--profile", "prod")
			if err != nil || in.App.Flags.Region != "prod-r" {
				t.Errorf("region = %q, %v", in.App.Flags.Region, err)
			}
		}},
		{"PROF-04", func(t *testing.T) { // the environment beats the profile
			in, err := read(t, []string{"APP_REGION=env-r"}, "--profile", "prod")
			if err != nil || in.App.Flags.Region != "env-r" {
				t.Errorf("region = %q, %v", in.App.Flags.Region, err)
			}
		}},
		{"PROF-05", func(t *testing.T) { // the command line beats the profile
			in, err := read(t, nil, "--profile", "prod", "--region", "argv-r")
			if err != nil || in.App.Flags.Region != "argv-r" {
				t.Errorf("region = %q, %v", in.App.Flags.Region, err)
			}
		}},
		{"PROF-06", func(t *testing.T) { // the selector: command line, then variable, then default
			in, err := read(t, []string{"APP_PROFILE=default"}, "--profile", "prod")
			if err != nil || in.App.Flags.Region != "prod-r" {
				t.Errorf("argv over variable: region = %q, %v", in.App.Flags.Region, err)
			}
			in, err = read(t, []string{"APP_PROFILE=prod"})
			if err != nil || in.App.Flags.Region != "prod-r" {
				t.Errorf("variable over default: region = %q, %v", in.App.Flags.Region, err)
			}
			in, err = read(t, nil)
			if err != nil || in.App.Flags.Region != "default-r" {
				t.Errorf("default: region = %q, %v", in.App.Flags.Region, err)
			}
		}},
		{"PROF-07", func(t *testing.T) { // an unknown profile chosen explicitly is a usage error listing the defined ones
			_, err := read(t, []string{"APP_PROFILE=prdo"})
			token, candidates, ok := SuggestionFacts(err)
			if !errors.Is(err, ErrUsage) || !ok || token != "prdo" || len(candidates) != 3 {
				t.Errorf("err = %v (facts %q %v)", err, token, candidates)
			}
		}},
		{"PROF-08", func(t *testing.T) { // a short-circuit flag waives the unknown-profile check
			if _, err := read(t, []string{"APP_PROFILE=prdo"}, "--help"); err != nil {
				t.Errorf("err = %v", err)
			}
		}},
		{"PROF-09", func(t *testing.T) { // an input pinned to the file reads the selected profile
			in, err := read(t, nil, "--profile", "prod")
			if err != nil || in.App.Config.Pinned != "prod-pin" {
				t.Errorf("pinned = %q, %v", in.App.Config.Pinned, err)
			}
		}},
		{"PROF-10", func(t *testing.T) { // an unknown profile is reported before a missing required flag, by both readers
			dir := t.TempDir()
			meta := pfSettings(pfFileAt(pfWrite(t, dir, "app.yaml", pfFile)))
			def := pfDef()
			def.Flags = append(def.Flags, FlagDef{Name: "need", Identifiers: []string{"--need"}, Type: "string", Required: true})
			for _, argv := range [][]string{{"--profile", "nope"}, {"--profile", "prod"}} {
				rtx := NewContextFor(def, argv).WithDir(dir).WithInputSettings(meta)
				_, err := rtx.Inputs[pfInputs]()
				_, _, rerr := rtx.InputsWithReport[pfInputs]()
				want := `app: --profile: profile "nope" is not defined in configuration file `
				if argv[1] == "prod" {
					want = "app: missing required input: --need"
				}
				for reader, e := range map[string]error{"Inputs": err, "InputsWithReport": rerr} {
					if e == nil || !strings.HasPrefix(e.Error(), want) || !errors.Is(e, ErrUsage) {
						t.Errorf("%v: %s = %v, want %q", argv, reader, e, want)
					}
				}
			}
		}},
		{"PROF-11", func(t *testing.T) { // a map merged from the profile and the shared keys credits each entry to its source
			path := pfWrite(t, t.TempDir(), "app.yaml", pfFile)
			_, rep, err := pfContext(t.TempDir(), nil, pfSettings(pfFileAt(path)), "--profile", "prod").InputsWithReport[pfInputs]()
			w, _ := rep.Winner("App.Config.Labels")
			if err != nil || w.Raw != "team=core, tier=api" || w.Origin != "config:app#labels.team, config:app#profiles.prod.labels.tier" {
				t.Fatalf("winner = %+v, %v", w, err)
			}
			h := rep.History("App.Config.Labels")
			if len(h) < 2 || h[len(h)-2] != (InputSource{Layer: "files", Raw: "web", Origin: "config:app#labels.tier"}) {
				t.Errorf("history = %+v, want the shared tier the profile overrode below the winner", h)
			}
		}},
		{"PROF-12", func(t *testing.T) { // a list in the chosen profile for a single-value flag is an error naming the profile
			path := pfWrite(t, t.TempDir(), "app.yaml", "profiles:\n  prod:\n    region: [a, b]\n")
			_, err := pfRead(t, nil, pfSettings(pfFileAt(path)), "--profile", "prod")
			if err == nil || !strings.HasSuffix(err.Error(), "--region takes one value, not a list (from configuration file "+path+" (profile prod), key region)") {
				t.Errorf("err = %v", err)
			}
		}},
	}
}

func TestConformance_ProfileMatrix(t *testing.T) {
	wiringModes(t, func(t *testing.T) {
		for _, c := range profileConformanceCases() {
			t.Run(c.id, c.check)
		}
	})
}
