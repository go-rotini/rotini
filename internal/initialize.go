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

// InitializeFn is the signature of [Initialize]. A command handler can bind it under a
// registry key and fetch it as an injectable service, so tests substitute a double (see
// [GenerateFn]).
type InitializeFn = func(name, format string, force bool, into string) error

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
//
// When into names an existing CLI, the new CLI is additionally registered as a
// statically composed ($ref) sub-command of that parent (the parent's spec
// gains a $ref and is re-generated). The new CLI still gets its own main.go and
// stays independently buildable.
func Initialize(name, format string, force bool, into string) error {
	if name == "" {
		return errors.New("a CLI name is required")
	}

	moduleRoot, moduleName, err := findModule()
	if err != nil {
		return err
	}

	// Defaults come from the module-root conf's `initialize` block (when present);
	// an explicit --format overrides the format. A project without that conf gets
	// rotini's built-ins (yaml, "cmd").
	defaults := moduleInitDefaults(moduleRoot)
	if format == "" {
		format = defaults.format
	}
	ext, err := normalizeFormat(format)
	if err != nil {
		return err
	}
	pkgDir := defaults.pkg

	cliDir := filepath.Join(moduleRoot, filepath.FromSlash(pkgDir), name)
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
	if err := WriteConf(confPath, scaffoldConf(name, pkgDir)); err != nil {
		return err
	}
	if err := writeMainGo(mainPath, moduleName, name, pkgDir); err != nil {
		return err
	}
	if err := Generate(specPath, confPath, false, nil); err != nil {
		return err
	}
	if into != "" {
		return composeInto(moduleRoot, pkgDir, into, name, ext)
	}
	return nil
}

// initDefaults holds the resolved `rotini init` defaults.
type initDefaults struct {
	format string
	pkg    string
}

// moduleInitDefaults reads the `initialize` block from the module-root conf (when
// present), falling back to rotini's built-ins (yaml format, "cmd" package dir).
func moduleInitDefaults(moduleRoot string) initDefaults {
	d := initDefaults{format: "yaml", pkg: "cmd"}
	confPath, err := discoverFile(moduleRoot, ".rotini.conf.")
	if err != nil {
		return d
	}
	conf, err := ReadConf(confPath)
	if err != nil || conf.Initialize == nil {
		return d
	}
	if conf.Initialize.Format != "" {
		d.format = conf.Initialize.Format
	}
	if conf.Initialize.Package != "" {
		d.pkg = conf.Initialize.Package
	}
	return d
}

// composeInto registers child as a $ref sub-command of the parent CLI and
// re-generates the parent so the composition takes effect.
func composeInto(moduleRoot, pkgDir, parent, child, childExt string) error {
	parentDir := filepath.Join(moduleRoot, filepath.FromSlash(pkgDir), parent)
	parentSpec, err := discoverFile(parentDir, ".rotini.spec.")
	if err != nil {
		return fmt.Errorf("compose into %q: %w", parent, err)
	}
	parentConf, _ := discoverFile(parentDir, ".rotini.conf.")

	spec, err := ReadSpec(parentSpec)
	if err != nil {
		return err
	}
	ref := "../" + child + "/.rotini.spec." + childExt
	for _, c := range spec.Command.Commands {
		if c.Ref == ref {
			return Generate(parentSpec, parentConf, false, nil) // already referenced
		}
	}
	spec.Command.Commands = append(spec.Command.Commands, Command{Ref: ref})
	if err := WriteSpec(parentSpec, spec); err != nil {
		return err
	}
	return Generate(parentSpec, parentConf, false, nil)
}

// discoverFile returns the first dir/<prefix><ext> file that exists, trying the
// supported serializations in order.
func discoverFile(dir, prefix string) (string, error) {
	for _, ext := range []string{"yaml", "yml", "jsonc", "json"} {
		p := filepath.Join(dir, prefix+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no %s* file found in %s", prefix, dir)
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

// scaffoldSpec builds a minimal valid spec: the schema URL, root name, a version
// metadata var, and example help content so spec-driven help works out of the box.
// The user adds commands from there.
func scaffoldSpec(name string) *Spec {
	return &Spec{
		Schema: specSchemaURL(),
		Command: Command{
			Name:        name,
			Summary:     name + " command-line program",
			Description: "Describe " + name + " here. This text appears at the top of `" + name + " --help`.",
		},
		Metadata: []MetadataEntry{{Var: "Version", Default: "dev"}},
	}
}

// scaffoldConf builds the conf for the standard per-CLI layout under
// <pkgDir>/<name>. Help generation is enabled so a freshly scaffolded CLI has
// working help.
func scaffoldConf(name, pkgDir string) *Conf {
	return &Conf{
		Schema: confSchemaURL(),
		Generate: &GenerateConfig{
			Rth: &GenerateRthConfig{
				Package: pkgDir + "/" + name + "/rth",
				File:    "handlers.go",
			},
			Rtg: &GenerateRtgConfig{
				Package: pkgDir + "/" + name + "/rtg",
				File:    "rotini.go",
				Features: &FeaturesConfig{
					Help: &Feature{Enabled: true},
				},
			},
		},
	}
}

// writeMainGo renders the binary entrypoint that runs the generated program.
func writeMainGo(path, moduleName, name, pkgDir string) error {
	content, err := renderGo("main", "templates/main.go.tmpl", map[string]any{
		"Import": moduleName + "/" + pkgDir + "/" + name + "/rth",
		"Pkg":    "rth",
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}
