package internal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// The embedded YAML seed templates rotini init renders to scaffold a new CLI's spec
// and conf. YAML is the authoring format; renderSeed converts the rendered output to
// the requested serialization (json/jsonc/toml) when it is not YAML.
var (
	//go:embed templates/.rotini.spec.yaml.tmpl
	specSeedTemplate []byte
	//go:embed templates/.rotini.conf.yaml.tmpl
	confSeedTemplate []byte
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

// Initialize scaffolds a new standalone rotini CLI named name under cmd/<name>/
// of the current module, then generates its code into the module-internal
// package internal/cmd/<name> (framework + rollup merged into one rotini.gen.go):
//
//	cmd/<name>/.rotini.spec.<fmt>   internal/cmd/<name>/rotini.gen.go
//	cmd/<name>/.rotini.conf.<fmt>   internal/cmd/<name>/<name>.go + per-command stubs
//	cmd/<name>/main.go
//
// format selects the spec/conf serialization (yaml, jsonc, json, or toml). The
// create-once files (spec, conf, main.go) are left untouched unless force is
// set; the generated framework/rollup file is always (re)written, and handler
// stubs are never overwritten.
func Initialize(name, format string, force bool, version string) error {
	return NewProcessor(version).Initialize(name, format, force)
}

// recipe selects the project layout the initializer scaffolds.
type recipe string

const (
	// recipeCmd scaffolds the CLI source under cmd/<name>/
	// ({.rotini.spec,.rotini.conf,main.go}) and its generated package into the
	// module-internal internal/cmd/<name>/{rotini.gen.go, per-command stubs}. The
	// established layout and the default.
	recipeCmd recipe = "cmd"
	// recipeFlat scaffolds everything into the current module's main (root) package
	// — main.go, the handler files, and the codegen together (an existing module; it
	// does not bootstrap go.mod/go.sum). Not yet implemented.
	recipeFlat recipe = "flat"
)

// initialize dispatches on the recipe. For now the cmd recipe (and the empty default)
// scaffold the established cmd/<name>/ layout; the flat recipe is a seam to be built
// once its layout is settled.
func (p *Processor) initialize(name, format string, force bool, rcp recipe) error {
	switch rcp {
	case recipeCmd, "":
		return p.initializeCmd(name, format, force)
	case recipeFlat:
		return fmt.Errorf("the %q init recipe is not yet supported", recipeFlat)
	default:
		return fmt.Errorf("unknown init recipe %q (want %q or %q)", rcp, recipeCmd, recipeFlat)
	}
}

// initializeCmd scaffolds the cmd/<name>/ layout (the cmd recipe) and generates it —
// the body the Processor's initialize routes recipeCmd (and the empty default) to.
func (p *Processor) initializeCmd(name, format string, force bool) error {
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

	seed := seedContext{RotiniVersion: schemaURLVersion(p.version), RootCommandName: name}
	specBytes, err := renderSeed("spec", specSeedTemplate, seed, ext)
	if err != nil {
		return err
	}
	if err := writeGeneratedFile(specPath, specBytes); err != nil {
		return err
	}
	confBytes, err := renderSeed("conf", confSeedTemplate, seed, ext)
	if err != nil {
		return err
	}
	if err := writeGeneratedFile(confPath, confBytes); err != nil {
		return err
	}
	if err := writeMainGo(mainPath, moduleName, name); err != nil {
		return err
	}
	return Generate(specPath, confPath, false, p.version, nil)
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
	case "toml":
		return "toml", nil
	default:
		return "", fmt.Errorf("unsupported format %q (want yaml, jsonc, json, or toml)", format)
	}
}

// seedContext is the data the embedded spec/conf seed templates render against.
// Field names are exported because text/template reads only exported fields.
type seedContext struct {
	RotiniVersion   string // $schema URL version segment, e.g. "0.0.0" or "1.4.0"
	RootCommandName string // the new CLI's root command name
}

// renderSeed renders an embedded YAML seed template against seed, then converts the
// rendered output to the requested serialization (see formatSeed). name labels the
// template for diagnostics ("spec" / "conf"); ext is the normalized format extension.
func renderSeed(name string, tmplBytes []byte, seed seedContext, ext string) ([]byte, error) {
	tmpl, err := template.New(name).Parse(string(tmplBytes))
	if err != nil {
		return nil, fmt.Errorf("parse %s seed template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, seed); err != nil {
		return nil, fmt.Errorf("render %s seed: %w", name, err)
	}
	return formatSeed(buf.Bytes(), ext)
}

// formatSeed converts a rendered YAML seed to the target serialization (the
// normalized extension from normalizeFormat). YAML — the authoring format — is
// returned verbatim; json and jsonc become pretty-printed JSON (a valid JSONC
// document); toml is transcoded through JSON. Conversion goes through an untyped
// value, so it carries every field the template declares (including documentation
// such as the disabled feature toggles).
func formatSeed(yamlBytes []byte, ext string) ([]byte, error) {
	if ext == "yaml" {
		return yamlBytes, nil
	}
	jsonBytes, err := yaml.ToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("convert seed to json: %w", err)
	}
	switch ext {
	case "json", "jsonc":
		var v any
		if err := json.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode seed json: %w", err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode seed json: %w", err)
		}
		return append(out, '\n'), nil
	case "toml":
		out, err := toml.FromJSON(jsonBytes)
		if err != nil {
			return nil, fmt.Errorf("convert seed to toml: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, ext)
	}
}

// writeMainGo renders the binary entrypoint that runs the generated program. The
// generated package lives at internal/cmd/<name> (package <name>); main.go imports
// it aliased as "cli" so the reference never collides with the rotini runtime
// package (also named "rotini").
func writeMainGo(path, moduleName, name string) error {
	content, err := renderGo("main", "templates/main.go.tmpl", map[string]any{
		"Import": moduleName + "/internal/cmd/" + name,
		"Pkg":    "cli",
	})
	if err != nil {
		return err
	}
	return writeGeneratedFile(path, content)
}
