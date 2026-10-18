package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// updateWiring rewrites the wired-feature golden lists:
// `go test ./internal/codegen -run WiredFeatures -update-wiring`.
var updateWiring = flag.Bool("update-wiring", false, "rewrite the wired-feature golden lists")

const wiringHead = "version: 0.0.0\ncommand:\n  name: app\n  summary: s\n"

// wiredFor resolves the spec body under wiringHead and returns the features its NewProgram
// passes.
func wiredFor(t *testing.T, body string) []string {
	t.Helper()
	spec := decodeSpecYAML(t, wiringHead+body)
	gp, err := resolveTree(spec, filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	gp.spec = spec // set by the generate pipeline; root-level settings read it
	return wiredFeatures(gp)
}

// Each declaration wires exactly the features it needs, in the runtime's canonical order.
func TestWiredFeatures_perDeclaration(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"nothing", `  flags:
    - {name: v, identifiers: [-v], schema: {type: bool, enum: [true]}}
  arguments:
    - {name: name, schema: {type: string, enum: [a, b]}}
`, nil},
		{"plugins", `  plugins:
    - {name: ext}
`, []string{"rotini.Plugins()"}},
		{"plugin discovery on a sub-command", `  commands:
    - name: sub
      summary: s
      plugin_discovery: {prefix: app-}
`, []string{"rotini.Plugins()"}},
		{"response files", `  response_files: {prefix: "@"}
`, []string{"rotini.ResponseFiles()"}},
		{"chdir", `  flags:
    - {name: dir, identifiers: [-C], role: chdir, schema: {type: string}}
`, []string{"rotini.Chdir()"}},
		{"multicall", `  multicall: true
  commands:
    - {name: ls, summary: s}
`, []string{"rotini.Multicall()"}},
		{"stdin formats", `  commands:
    - name: a
      summary: s
      stdin: {format: yaml, schema: {type: object, properties: {x: {type: string}}}}
    - name: b
      summary: s
      stdin: {format: lines, schema: {type: "[]string"}}
    - name: c
      summary: s
      stdin: {format: jsonl, stream: true, schema: {type: object, properties: {x: {type: string}}}}
`, []string{`rotini.Stdin("lines", "jsonl", "yaml")`, "rotini.Schemas()", "rotini.JSON()", "rotini.YAML()"}},
		{"stdin default json", `  stdin: {schema: {type: string}}
`, []string{`rotini.Stdin("json")`, "rotini.Schemas()", "rotini.JSON()"}},
		{"env", `  env:
    - {name: home, schema: {type: string}}
`, []string{"rotini.Env()"}},
		{"config yaml", `  config_files:
    - {name: app, path: ./app.yaml}
  config:
    - {name: region, schema: {type: string}}
`, []string{"rotini.ConfigFiles()", "rotini.YAML()"}},
		{"config formats", `  config_files:
    - {name: a, path: ./a.toml}
    - {name: b, discover: {strategy: walk-up, file: .b.jsonc}}
    - {name: c, path: ./c, format: json}
`, []string{"rotini.ConfigFiles()", "rotini.JSON()", "rotini.TOML()", "rotini.JSONC()"}},
		{"config path chosen at run time", `  config_files:
    - {name: app, path: ./app.yaml}
  flags:
    - {name: config, identifiers: [--config], schema: {type: string, config_source: app}}
`, []string{"rotini.ConfigFiles()", "rotini.UserHomes()", "rotini.JSON()", "rotini.YAML()", "rotini.TOML()", "rotini.JSONC()", "rotini.Dotenv()"}},
		{"profiles", `  config_files:
    - {name: app, path: ./app.yaml, profiles: {select: profile, under: profiles}}
  flags:
    - {name: profile, identifiers: [--profile], schema: {type: string}}
`, []string{"rotini.ConfigFiles()", "rotini.Profiles()", "rotini.YAML()"}},
		{"config schema", `  config_files:
    - {name: app, path: ./app.json, schema: {type: object}}
`, []string{"rotini.ConfigFiles()", "rotini.Schemas()", "rotini.JSON()"}},
		{".env file", `  config_files:
    - {name: local, path: .env, as: env}
`, []string{"rotini.ConfigFiles()", "rotini.EnvFiles()", "rotini.Dotenv()"}},
		{"variable file", `  env:
    - {name: token, schema: {type: string, variable_file: TOKEN_FILE}}
`, []string{"rotini.Env()", "rotini.EnvFiles()"}},
		{"fallbacks", `  flags:
    - {name: region, identifiers: [--region], schema: {type: string, variable: REGION}}
`, []string{"rotini.Fallbacks()"}},
		{"argument fallback", `  arguments:
    - {name: target, schema: {type: string, key: target}}
`, []string{"rotini.Fallbacks()"}},
		{"expand", `  flags:
    - {name: root, identifiers: [--root], schema: {type: string, expand: [home]}}
`, []string{"rotini.PathExpansion()"}},
		{"another user's home", `  config_files:
    - {name: app, path: ~bob/app.yaml}
`, []string{"rotini.ConfigFiles()", "rotini.UserHomes()", "rotini.YAML()"}},
		{"another user's home and an expansion", `  config_files:
    - {name: app, path: ~bob/app.yaml}
  flags:
    - {name: root, identifiers: [--root], schema: {type: string, expand: [home]}}
`, []string{"rotini.ConfigFiles()", "rotini.PathExpansion()", "rotini.YAML()"}},
		{"object flag", `  flags:
    - {name: db, identifiers: [--db], schema: {$ref: "#/schemas/DB"}}
  schemas:
    DB: {type: object, properties: {host: {type: string}}}
`, []string{"rotini.ObjectFlags()", "rotini.Schemas()", "rotini.JSON()", "rotini.YAML()"}},
		{"bounds", `  flags:
    - {name: port, identifiers: [--port], schema: {type: int, minimum: 1}}
`, []string{"rotini.Bounds()"}},
		{"unique items", `  env:
    - {name: tags, schema: {type: "[]string", uniqueItems: true}}
`, []string{"rotini.Env()", "rotini.UniqueItems()"}},
		{"unique items beside bounds", `  flags:
    - {name: tag, identifiers: [--tag], schema: {type: "[]string", minItems: 1, uniqueItems: true}}
  arguments:
    - {name: n, schema: {type: "[]int", uniqueItems: true}}
`, []string{"rotini.Bounds()", "rotini.UniqueItems()"}},
		{"unique objects", `  flags:
    - {name: mount, identifiers: [--mount], schema: {type: array, items: {$ref: "#/schemas/Mount"}, uniqueItems: true}}
  schemas:
    Mount: {type: object, properties: {src: {type: string}}}
`, []string{"rotini.ObjectFlags()", "rotini.Schemas()", "rotini.JSON()", "rotini.YAML()", "rotini.UniqueItems()"}},
		{"pattern", `  config_files:
    - {name: app, path: ./app.yaml}
  config:
    - {name: name, schema: {type: string, pattern: "^a"}}
`, []string{"rotini.ConfigFiles()", "rotini.YAML()", "rotini.Patterns()"}},
		{"flag groups", `  flags:
    - {name: a, identifiers: [--a], schema: {type: bool}}
    - {name: b, identifiers: [--b], schema: {type: bool}}
  flag_groups:
    - {kind: mutually_exclusive, flags: [a, b]}
`, []string{"rotini.FlagGroups()"}},
		{"flag dependencies", `  flags:
    - {name: a, identifiers: [--a], schema: {type: bool}}
    - {name: b, identifiers: [--b], schema: {type: bool}}
  flag_dependencies:
    - {when: a, requires: [b]}
`, []string{"rotini.FlagDependencies()"}},
		{"stream paths", `  arguments:
    - {name: src, schema: {type: inputfile}}
`, []string{"rotini.StreamPaths()"}},
		{"value types", `  flags:
    - {name: u, identifiers: [--u], schema: {type: url}}
    - {name: e, identifiers: [--e], schema: {type: "[]email"}}
    - {name: z, identifiers: [--z], schema: {type: timezone, nullable: true}}
    - {name: m, identifiers: [--m], schema: {type: "map[string]mac"}}
    - {name: h, identifiers: [--h], schema: {type: array, items: {type: hostport}}}
    - {name: r, identifiers: [--r], schema: {type: regexp}}
`, []string{"rotini.URLValues()", "rotini.EmailValues()", "rotini.TimeZoneValues()", "rotini.MACValues()", "rotini.IPValues()", "rotini.RegexpValues()"}},
		{"value type in an env input", `  env:
    - {name: net, schema: {type: cidr}}
`, []string{"rotini.Env()", "rotini.IPValues()"}},
		{"value type through a named schema", `  flags:
    - {name: home, identifiers: [--home], schema: {$ref: "#/schemas/Home"}}
  schemas:
    Home: {type: url}
`, []string{"rotini.URLValues()"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wiredFor(t, tc.body); !slices.Equal(got, tc.want) {
				t.Errorf("wired\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

// Each fixture's feature list is pinned; a change shows up as a diff of the list.
func TestWiredFeatures_fixtures(t *testing.T) {
	specs := map[string]string{
		"program":   filepath.Join("testdata", "program", "spec.yaml"),
		"cfgctl":    filepath.Join("testdata", "program", "cfgctl.yaml"),
		"carapace":  filepath.Join("testdata", "program", "carapace.yaml"),
		"companion": filepath.Join("..", "..", "cmd", "rotini", ".rotini.spec.yaml"),
	}
	shapes, err := filepath.Glob(filepath.Join("testdata", "stubshapes", "*", "spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range shapes {
		specs["stubshape-"+filepath.Base(filepath.Dir(s))] = s
	}
	specs["demo"] = ""
	for name, path := range specs {
		t.Run(name, func(t *testing.T) {
			var spec *Spec
			specPath := path
			if path == "" {
				spec, specPath = decodeSpecYAML(t, goldenSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml")
			} else {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				spec = decodeSpecYAML(t, string(body))
			}
			abs, err := filepath.Abs(specPath)
			if err != nil {
				t.Fatal(err)
			}
			gp, err := resolveTree(spec, abs, "example.com/app")
			if err != nil {
				t.Fatal(err)
			}
			gp.spec = spec
			got := strings.Join(wiredFeatures(gp), "\n") + "\n"
			golden := filepath.Join("testdata", "wiring", name+".txt")
			if *updateWiring {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read %s (run with -update-wiring to create it): %v", golden, err)
			}
			if got != string(want) {
				t.Errorf("wired features differ from %s:\n--- got ---\n%s--- want ---\n%s", golden, got, want)
			}
		})
	}
}

// A composed child's declarations are wired into the parent that grafts it, even when the
// parent declares nothing itself, and the child's own NewProgram wires them too.
func TestWiredFeatures_composedChild(t *testing.T) {
	tree := transitiveTree()
	tree["cmd/grand/.rotini.spec.yaml"] = `version: 0.0.0
command:
  name: grand
  summary: the grandchild
  flags:
    - name: home
      identifiers: [--home]
      schema: {type: url}
  env:
    - name: token
      schema: {type: string}
`
	emitted := composeModuleStaged(t, tree)
	for _, file := range []string{"internal/cmd/root/zz_root.go", "internal/cmd/child/zz_child.go", "internal/cmd/grand/zz_grand.go"} {
		src, ok := emitted[file]
		if !ok {
			t.Fatalf("%s was not generated; got %v", file, keysOf(emitted))
		}
		for _, want := range []string{"\t\trotini.Env(),\n", "\t\trotini.URLValues(),\n"} {
			if !strings.Contains(src, want) {
				t.Errorf("%s does not wire %q", file, strings.TrimSpace(want))
			}
		}
	}
}
