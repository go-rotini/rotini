package codegen

// `rotini initialize` writes a new CLI's seed spec and conf under cmd/<name>/ and runs the
// standard generate over them. The seed declares a root with --help/--version flags and
// `help`/`version` sub-commands, with only the help feature enabled. Generate writes main.go
// (carrying the //go:generate directive for later regeneration), the handler stubs and the
// generated files.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// baselineVersion is stamped into a seed's `version` when the binary has no version.
const baselineVersion = "0.0.0"

// seedVersion returns the binary version without a leading "v", or baselineVersion when
// empty.
func seedVersion(version string) string {
	if seg := strings.TrimPrefix(version, "v"); seg != "" {
		return seg
	}
	return baselineVersion
}

// cliNameRe constrains a new CLI's name, which becomes the scaffold directory, package name
// and root command, to a single path segment starting with a letter.
var cliNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// InitializeFn is the signature of [Processor.Initialize]. The companion CLI injects it as a
// dependency so tests can substitute a double (see [GenerateFn]).
type InitializeFn = func(name, format string, force bool) (Initialized, error)

// InitializeDryRunFn is the signature of [Processor.InitializeDryRun].
type InitializeDryRunFn = InitializeFn

// Initialized reports what an init wrote: the spec and conf paths (relative to the working
// directory where possible) and a "[15:04:05] 12.3ms" timing line.
type Initialized struct {
	Spec, Conf, Result string

	// Changes is set by a dry run: each file init would create or replace, one per line.
	Changes []string
}

// initialize plans the seed spec and conf for a new CLI under cmd/<name>/, then validates and
// generates without pruning, all through pl. The generate step reads the seed from memory, so
// a dry run, which writes nothing, plans exactly what a real one writes.
func (p *Processor) initialize(name, format string, force bool, pl *planner) (specPath, confPath string, err error) {
	if name == "" {
		return "", "", errors.New("a CLI name is required")
	}
	if !cliNameRe.MatchString(name) {
		return "", "", fmt.Errorf("invalid CLI name %q: must start with a letter and contain only letters, digits, '-' or '_'", name)
	}

	moduleRoot, _, err := findModule()
	if err != nil {
		return "", "", err
	}

	if format == "" {
		format = "yaml"
	}
	f, err := normalizeFormat(format)
	if err != nil {
		return "", "", err
	}

	cmdDir := filepath.Join(moduleRoot, "cmd", name)
	specPath = filepath.Join(cmdDir, ".rotini.spec."+string(f))
	confPath = filepath.Join(cmdDir, ".rotini.conf."+string(f))

	if !force {
		for _, pth := range []string{specPath, confPath} {
			if _, statErr := os.Stat(pth); statErr == nil {
				display := pth
				if rel, relErr := filepath.Rel(moduleRoot, pth); relErr == nil {
					display = rel
				}
				return "", "", fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(display))
			}
		}
	}

	version := seedVersion(p.version)
	specBytes, err := renderSpecFile(version, name, f)
	if err != nil {
		return "", "", err
	}
	if err := pl.write(specPath, specBytes); err != nil {
		return "", "", err
	}
	confBytes, err := renderConfFile(version, name, f)
	if err != nil {
		return "", "", err
	}
	if err := pl.write(confPath, confBytes); err != nil {
		return "", "", err
	}

	rs, rc, err := p.reconcileSeed(specPath, specBytes, confPath, confBytes, f)
	if err != nil {
		return "", "", err
	}
	// Never prune: with --force the seed may replace a spec with more commands, whose
	// (possibly edited) handlers must survive.
	if _, err := p.validateAndEmit(rs, rc, false, pl); err != nil {
		return "", "", err
	}
	return specPath, confPath, nil
}

// reconcileSeed is reconcile for the seed documents init has in memory.
func (p *Processor) reconcileSeed(specPath string, specBytes []byte, confPath string, confBytes []byte, f fileFormat) (*reconciledSpec, *reconciledConf, error) {
	spec, specJSON, specLocate, err := reconcileData[Spec](specPath, f, specBytes)
	if err != nil {
		return nil, nil, p.explainDecodeFailure("spec", err)
	}
	conf, confJSON, confLocate, err := reconcileData[Conf](confPath, f, confBytes)
	if err != nil {
		return nil, nil, p.explainDecodeFailure("conf", err)
	}
	return &reconciledSpec{path: specPath, spec: spec, json: specJSON, locate: specLocate},
		&reconciledConf{path: confPath, conf: conf, json: confJSON, locate: confLocate}, nil
}

// normalizeFormat maps a format name to its fileFormat, defaulting to yaml.
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
