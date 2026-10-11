package rotini

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type timeMapCmd struct {
	Flags     struct{}
	Arguments struct{}
	Env       struct {
		Days map[string]time.Time `rotini:"days" recon:"days" env:"APP_DAYS" layout:"01/02/2006"`
	}
	Config struct {
		Due    map[string]time.Time `rotini:"due" recon:"due" layout:"01/02/2006"`
		Labels map[string]string    `rotini:"labels" recon:"labels"`
	}
}
type timeMapInputs struct{ App timeMapCmd }

func timeMapBind(t *testing.T, conf string, env ...string) (timeMapInputs, error) {
	t.Helper()
	dir := t.TempDir()
	idWrite(t, dir, "c.yaml", conf)
	meta := InputSettings{ConfigFiles: []ConfigFile{{Name: "c", Scope: "app", Path: filepath.Join(dir, "c.yaml"), Format: "yaml"}}}
	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil).WithEnviron(env)
	var in timeMapInputs
	err := NewInputReader(meta).Read(rtx, &in)
	return in, err
}

// Env and config maps of dates read their values under the declared layout, as a flag's do,
// and a configuration file's map binds whole.
func TestTimeMaps_envAndConfig(t *testing.T) {
	in, err := timeMapBind(t, "due:\n  start: 01/02/2026\n  end: '01/09/2026'\nlabels:\n  team: core\n", "APP_DAYS=start=01/02/2026, end=01/09/2026")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{"start": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "end": time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)}
	if !maps.EqualFunc(in.App.Env.Days, want, time.Time.Equal) || !maps.EqualFunc(in.App.Config.Due, want, time.Time.Equal) {
		t.Errorf("env %v, config %v; want %v", in.App.Env.Days, in.App.Config.Due, want)
	}
	if in.App.Config.Labels["team"] != "core" {
		t.Errorf("labels = %v", in.App.Config.Labels)
	}

	if _, err := timeMapBind(t, "", "APP_DAYS=start=nope"); err == nil || !strings.Contains(err.Error(), "APP_DAYS") {
		t.Errorf("env value that is no date = %v", err)
	}
	if _, err := timeMapBind(t, "due:\n  start: nope\n"); err == nil || !strings.Contains(err.Error(), "due") {
		t.Errorf("config value that is no date = %v", err)
	}
}
