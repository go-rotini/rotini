package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// TestConfigSchemas_golden pins the schema written for each config file of
// testdata/configschema/spec.yaml. Refresh with
// `go test ./internal/codegen -run ConfigSchemas_golden -update`.
func TestConfigSchemas_golden(t *testing.T) {
	dir := filepath.Join("testdata", "configschema")
	gp := configSchemaProgram(t, filepath.Join(dir, "spec.yaml"))
	out := t.TempDir()
	gp.module.root = out
	if err := gp.writeConfigSchemas("schemas"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(out, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"ops-deploy.team.config.json", "ops.pinned.config.json", "ops.user.config.json"}; !slices.Equal(names, want) {
		t.Errorf("files = %v, want %v (the dotenv file gets none)", names, want)
	}
	for _, name := range names {
		got := readTestFile(t, filepath.Join(out, "schemas", name))
		if *updateGolden {
			writeTestFile(t, dir, name, got)
			continue
		}
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		if got != string(want) {
			t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
		if _, err := compileSchema(name, []byte(got)); err != nil {
			t.Fatalf("%s is not a valid JSON Schema: %v", name, err)
		}
	}
}

// TestConfigSchemas_keysMatchTheRuntime checks that every key a config schema lists is a key the
// generated code binds: a recon tag of the command that reads the file.
func TestConfigSchemas_keysMatchTheRuntime(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("testdata", "configschema", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	gp := configSchemaProgram(t, path)
	files := emitInModule(t, readTestFile(t, path), goldenConf)
	tags := map[string]bool{}
	for _, m := range regexp.MustCompile(`recon:"([^",]+)`).FindAllStringSubmatch(files["internal/cmd/demo/zz_demo.go"], -1) {
		tags[m[1]] = true
	}
	for _, cf := range gp.configFiles {
		for _, e := range gp.configFileKeys(cf) {
			if !tags[e.key] {
				t.Errorf("file %q lists key %q, which no generated field binds", cf.Name, e.key)
			}
		}
	}
}

// TestConfigSchemas_prune checks that a stale *.config.json is removed and another file kept.
func TestConfigSchemas_prune(t *testing.T) {
	gp := configSchemaProgram(t, filepath.Join("testdata", "configschema", "spec.yaml"))
	out := t.TempDir()
	gp.module.root = out
	writeTestFile(t, out, "schemas/ops.gone.config.json", "{}")
	writeTestFile(t, out, "schemas/notes.json", "{}")
	if err := gp.writeConfigSchemas("schemas"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "schemas", "ops.gone.config.json")); !os.IsNotExist(err) {
		t.Error("a stale config schema survived")
	}
	if _, err := os.Stat(filepath.Join(out, "schemas", "notes.json")); err != nil {
		t.Errorf("another file was removed: %v", err)
	}
}

// TestConfigSchemas_sampleValidates checks a config file a user would write against its schema:
// a correct one passes, and a value outside an enum or a bound fails.
func TestConfigSchemas_sampleValidates(t *testing.T) {
	gp := configSchemaProgram(t, filepath.Join("testdata", "configschema", "spec.yaml"))
	var user scopedConfigFile
	for _, cf := range gp.configFiles {
		if cf.Name == "user" {
			user = cf
		}
	}
	raw, err := json.Marshal(gp.configSchemaDoc(user))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("user.config.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc  string
		fail bool
	}{
		{`{"ui": {"color": "tty"}, "defaults": {"region": "us"}, "net": {"retries": 3}, "other": 1}`, false},
		{`{"ui": {"color": "purple"}}`, true},
		{`{"net": {"retries": 10}}`, true},
	} {
		problems := validateInstance("config", []byte(c.doc), schema)
		if got := len(problems) > 0; got != c.fail {
			t.Errorf("%s: problems %v, want failure %v", c.doc, problems, c.fail)
		}
	}
}

// configSchemaProgram resolves the spec at path into a program with a real planner.
func configSchemaProgram(t *testing.T, path string) *program {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, readTestFile(t, path)), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/ops")
	if err != nil {
		t.Fatal(err)
	}
	gp.plan = newPlanner(false)
	return gp
}
