package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the composer in-process, asserting the shape of what it emits. The e2e
// tier covers dispatch of the built binary.

// transitiveTree is a three-level tree: root composes child, child composes grand.
func transitiveTree() map[string]string {
	return map[string]string{
		"cmd/grand/.rotini.spec.yaml": `version: 0.0.0
command:
  name: grand
  summary: the grandchild, as it describes itself
  flags:
    - name: loud
      identifiers:
        - --loud
      schema:
        type: bool
`,
		"cmd/grand/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/grand/zz_grand.go
      package: grand
`,
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
  commands:
    - $ref: ../grand/.rotini.spec.yaml
`,
		"cmd/child/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/child/zz_child.go
      package: child
`,
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - $ref: ../child/.rotini.spec.yaml
      summary: as the parent describes it
`,
		"cmd/root/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/root/zz_root.go
      package: root
`,
	}
}

// TestCompose_transitiveRef pins composeNestedRef: the grandchild is grafted into the root's
// tree and delegates through the direct child's package, with no types or stubs at the root.
func TestCompose_transitiveRef(t *testing.T) {
	emitted := composeModuleStaged(t, transitiveTree())

	root, ok := emitted["internal/cmd/root/zz_root.go"]
	if !ok {
		t.Fatalf("root package was not generated; got %v", keysOf(emitted))
	}

	// 1. The whole tree is present in the root's Definition, two levels deep.
	for _, want := range []string{`"child"`, `"grand"`} {
		if !strings.Contains(root, want) {
			t.Errorf("root Definition does not contain %s:\n%s", want, root)
		}
	}

	// 2. The parent's overlay summary wins over the child's own.
	if !strings.Contains(root, "as the parent describes it") {
		t.Error("the parent's overlay summary did not win over the child's own")
	}

	// 3. The grandchild delegates through the direct child's package; the root imports the
	//    grandchild's package only for its types.
	if !strings.Contains(root, "internal/cmd/child") {
		t.Error("root does not import the direct child's package")
	}
	if !strings.Contains(root, "childcli.Handlers().ChildGrand()") || strings.Contains(root, "grandcli.Handlers()") {
		t.Error("the grandchild does not delegate through the child — a transitive ref should")
	}

	// 4. No stub or input types are emitted at the root for a composed node.
	if _, ok := emitted["internal/cmd/root/root_child.go"]; ok {
		t.Error("a stub was seeded at the root for a composed command")
	}
	if strings.Contains(root, "type GrandInputs") || strings.Contains(root, "type ChildInputs") {
		t.Error("composed commands' input types were re-emitted at the root")
	}

	// 5. The root's own stub is seeded.
	if _, ok := emitted["internal/cmd/root/root.go"]; !ok {
		t.Errorf("the root's own handler stub was not seeded; got %v", keysOf(emitted))
	}
}

// TestCompose_cycleIsRejected pins that a $ref back to a spec already being composed is
// reported as a cycle.
func TestCompose_cycleIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	writeTestFile(t, dir, "a/.rotini.spec.yaml", `version: 0.0.0
command:
  name: a
  commands:
    - $ref: ../b/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "b/.rotini.spec.yaml", `version: 0.0.0
command:
  name: b
  commands:
    - $ref: ../a/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "a/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/a/zz_a.go
      package: a
`)
	t.Chdir(dir)

	err := NewProcessor("0.0.0").Generate("a/.rotini.spec.yaml", "a/.rotini.conf.yaml", false, func(string, error) {}, func([]error) {})
	if err == nil {
		t.Fatal("a cyclic $ref generated successfully, want an error")
	}
	if !strings.Contains(err.Error(), "cyclic") {
		t.Errorf("error %q does not say the refs are cyclic", err)
	}
}

// stageOrder is the dependency order the staged generate walks: deepest child first, root
// last, restricted to the specs the caller actually supplied.
func stageOrder(files map[string]string) []string {
	var out []string
	for _, name := range []string{"grand", "child", "root"} {
		if _, ok := files["cmd/"+name+"/.rotini.spec.yaml"]; ok {
			out = append(out, name)
		}
	}
	return out
}

