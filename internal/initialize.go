package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// schemaVersion is the version segment of the published schema URLs scaffolded
// into new spec/conf files. It must satisfy the `$schema` pattern in the
// embedded schemas. TODO: source this from the real released rotini version.
const schemaVersion = "0.0.0"

func specSchemaURL() string {
	return "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/" + schemaVersion + "/schema-spec.json"
}

func confSchemaURL() string {
	return "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/" + schemaVersion + "/schema-conf.json"
}

// Initialize scaffolds a new standalone rotini CLI named name under cmd/<name>/
// of the current module, then generates its framework and handler packages:
//
//	cmd/<name>/.rotini.spec.<fmt>   cmd/<name>/rtg/rotini.go
//	cmd/<name>/.rotini.conf.<fmt>   cmd/<name>/rth/handlers.go + stubs
//	cmd/<name>/main.go
//
// format selects the spec/conf serialization (yaml, jsonc, or json). The
// create-once files (spec, conf, main.go) are left untouched unless force is
// set; the generated framework/rollup files are always (re)written, and handler
// stubs are never overwritten.
func Initialize(name, format string, force bool) error {
	if name == "" {
		return errors.New("a CLI name is required")
	}
	ext, err := normalizeFormat(format)
	if err != nil {
		return err
	}

	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}
	cliDir := filepath.Join(moduleRoot, "cmd", name)
	specPath := filepath.Join(cliDir, ".rotini.spec."+ext)
	confPath := filepath.Join(cliDir, ".rotini.conf."+ext)
	mainPath := filepath.Join(cliDir, "main.go")

	if !force {
		for _, p := range []string{specPath, confPath, mainPath} {
			if _, statErr := os.Stat(p); statErr == nil {
				rel, _ := filepath.Rel(moduleRoot, p)
				return fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(rel))
			}
		}
	}

	if err := WriteSpec(specPath, scaffoldSpec(name)); err != nil {
		return err
	}
	if err := WriteConf(confPath, scaffoldConf(name)); err != nil {
		return err
	}
	if err := writeMainGo(mainPath, moduleName, name); err != nil {
		return err
	}
	return Generate(specPath, confPath)
}

// normalizeFormat resolves the requested format to a file extension, defaulting
// to yaml.
func normalizeFormat(format string) (string, error) {
	switch format {
	case "", "yaml", "yml":
		return "yaml", nil
	case "jsonc":
		return "jsonc", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("unsupported format %q (want yaml, jsonc, or json)", format)
	}
}

// scaffoldSpec builds a minimal valid spec: just the schema URL and root name.
// The user adds commands from there.
func scaffoldSpec(name string) *Spec {
	return &Spec{
		Schema: specSchemaURL(),
		Name:   name,
	}
}

// scaffoldConf builds the conf for the standard per-CLI layout under cmd/<name>.
func scaffoldConf(name string) *Conf {
	return &Conf{
		Schema: confSchemaURL(),
		Generate: &GenerateConfig{
			Cmd: &GenerateCmdConfig{
				Package: "cmd/" + name + "/rth",
				GenFile: "handlers.go",
				Prune:   &PruneConfig{Enabled: true},
			},
			Framework: &GenerateFrameworkConfig{
				Package: "cmd/" + name + "/rtg",
				GenFile: "rotini.go",
			},
		},
	}
}

// writeMainGo renders the binary entrypoint that runs the generated program.
func writeMainGo(path, moduleName, name string) error {
	content, err := renderGo("main", "templates/main.go.tmpl", map[string]any{
		"Import": moduleName + "/cmd/" + name + "/rth",
		"Pkg":    "rth",
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}
