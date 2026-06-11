package internal

// This file owns the `rotini initialize` operation: scaffolding a new CLI's
// seed spec and conf under <package>/<name>/ of the current module. That is the
// whole job — the seeds are rendered from the embedded templates (renderer.go)
// and written to disk (writer.go); code generation is a separate, explicit
// `rotini generate` run against the seeded files.

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

// InitializeFn is the signature of [Processor.Initialize]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type InitializeFn = func(name, format string, force bool) error

// Initialize scaffolds a new standalone rotini CLI named name: the seed
// .rotini.spec.<fmt> and .rotini.conf.<fmt> under <package>/<name>/ of the
// current module (the package dir defaults to "cmd"; a module-root conf's
// `initialize` block overrides it). format selects the serialization (yaml,
// jsonc, json, or toml). The seeds are create-once: they are left untouched
// unless force is set. Code generation is a separate `rotini generate` run.
func Initialize(name, format string, force bool, version string) error {
	return NewProcessor(version).Initialize(name, format, force)
}

// initialize renders and writes the default seed spec and conf for a new CLI
// named name under <package>/<name>/.
func (p *Processor) initialize(name, format string, force bool) error {
	if name == "" {
		return errors.New("a CLI name is required")
	}

	moduleRoot, _, err := findModule()
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
	f, err := normalizeFormat(format)
	if err != nil {
		return err
	}

	cliDir := filepath.Join(moduleRoot, filepath.FromSlash(defaults.pkg), name)
	specPath := filepath.Join(cliDir, ".rotini.spec."+string(f))
	confPath := filepath.Join(cliDir, ".rotini.conf."+string(f))

	if !force {
		for _, pth := range []string{specPath, confPath} {
			if _, statErr := os.Stat(pth); statErr == nil {
				rel, _ := filepath.Rel(moduleRoot, pth)
				return fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(rel))
			}
		}
	}

	version := schemaURLVersion(p.version)
	specBytes, err := renderSpecFile(version, name, f)
	if err != nil {
		return err
	}
	if err := writeGeneratedFile(specPath, specBytes); err != nil {
		return err
	}
	confBytes, err := renderConfFile(version, name, f)
	if err != nil {
		return err
	}
	return writeGeneratedFile(confPath, confBytes)
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

// normalizeFormat resolves the requested format name to its fileFormat,
// defaulting to yaml.
func normalizeFormat(format string) (fileFormat, error) {
	switch format {
	case "", "yaml", "yml":
		return formatYAML, nil
	case "jsonc":
		return formatJSONC, nil
	case "json":
		return formatJSON, nil
	case "toml":
		return formatTOML, nil
	default:
		return formatUnknown, fmt.Errorf("unsupported format %q (want yaml, jsonc, json, or toml)", format)
	}
}
