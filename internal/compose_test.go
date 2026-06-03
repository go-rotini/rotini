package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	childSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: child
  commands:
    - name: greet
      inputs:
        arguments:
          - name: who
            schema: { type: string }
        flags:
          - name: loud
            identifiers: [--loud]
            schema: { type: bool }
`
	childConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  rth:
    package: cmd/child/rth
    file: handlers.go
  rtg:
    package: cmd/child/rtg
    file: rotini.go
`
	parentSpecYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: parent
  commands:
    - $ref: ../child/.rotini.spec.yaml
`
	parentConfYAML = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json
generate:
  rth:
    package: cmd/parent/rth
    file: handlers.go
  rtg:
    package: cmd/parent/rtg
    file: rotini.go
`
)

func TestGenerate_staticComposition(t *testing.T) {
	tmp := initTestModule(t) // module example.com/myclis, chdir'd
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), childConfYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), parentConfYAML)

	// Children must be generated before parents (the parent imports the child rth).
	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// Parent rtg: composed commands appear in the interface and the Definition,
	// but their input structs are NOT redeclared (they live in the child's rtg).
	rtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, rtg,
		"ParentChild() rotini.CommandHandlers",
		"ParentChildGreet() rotini.CommandHandlers",
		`Name: "child"`, `Handler: "ParentChild"`,
		`Name: "greet"`, `Handler: "ParentChildGreet"`,
		`{Name: "who", Type: "string"}`,
	)
	mustNotContain(t, rtg, "ParentChildGreetInputs", "ParentChildGreetFlags")

	// Parent rollup: imports the child rth (aliased) and delegates composed
	// commands to it; own root returns a local stub.
	rollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, rollup,
		`childrth "example.com/myclis/cmd/child/rth"`,
		"return childrth.Handlers().Child()",
		"return childrth.Handlers().ChildGreet()",
		"return &parentHandlers{}",
	)

	// No stub is created in the parent for a composed command.
	if _, err := os.Stat(filepath.Join(tmp, "cmd/parent/rth/parent_child_greet.go")); !os.IsNotExist(err) {
		t.Errorf("composed command should not get a parent stub: %v", err)
	}
	// The child remains a standalone CLI with its own typed inputs + Handlers().
	mustContain(t, filepath.Join(tmp, "cmd/child/rtg/rotini.go"), "type ChildGreetInputs struct")
	mustContain(t, filepath.Join(tmp, "cmd/child/rth/handlers.go"), "func Handlers() rtg.ProgramHandlers")
}

func TestGenerate_composeNameOverride(t *testing.T) {
	tmp := initTestModule(t)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.spec.yaml"), childSpecYAML)
	writeTestFile(t, filepath.Join(tmp, "cmd/child/.rotini.conf.yaml"), childConfYAML)
	// The parent grafts the child under a different name via `name:` on the $ref.
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: parent
  commands:
    - $ref: ../child/.rotini.spec.yaml
      name: kid
`
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.spec.yaml"), parentSpec)
	writeTestFile(t, filepath.Join(tmp, "cmd/parent/.rotini.conf.yaml"), parentConfYAML)

	if err := Generate("cmd/child/.rotini.spec.yaml", "cmd/child/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate child: %v", err)
	}
	if err := Generate("cmd/parent/.rotini.spec.yaml", "cmd/parent/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate parent: %v", err)
	}

	// The grafted command and method use the override name "kid"…
	rtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, rtg,
		"ParentKid() rotini.CommandHandlers",
		"ParentKidGreet() rotini.CommandHandlers",
		`Name: "kid"`, `Handler: "ParentKid"`,
		`Name: "greet"`, `Handler: "ParentKidGreet"`,
	)
	// …but delegation still targets the child's real handler methods (Child/ChildGreet).
	mustContain(t, filepath.Join(tmp, "cmd/parent/rth/handlers.go"),
		"return childrth.Handlers().Child()",
		"return childrth.Handlers().ChildGreet()",
	)
}

func TestGenerate_transitiveRef(t *testing.T) {
	tmp := initTestModule(t)
	conf := func(dir string) string {
		return "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
			"generate:\n  rth:\n    package: cmd/" + dir + "/rth\n    file: handlers.go\n" +
			"  rtg:\n    package: cmd/" + dir + "/rtg\n    file: rotini.go\n"
	}
	// grandchild (gc) has its own sub-command "ping"; child composes gc; parent
	// composes child — so the parent reaches gc transitively, through child.
	gcSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: gc
  commands:
    - name: ping
      inputs:
        arguments:
          - name: host
            schema: { type: string }
`
	childSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: child
  commands:
    - $ref: ../gc/.rotini.spec.yaml
`
	parentSpec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: parent
  commands:
    - $ref: ../child/.rotini.spec.yaml
`
	for dir, spec := range map[string]string{"gc": gcSpec, "child": childSpec, "parent": parentSpec} {
		writeTestFile(t, filepath.Join(tmp, "cmd/"+dir+"/.rotini.spec.yaml"), spec)
		writeTestFile(t, filepath.Join(tmp, "cmd/"+dir+"/.rotini.conf.yaml"), conf(dir))
	}

	// Dependency order: grandchild, then child, then parent.
	for _, dir := range []string{"gc", "child", "parent"} {
		if err := Generate("cmd/"+dir+"/.rotini.spec.yaml", "cmd/"+dir+"/.rotini.conf.yaml", false, nil); err != nil {
			t.Fatalf("generate %s: %v", dir, err)
		}
	}

	// The child composed gc directly (one level).
	mustContain(t, filepath.Join(tmp, "cmd/child/rth/handlers.go"),
		`gcrth "example.com/myclis/cmd/gc/rth"`,
		"return gcrth.Handlers().Gc()",
		"return gcrth.Handlers().GcPing()",
	)

	// The parent reaches gc transitively: its Definition + interface include the
	// grandchild commands…
	parentRtg := filepath.Join(tmp, "cmd/parent/rtg/rotini.go")
	mustContain(t, parentRtg,
		"ParentChildGc() rotini.CommandHandlers",
		"ParentChildGcPing() rotini.CommandHandlers",
		`Name: "gc"`, `Handler: "ParentChildGc"`,
		`Name: "ping"`, `Handler: "ParentChildGcPing"`,
		`{Name: "host", Type: "string"}`,
	)
	// …and its rollup delegates them to the *direct child* (which forwards to gc),
	// importing only the child's rth — never the grandchild's.
	parentRollup := filepath.Join(tmp, "cmd/parent/rth/handlers.go")
	mustContain(t, parentRollup,
		`childrth "example.com/myclis/cmd/child/rth"`,
		"return childrth.Handlers().Child()",
		"return childrth.Handlers().ChildGc()",
		"return childrth.Handlers().ChildGcPing()",
	)
	mustNotContain(t, parentRollup, "example.com/myclis/cmd/gc/rth", "gcrth")
}

