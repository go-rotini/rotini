package rotini

import (
	"reflect"
	"testing"
)

// TestConfigMap_mergesPerEntryAcrossFiles pins that a map spread across several configuration
// files merges entry by entry, the first declared file winning per key, for a config input as
// for a flag's config fallback, and that the report credits each entry to its own file and key.
func TestConfigMap_mergesPerEntryAcrossFiles(t *testing.T) {
	type in struct {
		App struct {
			Flags struct {
				Tags map[string]string `rotini:"tags" recon:"tags"`
			}
			Arguments struct{}
			Config    struct {
				Labels map[string]string `rotini:"labels" recon:"labels"`
			}
		}
	}
	dir := t.TempDir()
	user := pfWrite(t, dir, "user.yaml", "labels: {tier: api}\ntags: {a: user}\n")
	system := pfWrite(t, dir, "system.yaml", "labels: {team: core, tier: web}\ntags: {a: sys, b: sys}\n")
	meta := pfSettings(ConfigFile{Name: "user", Path: user}, ConfigFile{Name: "system", Path: system})
	def := Definition{Name: "app", Handler: "App", Flags: []FlagDef{{Name: "tags", Identifiers: []string{"--tag"}, Type: "map[string]string"}}}
	rtx := NewContextFor(def, nil).WithDir(dir).WithInputSettings(meta)

	got, rep, err := rtx.InputsWithReport[in]()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"team": "core", "tier": "api"}; !reflect.DeepEqual(got.App.Config.Labels, want) {
		t.Errorf("config labels = %v, want %v", got.App.Config.Labels, want)
	}
	if want := map[string]string{"a": "user", "b": "sys"}; !reflect.DeepEqual(got.App.Flags.Tags, want) {
		t.Errorf("flag tags = %v, want %v", got.App.Flags.Tags, want)
	}
	plain, err := rtx.Inputs[in]()
	if err != nil || !reflect.DeepEqual(plain, got) {
		t.Errorf("Inputs = %+v, %v; want the reported values", plain, err)
	}
	w, _ := rep.Winner("App.Config.Labels")
	if w.Origin != "config:system#labels.team, config:user#labels.tier" {
		t.Errorf("labels origin = %q", w.Origin)
	}
	h := rep.History("App.Config.Labels")
	if len(h) < 2 || h[len(h)-2] != (InputSource{Layer: "files", Raw: "web", Origin: "config:system#labels.tier"}) {
		t.Errorf("history = %+v, want the system tier the user file overrode", h)
	}
}

// TestConfigMap_profileOverAnotherFile pins that a map merged from a selected profile, its
// file's shared keys and a further file credits each entry to the source it came from.
func TestConfigMap_profileOverAnotherFile(t *testing.T) {
	dir := t.TempDir()
	app := pfWrite(t, dir, "app.yaml", pfFile)
	system := pfWrite(t, dir, "system.yaml", "labels: {owner: ops, tier: sys}\n")
	meta := pfSettings(pfFileAt(app), ConfigFile{Name: "system", Path: system})
	got, rep, err := pfContext(dir, nil, meta, "--profile", "prod").InputsWithReport[pfInputs]()
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"owner": "ops", "team": "core", "tier": "api"}; !reflect.DeepEqual(got.App.Config.Labels, want) {
		t.Errorf("labels = %v, want %v", got.App.Config.Labels, want)
	}
	w, _ := rep.Winner("App.Config.Labels")
	if want := "config:system#labels.owner, config:app#labels.team, config:app#profiles.prod.labels.tier"; w.Origin != want {
		t.Errorf("origin = %q, want %q", w.Origin, want)
	}
}