// composeModuleStaged generates each supplied spec in dependency order (grandchild, child,
// root) and returns everything emitted.
func composeModuleStaged(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for path, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	for _, name := range stageOrder(files) {
		spec := "cmd/" + name + "/.rotini.spec.yaml"
		conf := "cmd/" + name + "/.rotini.conf.yaml"
		if err := NewProcessor("0.0.0").Generate(spec, conf, false, func(string, error) {}, func([]error) {}); err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
	}
	return collectGoFiles(t, dir)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── a composed child's InputSettings ──────────────────────────────────────────────

// TestCompose_adoptsChildInputSettings pins that a composed child's env_prefix is adopted and
// its config_files are re-scoped to the graft's path in the parent's InputSettings.
func TestCompose_adoptsChildInputSettings(t *testing.T) {
	emitted := composeModuleStaged(t, inputSettingsTree(`version: 0.0.0
command:
  name: root
  summary: an umbrella declaring no channels of its own
  commands:
    - $ref: ../child/.rotini.spec.yaml
      name: kid
`))

	root := emitted["internal/cmd/root/zz_root.go"]

	// The umbrella declares no prefix, so the child's is adopted.
	if !strings.Contains(root, `EnvPrefix: "APP"`) {
		t.Errorf("the composed child's env_prefix was not adopted:\n%s", root)
	}

	// The child's source is re-scoped to the graft's path, using the overlay name "kid".
	if !strings.Contains(root, `Scope: "root/kid"`) {
		t.Errorf("the composed child's config source was not re-scoped to root/kid:\n%s", root)
	}
	if strings.Contains(root, `Scope: "child"`) {
		t.Error("the child's own scope survived the graft; the input reader matches Scope against the invoked chain")
	}
}

// TestCompose_ownEnvPrefixWinsOverAdopted pins that an umbrella's own env_prefix is kept over a
// child's.
func TestCompose_ownEnvPrefixWinsOverAdopted(t *testing.T) {
	emitted := composeModuleStaged(t, inputSettingsTree(`version: 0.0.0
command:
  name: root
  summary: an umbrella with a prefix of its own
  env_prefix: ROOT
  commands:
    - $ref: ../child/.rotini.spec.yaml
      name: kid
`))

	root := emitted["internal/cmd/root/zz_root.go"]
	if !strings.Contains(root, `EnvPrefix: "ROOT"`) {
		t.Errorf("the umbrella's own env_prefix did not win:\n%s", root)
	}
}

// TestCompose_conflictingEnvPrefixesAreReported pins that children with different env_prefix
// values are an error.
func TestCompose_conflictingEnvPrefixesAreReported(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	for _, c := range []struct{ name, prefix string }{{"one", "ONE"}, {"two", "TWO"}} {
		writeTestFile(t, dir, "cmd/"+c.name+"/.rotini.spec.yaml", `version: 0.0.0
command:
  name: `+c.name+`
  summary: a child with its own prefix
  env_prefix: `+c.prefix+`
  env:
    - name: token
      schema:
        type: string
`)
		writeTestFile(t, dir, "cmd/"+c.name+"/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/`+c.name+`/zz_`+c.name+`.go
      package: `+c.name+`
`)
	}
	writeTestFile(t, dir, "cmd/root/.rotini.spec.yaml", `version: 0.0.0
command:
  name: root
  summary: an umbrella composing two disagreeing children
  commands:
    - $ref: ../one/.rotini.spec.yaml
    - $ref: ../two/.rotini.spec.yaml
`)
	writeTestFile(t, dir, "cmd/root/.rotini.conf.yaml", `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/root/zz_root.go
      package: root
`)
	t.Chdir(dir)

	for _, name := range []string{"one", "two"} {
		if err := NewProcessor("0.0.0").Generate("cmd/"+name+"/.rotini.spec.yaml", "cmd/"+name+"/.rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
	}

	err := NewProcessor("0.0.0").Generate("cmd/root/.rotini.spec.yaml", "cmd/root/.rotini.conf.yaml", false, func(string, error) {}, func([]error) {})
	if err == nil {
		t.Fatal("two children with different env_prefix values generated successfully, want an error")
	}
	// The message must name both prefixes and the fix.
	for _, want := range []string{"ONE", "TWO", "env_prefix", "root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// inputSettingsTree is a child that declares both channels, plus whichever root spec the caller
// wants composed on top of it.
func inputSettingsTree(rootSpec string) map[string]string {
	return map[string]string{
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: a CLI declaring its own configuration sources
  env_prefix: APP
  config_files:
    - name: project
      discover:
        strategy: walk-up
        file: .apprc.yaml
  env:
    - name: endpoint
      schema:
        type: string
        default: https://example.test
`,
		"cmd/child/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/child/zz_child.go
      package: child
`,
		"cmd/root/.rotini.spec.yaml": rootSpec,
		"cmd/root/.rotini.conf.yaml": `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/root/zz_root.go
      package: root
`,
	}
}

// ── what a composed child dispatches ────────────────────────────────────────

// TestCompose_keepsWhatTheChildDispatches pins that a composed child keeps its root's plugins,
// plugin discovery, plugin path, and passthrough, with plugin binaries named after the child.
func TestCompose_keepsWhatTheChildDispatches(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/grand/.rotini.spec.yaml": `version: 0.0.0
command:
  name: grand
  summary: the grandchild
  plugins:
    - name: sync
`,
		"cmd/grand/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/grand/zz_grand.go\n      package: grand\n",
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
  plugin_path: ./child-plugins
  plugins:
    - name: deploy
  plugin_discovery: {}
  commands:
    - name: exec
      summary: runs a command
      passthrough: true
      arguments:
        - name: argv
          schema: {type: '[]string'}
    - name: tools
      summary: inline sub-command with its own plugin
      plugins:
        - name: lint
    - $ref: ../grand/.rotini.spec.yaml
`,
		"cmd/child/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/child/zz_child.go\n      package: child\n",
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  commands:
    - $ref: ../child/.rotini.spec.yaml
      name: kid
      plugin_path: ./parent-plugins
`,
		"cmd/root/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n      package: root\n",
	})
	root := emitted["internal/cmd/root/zz_root.go"]
	for _, want := range []string{
		`Binary: "child-deploy"`,                      // the composed root's plugin, named as the child names it
		`rotini.PluginDiscoveryDef{Prefix: "child-"}`, // its discovery, with the child's default prefix
		`"./parent-plugins"`,                          // plugin_path on the $ref node overlays the child's
		`Binary: "child-lint"`,                        // an inline sub-command inside the subtree: the child's name too
		`Binary: "grand-sync"`,                        // a transitive child's plugins keep its name
		`Passthrough: true`,                           // the child's passthrough command still forwards raw
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root definition missing %s:\n%s", want, root)
		}
	}
	if strings.Contains(root, `"root-deploy"`) || strings.Contains(root, `"root-lint"`) {
		t.Error("a composed child's plugin was renamed after the parent program")
	}
}

// TestCompose_passthroughRootSurvives pins that a child whose root is passthrough keeps
// Passthrough once composed.
func TestCompose_passthroughRootSurvives(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: a wrapper
  passthrough: true
  arguments:
    - name: argv
      schema: {type: '[]string'}
`,
		"cmd/child/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/child/zz_child.go\n      package: child\n",
		"cmd/root/.rotini.spec.yaml":  "version: 0.0.0\ncommand:\n  name: root\n  summary: the root\n  commands:\n    - $ref: ../child/.rotini.spec.yaml\n",
		"cmd/root/.rotini.conf.yaml":  "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n      package: root\n",
	})
	if root := emitted["internal/cmd/root/zz_root.go"]; !strings.Contains(root, "Passthrough: true") {
		t.Errorf("the composed passthrough root lost Passthrough:\n%s", root)
	}
}

// TestCompose_validatesComposedSpecs pins that validating a parent reports problems in composed
// children and grandchildren, positioned in their own files.
func TestCompose_validatesComposedSpecs(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{
		"cmd/grand/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: grand\n  summary: g\n  flags:\n    - name: n\n      identifiers: [--n]\n      schema: {type: int, default: abc}\n",
		"cmd/child/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: child\n  summary: c\n  bogus_key: 1\n  commands:\n    - $ref: ../grand/.rotini.spec.yaml\n    - $ref: ../root/.rotini.spec.yaml\n",
		"cmd/root/.rotini.spec.yaml":  "version: 0.0.0\ncommand:\n  name: root\n  summary: r\n  commands:\n    - $ref: ../child/.rotini.spec.yaml\n",
	} {
		writeTestFile(t, dir, path, body)
	}
	t.Chdir(dir)
	err := NewProcessor("0.0.0").Validate("cmd/root/.rotini.spec.yaml", "", false, "collect", "", nil, nil)
	if err == nil {
		t.Fatal("the parent validated clean over broken composed specs")
	}
	// Paths are reported in the OS's own spelling, so cmd\child\... on Windows.
	for _, want := range []string{
		filepath.FromSlash(`cmd/child/.rotini.spec.yaml:5:`), `unknown key "bogus_key"`,
		filepath.FromSlash(`cmd/grand/.rotini.spec.yaml:`), "`default` \"abc\" is not a valid integer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}
