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

// envNamesTree is a root with its own prefix mounting a child with another, which mounts a
// grandchild with none; a command written beside the child's $ref belongs to the root.
func envNamesTree(childPrefix string) map[string]string {
	conf := func(name string) string {
		return "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/" + name +
			"/zz_" + name + ".go\n      package: " + name + "\n  features:\n    - type: help\n      enabled: true\n"
	}
	return map[string]string{
		"cmd/grand/.rotini.spec.yaml": `version: 0.0.0
command:
  name: grand
  summary: the grandchild
  env:
    - name: deep
      schema: { type: string }
`,
		"cmd/grand/.rotini.conf.yaml": conf("grand"),
		"cmd/child/.rotini.spec.yaml": `version: 0.0.0
command:
  name: child
  summary: the child
` + childPrefix + `  flags:
    - name: region
      identifiers: [--region]
      cascading: true
      schema: { type: string, key: region }
  env:
    - name: home
      schema: { type: string }
  commands:
    - $ref: ../grand/.rotini.spec.yaml
`,
		"cmd/child/.rotini.conf.yaml": conf("child"),
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: the root
  env_prefix: ROOT
  env:
    - name: token
      schema: { type: string }
  commands:
    - $ref: ../child/.rotini.spec.yaml
      commands:
        - name: extra
          summary: written beside the $ref
`,
		"cmd/root/.rotini.conf.yaml": conf("root"),
	}
}

// TestCompose_childKeepsItsEnvNames pins that the parent's pages and types name the variables a
// mounted child's own code reads, derived under the child's prefix, and a grandchild's under
// none, while the parent's own inputs keep the parent's prefix.
func TestCompose_childKeepsItsEnvNames(t *testing.T) {
	emitted := composeModuleStaged(t, envNamesTree("  env_prefix: KID\n"))
	root := emitted["internal/cmd/root/zz_root.go"]
	for _, want := range []string{"KID_HOME", "KID_REGION", "DEEP", "ROOT_TOKEN",
		`env:"KID_REGION"`} { // the child's frame in the inputs of the command beside the $ref
		if !strings.Contains(root, want) {
			t.Errorf("the root's generated code does not name %s", want)
		}
	}
	for _, unwanted := range []string{"ROOT_HOME", "ROOT_REGION", "ROOT_DEEP", "KID_DEEP"} {
		if strings.Contains(root, unwanted) {
			t.Errorf("the root's generated code names %s, which no command reads", unwanted)
		}
	}
	if !strings.Contains(emitted["internal/cmd/child/zz_child.go"], `env:"KID_HOME"`) ||
		!strings.Contains(emitted["internal/cmd/grand/zz_grand.go"], `env:"DEEP"`) {
		t.Error("the child's and grandchild's own types do not read KID_HOME and DEEP, the names the root's pages list")
	}
}

// TestCompose_childsAdoptedPrefixIsItsOwn pins that a child declaring no env_prefix, whose own
// generate run adopts its grandchild's, is listed under that adopted prefix by the parent.
func TestCompose_childsAdoptedPrefixIsItsOwn(t *testing.T) {
	files := envNamesTree("")
	files["cmd/grand/.rotini.spec.yaml"] = strings.Replace(files["cmd/grand/.rotini.spec.yaml"],
		"  summary: the grandchild\n", "  summary: the grandchild\n  env_prefix: GR\n", 1)
	emitted := composeModuleStaged(t, files)
	child, root := emitted["internal/cmd/child/zz_child.go"], emitted["internal/cmd/root/zz_root.go"]
	if !strings.Contains(child, `env:"GR_HOME"`) {
		t.Fatalf("the child did not adopt its grandchild's prefix:\n%s", child)
	}
	for _, want := range []string{"GR_HOME", "GR_REGION", "GR_DEEP"} {
		if !strings.Contains(root, want) {
			t.Errorf("the root's generated code does not name %s", want)
		}
	}
	if strings.Contains(root, "ROOT_HOME") || strings.Contains(root, "ROOT_DEEP") {
		t.Error("the root named the child's inputs under its own prefix")
	}
}

// TestPinEnvNames pins which inputs get their variable written out: derived names under the
// prefix, an explicit variable kept as written, an argv-only flag and a nested $ref untouched.
func TestPinEnvNames(t *testing.T) {
	explicit := &InputSchema{Variable: "GITHUB_TOKEN"}
	c := pinEnvNames(Command{
		Name: "kid",
		Flags: []FlagInput{
			{Name: "region", Schema: &InputSchema{Key: "api.region"}},
			{Name: "token", Schema: explicit},
			{Name: "verbose", Schema: &InputSchema{Type: "bool"}},
		},
		Env: []EnvInput{{Name: "home"}},
		Commands: []Command{
			{Name: "show", Arguments: []ArgumentInput{{Name: "target", Schema: &InputSchema{Key: "target"}}}},
			{Ref: "../grand/.rotini.spec.yaml"},
		},
	}, "KID")
	got := []string{
		envVarOf(c.Flags[0].Schema), envVarOf(c.Flags[1].Schema), envVarOf(c.Flags[2].Schema),
		envVarOf(c.Env[0].Schema), envVarOf(c.Commands[0].Arguments[0].Schema),
	}
	want := []string{"KID_API_REGION", "GITHUB_TOKEN", "", "KID_HOME", "KID_TARGET"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("pinned variables = %q, want %q", got, want)
	}
	if c.Flags[1].Schema != explicit || c.Commands[1].Ref == "" {
		t.Error("an explicit variable or a nested $ref was rewritten")
	}
}

// TestCompose_prefixedParentTakesChildrenWithDifferentPrefixes pins that two children with
// different env_prefix values compose under a parent that declares its own, each keeping its
// names.
func TestCompose_prefixedParentTakesChildrenWithDifferentPrefixes(t *testing.T) {
	files := map[string]string{}
	for _, c := range []struct{ name, prefix string }{{"grand", "ONE"}, {"child", "TWO"}} {
		files["cmd/"+c.name+"/.rotini.spec.yaml"] = "version: 0.0.0\ncommand:\n  name: " + c.name +
			"\n  summary: a child\n  env_prefix: " + c.prefix + "\n  env:\n    - name: token\n      schema: { type: string }\n"
		files["cmd/"+c.name+"/.rotini.conf.yaml"] = "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/" +
			c.name + "/zz_" + c.name + ".go\n      package: " + c.name + "\n  features:\n    - type: help\n      enabled: true\n"
	}
	files["cmd/root/.rotini.spec.yaml"] = "version: 0.0.0\ncommand:\n  name: root\n  summary: the root\n  env_prefix: ROOT\n" +
		"  commands:\n    - $ref: ../grand/.rotini.spec.yaml\n    - $ref: ../child/.rotini.spec.yaml\n"
	files["cmd/root/.rotini.conf.yaml"] = strings.ReplaceAll(files["cmd/child/.rotini.conf.yaml"], "child", "root")
	root := composeModuleStaged(t, files)["internal/cmd/root/zz_root.go"]
	if !strings.Contains(root, "ONE_TOKEN") || !strings.Contains(root, "TWO_TOKEN") || strings.Contains(root, "ROOT_TOKEN") {
		t.Errorf("want ONE_TOKEN and TWO_TOKEN, and no ROOT_TOKEN, in:\n%s", root)
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

// TestCompose_childrenSharingARootName pins that two composed children whose specs share a root
// name, mounted under different names, import under different aliases and each delegate to
// their own package.
func TestCompose_childrenSharingARootName(t *testing.T) {
	childConf := func(dir string) string {
		return "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/" + dir + "/zz_db.go\n      package: db\n"
	}
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/grand/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: db\n  commands:\n    - name: dump\n",
		"cmd/grand/.rotini.conf.yaml": childConf("grand"),
		"cmd/child/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: db\n  commands:\n    - name: load\n",
		"cmd/child/.rotini.conf.yaml": childConf("child"),
		"cmd/root/.rotini.spec.yaml": "version: 0.0.0\ncommand:\n  name: root\n  commands:\n" +
			"    - $ref: ../grand/.rotini.spec.yaml\n    - $ref: ../child/.rotini.spec.yaml\n      name: legacy\n",
		"cmd/root/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n      package: root\n",
	})
	root := emitted["internal/cmd/root/zz_root.go"]
	for _, want := range []string{
		`dbcli "example.com/compose/internal/cmd/grand"`,
		`dbcli2 "example.com/compose/internal/cmd/child"`,
		"dbcli.Handlers().DbDump()",
		"dbcli2.Handlers().DbLoad()",
		"(*dbcli2.DbLoadInputs)(nil)",
	} {
		if !strings.Contains(root, want) {
			t.Errorf("root does not contain %s:\n%s", want, root)
		}
	}
}

// TestCompose_hiddenAliasClaimsAComposedName pins that a sibling's hidden alias taking a composed
// child's name is a collision, as it is between inline commands: dispatch would never reach the
// child.
func TestCompose_hiddenAliasClaimsAComposedName(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
	writeTestFile(t, dir, "kid/.rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: kid\n")
	writeTestFile(t, dir, "app/.rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: app\n  commands:\n"+
		"    - name: old\n      hidden_aliases: [kid]\n    - $ref: ../kid/.rotini.spec.yaml\n")
	t.Chdir(dir)

	spec, err := readSpec("app/.rotini.spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTree(spec, "app/.rotini.spec.yaml", "example.com/compose"); err == nil ||
		!strings.Contains(err.Error(), `duplicate command name or alias "kid"`) {
		t.Fatalf("err = %v, want the hidden alias reported", err)
	}
}

// TestCompose_pluginShadowedByAComposedChild pins that a composed child taking the name of a
// plugin declared beside it is reported, at the root and below an inline command, as it is
// for an inline command: sub-commands dispatch first, so the plugin could never run.
func TestCompose_pluginShadowedByAComposedChild(t *testing.T) {
	for name, parent := range map[string]string{
		"root":   "  plugins:\n    - name: kid\n  commands:\n    - $ref: ../kid/.rotini.spec.yaml\n",
		"inline": "  commands:\n    - name: tools\n      plugins:\n        - name: other\n          aliases: [kid]\n      commands:\n        - $ref: ../kid/.rotini.spec.yaml\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, "go.mod", "module example.com/compose\n\ngo 1.26\n")
			writeTestFile(t, dir, "kid/.rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: kid\n")
			writeTestFile(t, dir, "app/.rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: app\n"+parent)
			t.Chdir(dir)

			spec, err := readSpec("app/.rotini.spec.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolveTree(spec, "app/.rotini.spec.yaml", "example.com/compose"); err == nil ||
				!strings.Contains(err.Error(), `also dispatches as "kid"`) {
				t.Fatalf("err = %v, want the shadowed plugin reported", err)
			}
		})
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

// TestLintComposedTree_missingRefWithoutModule pins that validate reports a local $ref naming no
// file when there is no module for the composer to run in, as generate does.
func TestLintComposedTree_missingRefWithoutModule(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	spec := &Spec{Command: Command{Name: "app", Commands: []Command{{Ref: "../status/.rotini.spec.yaml"}}}}
	problems := lintComposedTree(spec, filepath.Join(dir, "app", ".rotini.spec.yaml"))
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), `compose "../status/.rotini.spec.yaml"`) || !strings.Contains(problems[0].Error(), "file does not exist") {
		t.Errorf("problems = %v, want one naming the missing ref", problems)
	}
	if err := os.MkdirAll(filepath.Join(dir, "status"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status", ".rotini.spec.yaml"), []byte("version: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if problems := lintComposedTree(spec, filepath.Join(dir, "app", ".rotini.spec.yaml")); len(problems) != 0 {
		t.Errorf("with the file in place: %v", problems)
	}
	// A $ref naming a directory is reported too, as the composer reports it.
	dirSpec := &Spec{Command: Command{Name: "app", Commands: []Command{{Ref: "../status"}}}}
	if problems := lintComposedTree(dirSpec, filepath.Join(dir, "app", ".rotini.spec.yaml")); len(problems) != 1 || !strings.Contains(problems[0].Error(), `compose "../status"`) {
		t.Errorf("a directory: %v, want one problem naming the ref", problems)
	}
}
