package codegen

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mountTree writes files (path → content) under a new temp dir, chdirs there, and returns it.
func mountTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

func childSpec(name string, aliases ...string) string {
	s := "version: 1.6.0\ncommand:\n  name: " + name + "\n"
	if len(aliases) > 0 {
		s += "  aliases: [" + strings.Join(aliases, ", ") + "]\n"
	}
	return s
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMount(t *testing.T) {
	children := map[string]string{
		"cmd/ab12/.rotini.spec.yaml": childSpec("ab12"),
		"cmd/r2d2/.rotini.spec.yaml": childSpec("r2d2"),
	}
	tests := []struct {
		name   string
		parent string
		specs  []string
		opts   MountOptions
		want   string
	}{
		{
			name:   "no commands key",
			parent: "version: 1.6.0\ncommand:\n  name: app\n  summary: the app\n",
			specs:  []string{"cmd/ab12/.rotini.spec.yaml"},
			opts:   MountOptions{Group: "audience"},
			want:   "version: 1.6.0\ncommand:\n  name: app\n  summary: the app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n      group: audience\n",
		},
		{
			name:   "no trailing newline",
			parent: "command:\n  name: app",
			specs:  []string{"cmd/ab12/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n",
		},
		{
			name:   "empty flow list",
			parent: "command:\n  name: app\n  commands: []\n  summary: s\n",
			specs:  []string{"cmd/ab12/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n  summary: s\n",
		},
		{
			name:   "after the last $ref, before inline commands and comments",
			parent: "command:\n  name: app\n  commands:\n    # children\n    - $ref: ../ab12/.rotini.spec.yaml\n    # end\n    - name: help\n",
			specs:  []string{"cmd/r2d2/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    # children\n    - $ref: ../ab12/.rotini.spec.yaml\n    - $ref: ../r2d2/.rotini.spec.yaml\n    # end\n    - name: help\n",
		},
		{
			name:   "keeps the list's indentation",
			parent: "command:\n    name: app\n    commands:\n    -   name: help\n",
			specs:  []string{"cmd/ab12/.rotini.spec.yaml"},
			opts:   MountOptions{Group: "true"},
			want:   "command:\n    name: app\n    commands:\n    -   name: help\n    -   $ref: ../ab12/.rotini.spec.yaml\n        group: \"true\"\n",
		},
		{
			name:   "crlf",
			parent: "command:\r\n  name: app\r\n  commands:\r\n    - name: help\r\n",
			specs:  []string{"cmd/ab12/.rotini.spec.yaml"},
			want:   "command:\r\n  name: app\r\n  commands:\r\n    - name: help\r\n    - $ref: ../ab12/.rotini.spec.yaml\r\n",
		},
		{
			name:   "prune the last $ref, mount in its place",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n    - name: help\n    - $ref: ../gone/.rotini.spec.yaml\n      group: g\n",
			specs:  []string{"cmd/r2d2/.rotini.spec.yaml"},
			opts:   MountOptions{Prune: true},
			want:   "command:\n  name: app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n    - $ref: ../r2d2/.rotini.spec.yaml\n    - name: help\n",
		},
		{
			name:   "prune every entry",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../gone/.rotini.spec.yaml\n  summary: s\n",
			opts:   MountOptions{Prune: true},
			want:   "command:\n  name: app\n  summary: s\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"cmd/app/.rotini.spec.yaml": tt.parent}
			maps.Copy(files, children)
			mountTree(t, files)
			if _, err := Mount("cmd/app/.rotini.spec.yaml", tt.specs, tt.opts); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, "cmd/app/.rotini.spec.yaml"); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestMount_report(t *testing.T) {
	mountTree(t, map[string]string{
		"cmd/app/.rotini.spec.yaml":  "command:\n  name: app\n  commands:\n    - $ref: ../ab12/.rotini.spec.yaml\n    - $ref: ../gone/.rotini.spec.yaml\n",
		"cmd/ab12/.rotini.spec.yaml": childSpec("ab12"),
		"cmd/r2d2/.rotini.spec.yaml": childSpec("r2d2"),
	})
	before := readText(t, "cmd/app/.rotini.spec.yaml")
	specs := []string{"cmd/ab12/.rotini.spec.yaml", "cmd/app/.rotini.spec.yaml", "cmd/r2d2/.rotini.spec.yaml", "cmd/r2d2/.rotini.spec.yaml"}
	res, err := Mount("cmd/app/.rotini.spec.yaml", specs, MountOptions{Prune: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Mounted, []MountEntry{{"r2d2", "../r2d2/.rotini.spec.yaml"}}) ||
		!slices.Equal(res.Already, []MountEntry{{"ab12", "../ab12/.rotini.spec.yaml"}}) ||
		!slices.Equal(res.Removed, []string{"../gone/.rotini.spec.yaml"}) ||
		!slices.Equal(res.Skipped, []string{"cmd/app/.rotini.spec.yaml"}) || !res.Changed() {
		t.Errorf("result %+v", res)
	}
	if readText(t, "cmd/app/.rotini.spec.yaml") != before {
		t.Error("a dry run wrote the spec")
	}

	res, err = Mount("cmd/app/.rotini.spec.yaml", []string{"cmd/ab12/.rotini.spec.yaml"}, MountOptions{})
	if err != nil || res.Changed() || readText(t, "cmd/app/.rotini.spec.yaml") != before {
		t.Errorf("an already-mounted spec changed something: %+v, %v", res, err)
	}
}

func TestMount_refuses(t *testing.T) {
	tests := []struct {
		name   string
		files  map[string]string
		into   string
		specs  []string
		errHas string
	}{
		{
			name: "two new specs share a name",
			files: map[string]string{
				"a/.rotini.spec.yaml": childSpec("db"),
				"b/.rotini.spec.yaml": childSpec("db"),
			},
			specs:  []string{"a/.rotini.spec.yaml", "b/.rotini.spec.yaml"},
			errHas: `b/.rotini.spec.yaml: the command name "db" is already taken by a/.rotini.spec.yaml`,
		},
		{
			name:   "an alias takes an inline command's name",
			files:  map[string]string{"a/.rotini.spec.yaml": childSpec("db", "help")},
			specs:  []string{"a/.rotini.spec.yaml"},
			errHas: `the command name "help" is already taken by the command "help"`,
		},
		{
			name:   "a hidden alias of an inline command",
			files:  map[string]string{"a/.rotini.spec.yaml": childSpec("db")},
			into:   "command:\n  name: app\n  commands:\n    - name: data\n      hidden_aliases: [db]\n",
			specs:  []string{"a/.rotini.spec.yaml"},
			errHas: `the command name "db" is already taken by the command "data"`,
		},
		{
			name:   "a plugin's alias",
			files:  map[string]string{"a/.rotini.spec.yaml": childSpec("db")},
			into:   "command:\n  name: app\n  plugins:\n    - name: database\n      aliases: [db]\n  commands:\n    - name: help\n",
			specs:  []string{"a/.rotini.spec.yaml"},
			errHas: `the command name "db" is already taken by the plugin "database"`,
		},
		{
			name: "a name written beside an existing $ref",
			files: map[string]string{
				"cmd/old/.rotini.spec.yaml": childSpec("old"),
				"a/.rotini.spec.yaml":       childSpec("renamed"),
			},
			into:   "command:\n  name: app\n  commands:\n    - $ref: ../old/.rotini.spec.yaml\n      name: renamed\n",
			specs:  []string{"a/.rotini.spec.yaml"},
			errHas: `already taken by ../old/.rotini.spec.yaml`,
		},
		{
			name:   "a flow list with entries",
			files:  map[string]string{"a/.rotini.spec.yaml": childSpec("db")},
			into:   "command:\n  name: app\n  commands: [{name: help}]\n",
			specs:  []string{"a/.rotini.spec.yaml"},
			errHas: "written as a [...] list",
		},
		{
			name:   "a missing spec",
			specs:  []string{"nope/.rotini.spec.yaml"},
			errHas: "nope/.rotini.spec.yaml",
		},
		{
			name:   "a mod:// ref",
			specs:  []string{"mod://example.com/db@v1.0.0/.rotini.spec.yaml"},
			errHas: "write a mod:// $ref by hand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			into := tt.into
			if into == "" {
				into = "command:\n  name: app\n  commands:\n    - name: help\n"
			}
			files := map[string]string{"cmd/app/.rotini.spec.yaml": into}
			maps.Copy(files, tt.files)
			mountTree(t, files)
			_, err := Mount("cmd/app/.rotini.spec.yaml", tt.specs, MountOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.errHas) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.errHas)
			}
			if readText(t, "cmd/app/.rotini.spec.yaml") != into {
				t.Error("a refused mount wrote the spec")
			}
		})
	}
}

func TestMount_yamlOnly(t *testing.T) {
	mountTree(t, map[string]string{"app.json": `{"command":{"name":"app"}}`})
	if _, err := Mount("app.json", nil, MountOptions{Prune: true}); err == nil || !strings.Contains(err.Error(), "YAML specs only") {
		t.Fatalf("err = %v", err)
	}
}

func TestMount_under(t *testing.T) {
	children := map[string]string{
		"cmd/migrate/.rotini.spec.yaml": childSpec("migrate"),
		"cmd/seed/.rotini.spec.yaml":    childSpec("seed", "s"),
		// db declares its own "seed" and mounts "backup" itself.
		"cmd/db/.rotini.spec.yaml":     "command:\n  name: db\n  commands:\n    - name: seed\n    - $ref: ../backup/.rotini.spec.yaml\n",
		"cmd/backup/.rotini.spec.yaml": childSpec("backup", "bk"),
	}
	tests := []struct {
		name   string
		parent string
		under  []string
		specs  []string
		want   string
		errHas string
	}{
		{
			name:   "an inline command, by alias, with no commands yet",
			parent: "command:\n  name: app\n  commands:\n    - name: data\n      aliases: [d]\n      summary: data\n    - name: help\n",
			under:  []string{"d"},
			specs:  []string{"cmd/migrate/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    - name: data\n      aliases: [d]\n      summary: data\n      commands:\n        - $ref: ../migrate/.rotini.spec.yaml\n    - name: help\n",
		},
		{
			name:   "two levels down",
			parent: "command:\n  name: app\n  commands:\n    - name: data\n      commands:\n        - name: sql\n          commands:\n            - name: shell\n",
			under:  []string{"data", "sql"},
			specs:  []string{"cmd/migrate/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    - name: data\n      commands:\n        - name: sql\n          commands:\n            - name: shell\n            - $ref: ../migrate/.rotini.spec.yaml\n",
		},
		{
			name:   "beside a $ref",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n      group: data\n    - name: help\n",
			under:  []string{"db"},
			specs:  []string{"cmd/migrate/.rotini.spec.yaml"},
			want:   "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n      group: data\n      commands:\n        - $ref: ../migrate/.rotini.spec.yaml\n    - name: help\n",
		},
		{
			name:   "a name the $ref'd cli already has",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n",
			under:  []string{"db"},
			specs:  []string{"cmd/seed/.rotini.spec.yaml"},
			errHas: `the command name "seed" is already taken by ../db/.rotini.spec.yaml's own command "seed" under app db`,
		},
		{
			name:   "an alias of a cli the $ref'd cli mounts",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n",
			under:  []string{"db"},
			specs:  []string{"cmd/other/.rotini.spec.yaml"},
			errHas: `"bk" is already taken`,
		},
		{
			name:   "a command declared in the $ref'd spec",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n",
			under:  []string{"db", "seed"},
			specs:  []string{"cmd/migrate/.rotini.spec.yaml"},
			errHas: `app db has no command "seed" written here; if it's declared in ../db/.rotini.spec.yaml, mount into that spec instead`,
		},
		{
			name:   "no such command",
			parent: "command:\n  name: app\n  commands:\n    - name: help\n",
			under:  []string{"nope"},
			specs:  []string{"cmd/migrate/.rotini.spec.yaml"},
			errHas: `app has no command "nope"`,
		},
		{
			name:   "a spec under itself",
			parent: "command:\n  name: app\n  commands:\n    - $ref: ../db/.rotini.spec.yaml\n",
			under:  []string{"db"},
			specs:  []string{"cmd/db/.rotini.spec.yaml"},
			errHas: "can't mount a spec under itself (app db)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"cmd/app/.rotini.spec.yaml": tt.parent, "cmd/other/.rotini.spec.yaml": childSpec("other", "bk")}
			maps.Copy(files, children)
			mountTree(t, files)
			_, err := Mount("cmd/app/.rotini.spec.yaml", tt.specs, MountOptions{Under: tt.under})
			got := readText(t, "cmd/app/.rotini.spec.yaml")
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.errHas)
				}
				if got != tt.parent {
					t.Error("a refused mount wrote the spec")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
