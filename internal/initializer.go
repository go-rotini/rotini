package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// baselineSchemaVersion is the version segment scaffolded into new spec/conf
// `$schema` URLs when the running rotini binary is unreleased (its bound version
// is "v0.0.0", or empty). A real release stamps its own tag instead.
const baselineSchemaVersion = "0.0.0"

// schemaURLVersion resolves the version segment for a scaffolded `$schema` URL
// from the binary's bound version string ("vX.Y.Z" / "v0.0.0"): the leading "v"
// is stripped to the "X.Y.Z" segment, falling back to the baseline when empty.
// The URL is always the refs/tags/<VER> form.
func schemaURLVersion(version string) string {
	if seg := strings.TrimPrefix(version, "v"); seg != "" {
		return seg
	}
	return baselineSchemaVersion
}

// schemaURL builds the refs/tags/<VER> URL for the named embedded schema file.
func schemaURL(version, file string) string {
	return "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/" + schemaURLVersion(version) + "/" + file
}

func specSchemaURL(version string) string { return schemaURL(version, "schema-spec.json") }

func confSchemaURL(version string) string { return schemaURL(version, "schema-conf.json") }

// InitializeFn is the signature of [Processor.Initialize]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type InitializeFn = func(name, format string, force bool, into string) error

// Initialize scaffolds a new standalone rotini CLI named name under cmd/<name>/
// of the current module, then generates its code (the default single-package
// layout — framework + rollup merged into one cli/rotini.gen.go):
//
//	cmd/<name>/.rotini.spec.<fmt>   cmd/<name>/cli/rotini.gen.go
//	cmd/<name>/.rotini.conf.<fmt>   cmd/<name>/cli/<name>.go + per-command stubs
//	cmd/<name>/main.go
//
// format selects the spec/conf serialization (yaml, jsonc, or json). The
// create-once files (spec, conf, main.go) are left untouched unless force is
// set; the generated framework/rollup file is always (re)written, and handler
// stubs are never overwritten.
//
// When into names an existing CLI, the new CLI is additionally registered as a
// statically composed ($ref) sub-command of that parent (the parent's spec
// gains a $ref and is re-generated). The new CLI still gets its own main.go and
// stays independently buildable.
func Initialize(name, format string, force bool, into, version string) error {
	return NewProcessor(version).Initialize(name, format, force, into)
}

// recipe selects the project layout the initializer scaffolds.
type recipe string

const (
	// recipeCmd scaffolds the CLI under cmd/<name>/ with its generated package
	// beside it: cmd/<name>/{.rotini.spec,.rotini.conf,main.go} plus
	// cmd/<name>/cli/{rotini.gen.go, per-command stubs}. The established layout and
	// the default.
	recipeCmd recipe = "cmd"
	// recipeFlat scaffolds everything into the current module's main (root) package
	// — main.go, the handler files, and the codegen together (an existing module; it
	// does not bootstrap go.mod/go.sum). Not yet implemented.
	recipeFlat recipe = "flat"
)

// initialize dispatches on the recipe. For now the cmd recipe (and the empty default)
// scaffold the established cmd/<name>/ layout; the flat recipe is a seam to be built
// once its layout is settled.
func (p *Processor) initialize(name, format string, force bool, into string, rcp recipe) error {
	switch rcp {
	case recipeCmd, "":
		return p.initializeCmd(name, format, force, into)
	case recipeFlat:
		return fmt.Errorf("the %q init recipe is not yet supported", recipeFlat)
	default:
		return fmt.Errorf("unknown init recipe %q (want %q or %q)", rcp, recipeCmd, recipeFlat)
	}
}

// initializeCmd scaffolds the cmd/<name>/ layout (the cmd recipe) and generates it —
// the body the Processor's initialize routes recipeCmd (and the empty default) to.
func (p *Processor) initializeCmd(name, format string, force bool, into string) error {
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
		for _, pth := range []string{specPath, confPath, mainPath} {
			if _, statErr := os.Stat(pth); statErr == nil {
				rel, _ := filepath.Rel(moduleRoot, pth)
				return fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(rel))
			}
		}
	}

	if err := writeSpec(specPath, scaffoldSpec(name, p.version)); err != nil {
		return err
	}
	if err := writeConf(confPath, scaffoldConf(name, pkgDir, p.version)); err != nil {
		return err
	}
	if err := writeMainGo(mainPath, moduleName, name, pkgDir); err != nil {
		return err
	}
	if err := Generate(specPath, confPath, false, p.version, nil); err != nil {
		return err
	}
	if into != "" {
		return composeInto(moduleRoot, pkgDir, into, name, ext, p.version)
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
	confPath, err := discoverFile(moduleRoot, fileTypeConf)
	if err != nil {
		return d
	}
	conf, err := readConf(confPath)
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
func composeInto(moduleRoot, pkgDir, parent, child, childExt, version string) error {
	parentDir := filepath.Join(moduleRoot, filepath.FromSlash(pkgDir), parent)
	parentSpec, err := discoverFile(parentDir, fileTypeSpec)
	if err != nil {
		return fmt.Errorf("compose into %q: %w", parent, err)
	}
	parentConf, _ := discoverFile(parentDir, fileTypeConf)

	spec, err := readSpec(parentSpec)
	if err != nil {
		return err
	}
	ref := "../" + child + "/.rotini.spec." + childExt
	for _, c := range spec.Command.Commands {
		if c.Ref == ref {
			return Generate(parentSpec, parentConf, false, version, nil) // already referenced
		}
	}
	spec.Command.Commands = append(spec.Command.Commands, Command{Ref: ref})
	if err := writeSpec(parentSpec, spec); err != nil {
		return err
	}
	return Generate(parentSpec, parentConf, false, version, nil)
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

// scaffoldSpec builds a minimal valid spec: the schema URL, root name, and example
// help content so spec-driven help works out of the box. The user adds commands
// (and binds their own build vars like version) from there.
func scaffoldSpec(name, version string) *Spec {
	return &Spec{
		Schema: specSchemaURL(version),
		Command: Command{
			Name:        name,
			Summary:     name + " command-line program",
			Description: "Describe " + name + " here. This text appears at the top of `" + name + " --help`.",
		},
	}
}

// scaffoldConf builds the conf for the default single-contained-package layout
// under <pkgDir>/<name>/cli: cli and cligen point at the same package and file,
// so the framework and the handler rollup are generated into one rotini.gen.go.
// Help generation is enabled so a freshly scaffolded CLI has working help.
func scaffoldConf(name, pkgDir, version string) *Conf {
	cliPkg := pkgDir + "/" + name + "/cli"
	return &Conf{
		Schema: confSchemaURL(version),
		Generate: &GenerateConfig{
			Packages: &PackagesConfig{
				Cli:    &PackageConfig{Package: cliPkg, File: "rotini.gen.go"},
				Cligen: &PackageConfig{Package: cliPkg, File: "rotini.gen.go"},
			},
			Features: &FeaturesConfig{
				Help: &Feature{Enabled: true, Dir: cliPkg + "/embed/help"},
			},
		},
	}
}

// writeMainGo renders the binary entrypoint that runs the generated program.
func writeMainGo(path, moduleName, name, pkgDir string) error {
	content, err := renderGo("main", "templates/main.go.tmpl", map[string]any{
		"Import": moduleName + "/" + pkgDir + "/" + name + "/cli",
		"Pkg":    "cli",
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}
