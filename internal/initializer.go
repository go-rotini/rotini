package internal

// This file owns the `rotini initialize` operation: scaffolding a new CLI's
// seed spec and conf under cmd/<name>/ of the current module, validating
// them, and running the standard generate over them. The seed is MINIMAL: a
// root-only spec (no sub-commands or flags) and a conf declaring the entrypoint
// + packages with every feature off. Init then runs the SAME generate as
// `rotini generate` (no special init-style path): it writes the entrypoint
// main.go — which carries the //go:generate directive, so every later regen is
// just `go generate ./...` — the empty root handler stub, and the codegen files.
// The author grows the spec/conf from there.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// cliNameRe constrains a new CLI's name: it becomes the scaffold directory, the
// generated Go package name, and the root command, so it must be a safe single
// path segment that starts a valid identifier.
var cliNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// InitializeFn is the signature of [Processor.Initialize]. A command handler binds it
// under a registry key and fetches it as an injectable service, so tests substitute a
// double (see [GenerateFn]).
type InitializeFn = func(name, format string, force bool) error

// Initialize scaffolds a new standalone rotini CLI named name: it writes the seed
// .rotini.spec.<fmt> and .rotini.conf.<fmt> under cmd/<name>/ of the current
// module, validates them, and runs the standard generate to produce the
// entrypoint, empty handler stubs, and codegen files — a ready-to-build CLI.
// format selects the serialization (yaml, jsonc, json, or toml; a module-root
// conf's `initialize` block sets the default). The seeds (and the create-once
// main.go/stubs) are left untouched unless force is set.
func Initialize(name, format string, force bool, version string) error {
	return NewProcessor(version).Initialize(name, format, force)
}

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

	// The default format comes from the module-root conf's `initialize` block (when
	// present); an explicit --format overrides it. A project without that conf gets
	// rotini's built-in default (yaml).
	if format == "" {
		format = moduleInitFormat(moduleRoot)
	}
	f, err := normalizeFormat(format)
	if err != nil {
		return err
	}

	cliDir := filepath.Join(moduleRoot, "cmd", name)
	specPath := filepath.Join(cliDir, ".rotini.spec."+string(f))
	confPath := filepath.Join(cliDir, ".rotini.conf."+string(f))

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
	if err := writeGeneratedFile(confPath, confBytes); err != nil {
		return err
	}

	// Validate the seeds, then run the standard generate over them — the exact
	// same path `rotini generate` runs (no init-special-casing). This kickstarts
	// the new CLI: it writes the entrypoint main.go (which carries the
	// //go:generate directive, so every later regen is just `go generate ./...`),
	// an empty handler stub per command, and the cmd/cmdgen codegen files.
	s := newSession(specPath, confPath, p.version)
	if err := s.load(); err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	return s.generate()
}

// moduleInitFormat reads the default seed format from the `initialize` block of
// the module-root conf (when present), falling back to rotini's built-in (yaml).
func moduleInitFormat(moduleRoot string) string {
	confPath, err := discoverConf(moduleRoot)
	if err != nil {
		return "yaml"
	}
	conf, err := readConf(confPath)
	if err != nil || conf.Initialize == nil || conf.Initialize.Format == "" {
		return "yaml"
	}
	return conf.Initialize.Format
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
