package codegen

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// `rotini init --template <shape>` starts from a shape other than the plain seed:
//
//   - plugin: a kubectl-style plugin (<host>-<name>), with display_name, completion on, and
//     multicall answering the host's <host>_complete-<name> completion requests;
//   - daemon: a long-running `serve` command whose seeded handler loops until its context ends
//     (Ctrl-C, SIGTERM or --for), then tears down in PostRun;
//   - suite: two binaries, <name> and <name>-admin, both composing a shared `status` child
//     from its own spec.
//
// Each shape's specs and confs are YAML templates under templates/init/<shape>, transcoded to
// the chosen format like the plain seed.

//go:embed templates/init
var initTemplates embed.FS

// initTemplateNames are the shapes `--template` accepts, in the order help lists them.
var initTemplateNames = []string{"plugin", "daemon", "suite"}

// templateInitData is the data for a shape's templates.
type templateInitData struct {
	Version string // the seed's `version`
	Name    string // the CLI being initialized: its root command, cmd/<Name> and binary name
	Ext     string // the spec and conf extension: yaml, json, jsonc or toml
	Module  string // the Go module's import path

	// plugin
	Display      string // the root's display_name, e.g. "kubectl unready"
	Host         string // the program that runs the plugin, e.g. "kubectl"
	CompleteName string // the name the host runs for completion, e.g. "kubectl_complete-unready"

	// suite
	Admin bool   // rendering the <name>-admin binary's spec
	Child string // the shared child's name

	// the seeded handler (daemon)
	Package string // the cmd package's name
	Handler string // the handler type
	Inputs  string // the command's inputs type
	Frame   string // the command's frame in its inputs type
}

// initCLI is one CLI a shape writes under cmd/<name>: its seed spec and conf, and any files
// the shape seeds itself (created once, before generate, which then keeps them).
type initCLI struct {
	name  string
	spec  func(fileFormat) ([]byte, error)
	conf  func(fileFormat) ([]byte, error)
	files []initFile
}

// initFile is a file a shape seeds, at a module-root-relative path.
type initFile struct {
	path    string
	content []byte
}

// initCLIs returns the CLIs to write for template ("" for the plain seed), in the order they
// must be generated: a composed child before the binaries that compose it.
func initCLIs(template string, d templateInitData) ([]initCLI, error) {
	plainConf := func(name string) func(fileFormat) ([]byte, error) {
		return func(f fileFormat) ([]byte, error) { return renderSeedFile("conf", templateConf, d.Version, name, f) }
	}
	switch template {
	case "":
		return []initCLI{{
			name: d.Name,
			spec: func(f fileFormat) ([]byte, error) { return renderSpecFile(d.Version, d.Name, f) },
			conf: plainConf(d.Name),
		}}, nil
	case "plugin":
		host, plugin, ok := strings.Cut(d.Name, "-")
		if !ok || host == "" || plugin == "" {
			return nil, fmt.Errorf("a plugin's name is <host>-<plugin>, such as kubectl-unready; got %q", d.Name)
		}
		d.Host = host
		d.Display = host + " " + strings.ReplaceAll(strings.ReplaceAll(plugin, "-", " "), "_", "-")
		d.CompleteName = host + "_complete-" + strings.ReplaceAll(plugin, "-", "_")
		return []initCLI{{
			name: d.Name,
			spec: initSeed("plugin/spec.yaml.tmpl", d),
			conf: initSeed("plugin/conf.yaml.tmpl", d),
		}}, nil
	case "daemon":
		pascal := toPascalCase(d.Name)
		d.Package = goPkgName(d.Name)
		d.Handler = lowerFirst(pascal) + toPascalCase("serve") + "Handler"
		d.Frame = pascal + toPascalCase("serve")
		d.Inputs = d.Frame + "Inputs"
		handler, err := initGoFile("daemon/serve.go.tmpl", d)
		if err != nil {
			return nil, err
		}
		return []initCLI{{
			name: d.Name,
			spec: initSeed("daemon/spec.yaml.tmpl", d),
			conf: plainConf(d.Name),
			files: []initFile{{
				path:    "internal/cmd/" + d.Name + "/" + commandStubFilename(d.Name, "serve", ""),
				content: handler,
			}},
		}}, nil
	case "suite":
		d.Child = "status"
		admin := d.Name + "-admin"
		if d.Name == d.Child || admin == d.Child {
			return nil, fmt.Errorf("a suite can't be named %q: its shared child is", d.Child)
		}
		// The first binary's go:generate regenerates the shared child before itself, and the
		// second binary sorts after it, so `go generate ./...` keeps all three in step.
		main, err := renderMainFile("", d.Module+"/internal/cmd/"+d.Name, "cmd", d.Ext)
		if err != nil {
			return nil, err
		}
		child := fmt.Sprintf("//go:generate go tool rotini generate ../%[1]s/.rotini.spec.%[2]s --config ../%[1]s/.rotini.conf.%[2]s\n", d.Child, d.Ext)
		adminData := d
		adminData.Admin = true
		return []initCLI{
			{
				name: d.Child,
				spec: initSeed("suite/child.spec.yaml.tmpl", d),
				conf: initSeed("suite/child.conf.yaml.tmpl", d),
			},
			{
				name:  d.Name,
				spec:  initSeed("suite/spec.yaml.tmpl", d),
				conf:  plainConf(d.Name),
				files: []initFile{{path: "cmd/" + d.Name + "/main.go", content: append([]byte(child), main...)}},
			},
			{
				name: admin,
				spec: initSeed("suite/spec.yaml.tmpl", adminData),
				conf: plainConf(admin),
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown template %q (want %s)", template, strings.Join(initTemplateNames, ", "))
	}
}

// initSeed renders one of a shape's YAML seed templates and transcodes it to the chosen format.
func initSeed(file string, d templateInitData) func(fileFormat) ([]byte, error) {
	return func(f fileFormat) ([]byte, error) {
		text, err := initTemplates.ReadFile("templates/init/" + file)
		if err != nil {
			return nil, fmt.Errorf("read the %s template: %w", file, err)
		}
		rendered, err := renderTemplate(file, string(text), d)
		if err != nil {
			return nil, err
		}
		return convert(rendered, f)
	}
}

// initGoFile renders one of a shape's Go templates, gofmt'd.
func initGoFile(file string, d templateInitData) ([]byte, error) {
	text, err := initTemplates.ReadFile("templates/init/" + file)
	if err != nil {
		return nil, fmt.Errorf("read the %s template: %w", file, err)
	}
	return renderGoFileWithHeader("", file, string(text), d)
}

// seedOverlay holds the files a dry-run init has planned but not written, by absolute path, so
// a template's later CLIs can read the specs of the earlier ones they compose.
var seedOverlay struct {
	sync.Mutex

	files map[string][]byte
}

// withSeedOverlay serves files to [readRaw] until the returned function is called.
func withSeedOverlay(files map[string][]byte) func() {
	seedOverlay.Lock()
	seedOverlay.files = files
	seedOverlay.Unlock()
	return func() {
		seedOverlay.Lock()
		seedOverlay.files = nil
		seedOverlay.Unlock()
	}
}

// seedOverlayRead returns the planned content of path while a dry-run init is running.
func seedOverlayRead(path string) ([]byte, bool) {
	seedOverlay.Lock()
	defer seedOverlay.Unlock()
	if seedOverlay.files == nil {
		return nil, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	data, ok := seedOverlay.files[abs]
	return data, ok
}
