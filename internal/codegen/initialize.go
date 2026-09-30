package codegen

// `rotini initialize`: scaffolding a new CLI's seed spec and conf under cmd/<name>/, then
// running the standard generate over them. The seed is small — a root with --help/--version
// flags plus `help` and `version` sub-commands, and a conf with only the help feature on — and
// generate takes it from there, writing the entrypoint main.go (which carries the
// //go:generate directive, so every later regen is `go generate ./...`), the handler stubs
// seeded to print help and version, and the codegen files.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// baselineVersion is the version stamped into a new spec/conf's `version` key when
// the running rotini binary is unreleased (its bound version is "v0.0.0", or empty).
// A real release stamps its own tag instead.
const baselineVersion = "0.0.0"

// seedVersion resolves the bare "X.Y.Z" version stamped into a scaffolded spec/conf's
// `version` key from the binary's bound version string ("vX.Y.Z" / "v0.0.0"): the
// leading "v" is stripped, falling back to the baseline when empty.
func seedVersion(version string) string {
	if seg := strings.TrimPrefix(version, "v"); seg != "" {
		return seg
	}
	return baselineVersion
}

// cliNameRe constrains a new CLI's name: it becomes the scaffold directory, the
// generated Go package name, and the root command, so it must be a safe single
// path segment that starts a valid identifier.
var cliNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// InitializeFn is the signature of [Processor.Initialize]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type InitializeFn = func(name, format string, force bool) error

// initialize renders and writes the default seed spec and conf for a new CLI
// named name under cmd/<name>/, validates them, and runs the standard generate
// over them.
func (p *Processor) initialize(name, format string, force bool) error {
	if name == "" {
		return errors.New("a CLI name is required")
	}
	if !cliNameRe.MatchString(name) {
		return fmt.Errorf("invalid CLI name %q: must start with a letter and contain only letters, digits, '-' or '_'", name)
	}

	moduleRoot, _, err := findModule()
	if err != nil {
		return err
	}

	// The seed serialization: an explicit --format, else rotini's default (yaml).
	if format == "" {
		format = "yaml"
	}
	f, err := normalizeFormat(format)
	if err != nil {
		return err
	}

	cmdDir := filepath.Join(moduleRoot, "cmd", name)
	specPath := filepath.Join(cmdDir, ".rotini.spec."+string(f))
	confPath := filepath.Join(cmdDir, ".rotini.conf."+string(f))

	if !force {
		for _, pth := range []string{specPath, confPath} {
			if _, statErr := os.Stat(pth); statErr == nil {
				display := pth
				if rel, relErr := filepath.Rel(moduleRoot, pth); relErr == nil {
					display = rel
				}
				return fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(display))
			}
		}
	}

	version := seedVersion(p.version)
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
	if err := writeGeneratedFile(confPath, confBytes); err != nil {
		return err
	}

	// Reconcile the just-written seeds, then run the exact same generate `rotini generate`
	// runs, with no init special-casing.
	rs, rc, err := p.reconcile(specPath, confPath)
	if err != nil {
		return err
	}
	// A fresh scaffold has nothing to prune, so its notices are always empty.
	_, err = p.validateAndEmit(rs, rc)
	return err
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