func TestGenerate_cyclicRefErrors(t *testing.T) {
	tmp := initTestModule(t)
	// A spec that composes itself — the simplest cycle.
	selfRef := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: a
  commands:
    - $ref: ../a/.rotini.spec.yaml
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/a/rth\n  rtg:\n    package: cmd/a/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.spec.yaml"), selfRef)
	writeTestFile(t, filepath.Join(tmp, "cmd/a/.rotini.conf.yaml"), conf)

	err := Generate("cmd/a/.rotini.spec.yaml", "cmd/a/.rotini.conf.yaml", false, nil)
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("expected cyclic $ref error, got %v", err)
	}
}

func TestGenerate_channelConstraintTags(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: app
  inputs:
    env:
      - name: port
        schema: { type: int, minimum: 1, maximum: 65535 }
      - name: region
        schema: { type: string, minLength: 2, pattern: "^[a-z]+$" }
    config:
      - name: name
        schema: { type: string, key: app.name, maxLength: 5 }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/app/rth\n  rtg:\n    package: cmd/app/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The env/config struct fields carry the validation tags the binder reads.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		`min:"1"`, `max:"65535"`,
		`minlen:"2"`, `pattern:"^[a-z]+$"`,
		`maxlen:"5"`,
	)
}

func TestGenerate_stdinSchemaInBindMeta(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: app
  inputs:
    stdin:
      format: yaml
      schema:
        type: object
        properties:
          port: { type: integer, minimum: 1, maximum: 65535 }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/app/rth\n  rtg:\n    package: cmd/app/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// BindMeta carries the stdin payload schema keyed by the <Prefix>Stdin type name.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"),
		"var BindMeta", "StdinSchemas", `"AppStdin"`, "maximum", "65535",
	)
}

func TestGenerate_inputSchemaRef(t *testing.T) {
	tmp := initTestModule(t)
	// A document-level named schema referenced by an input via $ref: the input's Go
	// field takes the generated named type, and that type is emitted in the rtg.
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
schemas:
  Endpoint:
    type: object
    properties:
      host: { type: string }
      port: { type: integer }
command:
  name: app
  inputs:
    config:
      - name: server
        schema:
          $ref: "#/schemas/Endpoint"
          key: server
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/app/rth\n  rtg:\n    package: cmd/app/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	rtg := filepath.Join(tmp, "cmd/app/rtg/rotini.go")
	// The named type is generated, and the config field is typed as it (not string).
	mustContain(t, rtg,
		"type Endpoint struct",
		"Server Endpoint `",
	)
	mustNotContain(t, rtg, "Server string")
}

func TestGenerate_secretFlagDef(t *testing.T) {
	tmp := initTestModule(t)
	spec := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-spec.json
command:
  name: app
  inputs:
    flags:
      - name: token
        identifiers: [--token]
        schema: { type: string, secret: true }
`
	conf := "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n" +
		"generate:\n  rth:\n    package: cmd/app/rth\n  rtg:\n    package: cmd/app/rtg\n"
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.spec.yaml"), spec)
	writeTestFile(t, filepath.Join(tmp, "cmd/app/.rotini.conf.yaml"), conf)

	if err := Generate("cmd/app/.rotini.spec.yaml", "cmd/app/.rotini.conf.yaml", false, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The FlagDef carries Secret so the parser redacts the value in errors.
	mustContain(t, filepath.Join(tmp, "cmd/app/rtg/rotini.go"), `Name: "token"`, "Secret: true")
}

func mustNotContain(t *testing.T, path string, subs ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, s := range subs {
		if strings.Contains(string(data), s) {
			t.Errorf("%s unexpectedly contains %q", path, s)
		}
	}
}
