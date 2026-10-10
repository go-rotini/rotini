package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The write-back rows of the input conformance matrix: SetConfigValue and UnsetConfigValue
// write a declared config file so that the input reader reads the value back.

// wcKeys is the key table of pfInputs' configuration inputs and fallbacks.
func wcKeys() []ConfigKey {
	return []ConfigKey{
		{Key: "db.host", Type: "string"},
		{Key: "db.port", Type: "int", Minimum: new(1.0)},
		{Key: "labels", Type: "map[string]string"},
		{Key: "pinned", Type: "string"},
		{Key: "port", Type: "int"},
		{Key: "region", Type: "string"},
		{Key: "tags", Type: "[]string"},
	}
}

func writeConformanceCases() []dataCase {
	setup := func(t *testing.T, body string, profiles bool) (dir string, meta InputSettings) {
		t.Helper()
		dir = t.TempDir()
		path := pfWrite(t, dir, "app.yaml", body)
		f := ConfigFile{Name: "app", Path: path, Keys: wcKeys()}
		if profiles {
			f.Profiles = pfProfiles()
		}
		return dir, pfSettings(f)
	}
	readBack := func(t *testing.T, dir string, meta InputSettings, env []string, argv ...string) pfInputs {
		t.Helper()
		var in pfInputs
		if err := NewInputReader(meta).Read(pfContext(dir, env, meta, argv...), &in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	return []dataCase{
		{"WRITE-01", func(t *testing.T) { // a written config input is read back with its type
			dir, meta := setup(t, "", false)
			rtx := pfContext(dir, nil, meta)
			for _, kv := range [][2]string{{"db.host", "h"}, {"db.port", "6"}, {"tags", "a,b"}, {"labels", "k=v"}} {
				if err := SetConfigValue(rtx, "app", kv[0], kv[1]); err != nil {
					t.Fatal(err)
				}
			}
			in := readBack(t, dir, meta, nil)
			if c := in.App.Config; c.Host != "h" || c.DBPort != 6 || !slices.Equal(c.Tags, []string{"a", "b"}) || c.Labels["k"] != "v" {
				t.Errorf("read back %+v", c)
			}
		}},
		{"WRITE-02", func(t *testing.T) { // a written flag fallback is read back below argv and env
			dir, meta := setup(t, "region: old\n", false)
			if err := SetConfigValue(pfContext(dir, nil, meta), "app", "region", "new"); err != nil {
				t.Fatal(err)
			}
			if in := readBack(t, dir, meta, nil); in.App.Flags.Region != "new" {
				t.Errorf("region = %q", in.App.Flags.Region)
			}
			if in := readBack(t, dir, meta, []string{"APP_REGION=env"}); in.App.Flags.Region != "env" {
				t.Errorf("the environment lost: region = %q", in.App.Flags.Region)
			}
		}},
		{"WRITE-03", func(t *testing.T) { // an invalid value is a usage error and the file is untouched
			dir, meta := setup(t, "db: {port: 5}\n", false)
			err := SetConfigValue(pfContext(dir, nil, meta), "app", "db.port", "0")
			if !errors.Is(err, ErrUsage) {
				t.Errorf("err = %v", err)
			}
			if in := readBack(t, dir, meta, nil); in.App.Config.DBPort != 5 {
				t.Errorf("db.port = %d", in.App.Config.DBPort)
			}
		}},
		{"WRITE-04", func(t *testing.T) { // an undeclared key is a usage error naming the declared keys
			dir, meta := setup(t, "", false)
			err := SetConfigValue(pfContext(dir, nil, meta), "app", "db.hots", "x")
			ie, ok := errors.AsType[*InputError](err)
			if !ok || !errors.Is(err, ErrUsage) || ie.Token != "db.hots" || !slices.Contains(ie.Candidates, "db.host") {
				t.Errorf("err = %#v", err)
			}
		}},
		{"WRITE-05", func(t *testing.T) { // the selected profile gets the value, and the run reads it
			dir, meta := setup(t, pfFile, true)
			if err := SetConfigValue(pfContext(dir, []string{"APP_PROFILE=prod"}, meta), "app", "db.host", "new.example"); err != nil {
				t.Fatal(err)
			}
			if in := readBack(t, dir, meta, nil, "--profile", "prod"); in.App.Config.Host != "new.example" {
				t.Errorf("prod host = %q", in.App.Config.Host)
			}
			if in := readBack(t, dir, meta, nil, "--profile", "default"); in.App.Config.Host != "shared.example" {
				t.Errorf("default host = %q", in.App.Config.Host)
			}
		}},
		{"WRITE-06", func(t *testing.T) { // a missing file is created where the reader looks
			dir := t.TempDir()
			meta := pfSettings(ConfigFile{Name: "app", Path: "sub/app.yaml", Keys: wcKeys()})
			if err := SetConfigValue(pfContext(dir, nil, meta), "app", "port", "8080"); err != nil {
				t.Fatal(err)
			}
			if in := readBack(t, dir, meta, nil); in.App.Flags.Port != 8080 {
				t.Errorf("port = %d", in.App.Flags.Port)
			}
		}},
		{"WRITE-07", func(t *testing.T) { // unset removes the key, keeping the file's other lines
			dir, meta := setup(t, "# keep\nregion: r\nport: 1\n", false)
			if err := UnsetConfigValue(pfContext(dir, nil, meta), "app", "region"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "app.yaml"))
			if err != nil || string(b) != "# keep\nport: 1\n" {
				t.Errorf("file = %q, %v", b, err)
			}
		}},
		{"WRITE-08", func(t *testing.T) { // comments and layout survive a write
			body := "# head\nregion: r # line\n\ndb:\n  # foot\n  port: 1\n"
			dir, meta := setup(t, body, false)
			if err := SetConfigValue(pfContext(dir, nil, meta), "app", "db.port", "2"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "app.yaml"))
			if err != nil || string(b) != strings.Replace(body, "port: 1", "port: 2", 1) {
				t.Errorf("file = %q, %v", b, err)
			}
		}},
	}
}

func TestConformance_WriteMatrix(t *testing.T) {
	for _, c := range writeConformanceCases() {
		t.Run(c.id, c.check)
	}
}
