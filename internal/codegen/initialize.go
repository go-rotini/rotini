package codegen

// `rotini initialize` writes a new CLI's seed spec and conf under cmd/<name>/ and runs the
// standard generate over them. The seed declares a root with --help/--version flags and
// `help`/`version` sub-commands, with only the help feature enabled. Generate writes main.go
// (carrying the //go:generate directive for later regeneration), the handler stubs and the
// generated files. `--template` starts from another shape instead; see initialize_templates.go.

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
type InitializeFn = func(name string, opt InitOptions) (Initialized, error)

// InitializeDryRunFn is the signature of [Processor.InitializeDryRun].
type InitializeDryRunFn = InitializeFn

// InitOptions are init's choices besides the CLI's name.
type InitOptions struct {
	// Format is the spec and conf format: yaml (the default), json, jsonc or toml.
	Format string
	// Template is the shape to start from: plugin, daemon or suite; "" for the plain seed.
	Template string
	// Force replaces an existing seed spec and conf.
	Force bool
}

// Initialized reports what an init wrote: the spec and conf paths (relative to the working
// directory where possible) and a "[15:04:05] 12.3ms" timing line.
type Initialized struct {
	Spec, Conf, Result string

	// Also lists the other CLIs a template wrote, such as a suite's second binary and its
	// shared child, in the order they were written.
	Also []SeedFiles

	// Changes is set by a dry run: each file init would create or replace, one per line.
	Changes []string
}

// SeedFiles is one CLI's seed spec and conf, as [Initialized] reports them.
type SeedFiles struct {
	Spec, Conf string
}

// initialize plans the seed spec and conf for a new CLI under cmd/<name>/ (or, with a
// template, every CLI the template declares), then validates and generates each without
// pruning, all through pl. The generate step reads the seed from memory, so a dry run, which
// writes nothing, plans exactly what a real one writes. The first CLI returned is the one
// named name.
func (p *Processor) initialize(name string, opt InitOptions, pl *planner) ([]SeedFiles, error) {
	if name == "" {
		return nil, errors.New("a CLI name is required")
	}
	if !cliNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid CLI name %q: must start with a letter and contain only letters, digits, '-' or '_'", name)
	}

	moduleRoot, modulePath, err := findModule()
	if err != nil {
		return nil, err
	}

	format := opt.Format
	if format == "" {
		format = "yaml"
	}
	f, err := normalizeFormat(format)
	if err != nil {
		return nil, err
	}

	data := templateInitData{Version: seedVersion(p.version), Name: name, Ext: string(f), Module: modulePath}
	clis, err := initCLIs(opt.Template, data)
	if err != nil {
		return nil, err
	}

	seeds := make([]SeedFiles, len(clis))
	for i, c := range clis {
		cmdDir := filepath.Join(moduleRoot, "cmd", c.name)
		seeds[i] = SeedFiles{
			Spec: filepath.Join(cmdDir, ".rotini.spec."+string(f)),
			Conf: filepath.Join(cmdDir, ".rotini.conf."+string(f)),
		}
	}
	if !opt.Force {
		if err := existingSeed(seeds, moduleRoot); err != nil {
			return nil, err
		}
	}

	// A template's CLIs may compose each other: in a dry run the later ones read the earlier
	// seeds from the plan rather than the disk.
	if pl.dry {
		defer withSeedOverlay(pl.pending)()
	}
	for i, c := range clis {
		specBytes, err := c.spec(f)
		if err != nil {
			return nil, err
		}
		if err := pl.write(seeds[i].Spec, specBytes); err != nil {
			return nil, err
		}
		confBytes, err := c.conf(f)
		if err != nil {
			return nil, err
		}
		if err := pl.write(seeds[i].Conf, confBytes); err != nil {
			return nil, err
		}
		// Files the template seeds itself are created before generate, which then keeps them.
		for _, file := range c.files {
			if err := pl.createOnce(filepath.Join(moduleRoot, filepath.FromSlash(file.path)), file.content); err != nil {
				return nil, err
			}
		}

		rs, rc, err := p.reconcileSeed(seeds[i].Spec, specBytes, seeds[i].Conf, confBytes, f)
		if err != nil {
			return nil, err
		}
		// Never prune: with --force the seed may replace a spec with more commands, whose
		// (possibly edited) handlers must survive.
		if _, err := p.validateAndEmit(rs, rc, false, pl); err != nil {
			return nil, err
		}
	}

	// Report the CLI named name first, then the others in the order they were written.
	ordered := make([]SeedFiles, 0, len(seeds))
	for i, c := range clis {
		if c.name == name {
			ordered = append(ordered, seeds[i])
		}
	}
	for i, c := range clis {
		if c.name != name {
			ordered = append(ordered, seeds[i])
		}
	}
	return ordered, nil
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

// existingSeed reports the first seed spec or conf that already exists, which init refuses to
// overwrite without --force.
func existingSeed(seeds []SeedFiles, moduleRoot string) error {
	for _, s := range seeds {
		for _, pth := range []string{s.Spec, s.Conf} {
			if _, err := os.Stat(pth); err == nil {
				display := pth
				if rel, relErr := filepath.Rel(moduleRoot, pth); relErr == nil {
					display = rel
				}
				return fmt.Errorf("%s already exists (use --force to overwrite)", filepath.ToSlash(display))
			}
		}
	}
	return nil
}
