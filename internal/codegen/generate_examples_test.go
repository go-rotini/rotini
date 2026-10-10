package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/dotenv"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// examplesConf turns on the user-facing whole-program features and the config schemas, so the
// examples point editors at them.
const examplesConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cfgctl/zz_cfgctl.go
      package: cfgctl
  schemas:
    config:
      dir: schemas
  features:
    - type: carapace
      enabled: true
    - type: config_example
      enabled: true
    - type: env_example
      enabled: true
`

// examplesOutputs are the files examplesConf writes for the cfgctl fixture, relative to the
// module.
var examplesOutputs = []string{
	"completions/carapace/cfgctl.yaml",
	"examples/config/app.example.yaml",
	"examples/config/project.example.yaml",
	"examples/config/user.example.yaml",
	"examples/config/secrets.example.json",
	"examples/config/tuning.example.toml",
	"examples/config/editor.example.jsonc",
	"examples/config/local.example.env",
	"examples/config/stages.example.yaml",
	".env.example",
}

// generateFixture generates spec (a file under testdata/program) with conf in a fresh module
// and returns its directory and the generate notices.
func generateFixture(t *testing.T, spec, module, conf string) (string, []error) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(programDir, spec))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/"+module+"\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", string(src))
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)
	var notices []error
	var genErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) { genErr = e }, func(n []error) { notices = n }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if genErr != nil {
		t.Fatalf("Generate reported: %v", genErr)
	}
	return dir, notices
}

// compareProgramGolden checks each file against testdata/program/<golden>, or rewrites them with
// -update.
func compareProgramGolden(t *testing.T, dir, golden string, files []string) {
	t.Helper()
	at := filepath.Join(programDir, golden)
	for _, rel := range files {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("%s wasn't written: %v", rel, err)
		}
		if *updateGolden {
			writeTestFile(t, at, rel, string(got))
			continue
		}
		want, err := os.ReadFile(filepath.Join(at, rel))
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", rel, got, want)
		}
	}
}

// TestExamples_golden pins the carapace spec, the config examples and .env.example for the
// cfgctl fixture, which declares files in every format, by path, walk-up and xdg, with a pinned
// key, secrets, a nested env map and profiles. Run with -update to refresh them.
func TestExamples_golden(t *testing.T) {
	dir, _ := generateFixture(t, "cfgctl.yaml", "cfgctl", examplesConf)
	compareProgramGolden(t, dir, "golden-cfgctl", examplesOutputs)

	cmd, err := os.ReadFile(filepath.Join(dir, "internal", "cmd", "cfgctl", "zz_cfgctl.go"))
	if err != nil {
		t.Fatal(err)
	}
	app, _ := os.ReadFile(filepath.Join(dir, "examples", "config", "app.example.yaml"))
	env, _ := os.ReadFile(filepath.Join(dir, ".env.example"))
	for _, want := range []string{
		"func ConfigExample(name string) (string, error) {",
		"var ConfigExampleApp = `" + string(app) + "`",
		"func EnvExample() string { return EnvExampleText }",
		"var EnvExampleText = `" + string(env) + "`",
	} {
		if !strings.Contains(string(cmd), want) {
			t.Errorf("the cmd file lacks %q", want)
		}
	}
}

// carapaceConf writes the carapace spec to a file of its own, with completion messages on.
const carapaceConf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/kit/zz_kit.go
      package: kit
  features:
    - type: completion
      enabled: true
      messages: all
    - type: carapace
      enabled: true
      file: share/kit.yaml
`

// TestCarapace_golden pins the carapace spec for a fixture with a flag group, enums with
// summaries, hidden, deprecated and negated spellings, an optional value, completion hints,
// passthrough, plugins and a deprecated command.
func TestCarapace_golden(t *testing.T) {
	dir, _ := generateFixture(t, "carapace.yaml", "kit", carapaceConf)
	compareProgramGolden(t, dir, "golden-carapace", []string{"share/kit.yaml"})
}

// TestExamples_embed stores the examples in files beside the cmd package with `embed: true`.
func TestExamples_embed(t *testing.T) {
	conf := strings.ReplaceAll(examplesConf, "example\n      enabled: true\n", "example\n      enabled: true\n      embed: true\n")
	dir, _ := generateFixture(t, "cfgctl.yaml", "cfgctl", conf)
	cmd, err := os.ReadFile(filepath.Join(dir, "internal", "cmd", "cfgctl", "zz_cfgctl.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"//go:embed renders/config_example_app.yaml\nvar ConfigExampleApp string", "//go:embed renders/env_example.env\nvar EnvExampleText string", `_ "embed"`} {
		if !strings.Contains(string(cmd), want) {
			t.Errorf("the cmd file lacks %q", want)
		}
	}
	for _, f := range []string{"config_example_app.yaml", "config_example_tuning.toml", "env_example.env"} {
		if _, err := os.Stat(filepath.Join(dir, "internal", "cmd", "cfgctl", "renders", f)); err != nil {
			t.Errorf("renders/%s wasn't written: %v", f, err)
		}
	}
}

// uncomment removes the leading key marker from every key line of an example, as a user setting
// every key would, keeping the notes and an editor modeline.
func uncomment(text, key string) string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if rest, ok := strings.CutPrefix(line, key); ok && !strings.HasPrefix(line, "# yaml-language-server:") {
			line = rest
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestExamples_roundTrip uncomments every key of each example, decodes it as its format, and
// validates it against the file's generated config schema.
func TestExamples_roundTrip(t *testing.T) {
	dir, _ := generateFixture(t, "cfgctl.yaml", "cfgctl", examplesConf)
	for _, rel := range examplesOutputs {
		if !strings.HasPrefix(rel, "examples/config/") {
			continue
		}
		t.Run(filepath.Base(rel), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatal(err)
			}
			name, _, _ := strings.Cut(filepath.Base(rel), ".")
			var doc any
			switch filepath.Ext(rel) {
			case ".yaml":
				err = yaml.Unmarshal([]byte(uncomment(string(raw), "# ")), &doc)
			case ".toml":
				err = toml.Unmarshal([]byte(uncomment(string(raw), "# ")), &doc)
			case ".jsonc":
				err = jsonc.Unmarshal([]byte(uncomment(string(raw), "// ")), &doc)
			case ".json":
				err = json.Unmarshal(raw, &doc)
			case ".env":
				if _, err := dotenv.Parse([]byte(uncomment(string(raw), "# ")), dotenv.WithRelaxedNames()); err != nil {
					t.Fatalf("uncommented, it doesn't parse: %v\n%s", err, raw)
				}
				return
			}
			if err == nil && doc == nil {
				return // every key is listed in another file's example
			}
			if err != nil {
				t.Fatalf("uncommented, it doesn't decode: %v\n%s", err, raw)
			}
			schemaPath := filepath.Join(dir, "schemas", "cfgctl."+name+configSchemaSuffix)
			src, err := os.ReadFile(schemaPath)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			res, err := jsonschema.Validate(src, instance)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid {
				t.Errorf("uncommented, it doesn't validate: %v\n%s", res.Errors, raw)
			}
		})
	}
}
