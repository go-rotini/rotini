package codegen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// ImportOptions configures [Processor.Import].
type ImportOptions struct {
	// Framework is the program's framework: [FrameworkCobra] (the default, ""),
	// [FrameworkKong] or [FrameworkURFave].
	Framework string
	// Package is the Go package that builds the program's command tree, such as ./cmd.
	Package string
	// Root is a Go expression, evaluated in Package, that yields a command of the tree (a
	// pointer to the grammar, for Kong); "" finds one.
	Root string
	// Name is the CLI's name; "" keeps the program's root command name, else (Kong, or a
	// urfave/cli root with no name) the last element of Package's import path.
	Name string
	// Vars are the Kong variables the program passes to kong.Vars, for ${name} interpolations.
	Vars map[string]string
	// Dir names cmd/<dir> and internal/cmd/<dir>; "" uses the CLI's name.
	Dir string
	// Strict imports a command with no argument rule as taking only the arguments its usage
	// line names, instead of also any number more.
	Strict bool
	// Format is the spec and conf format: yaml (default), yml, json, jsonc or toml.
	Format string
	// Tags are build tags for go test.
	Tags string
	// Timeout bounds the import's go test run; 0 means two minutes.
	Timeout time.Duration
	// ImporterVersion is the github.com/go-rotini/import release to run, or the path of a
	// local checkout; "" means [ImporterVersion].
	ImporterVersion string
	// Force replaces an existing spec and conf, writes into an internal/cmd/<dir> that isn't
	// rotini-generated, and writes beside an existing cmd/<dir>/main.go (which is kept).
	Force bool
}

// ImportFn is the signature of [Processor.Import] and [Processor.ImportDryRun].
type ImportFn = func(ctx context.Context, opts ImportOptions) (Imported, error)

// Imported reports an import.
type Imported struct {
	// Notes are the import's notes, one stderr line each: "[lossy] acme deploy: …".
	Notes []string
	// Summary is the import's summary line: "imported 13 commands, 41 flags: …".
	Summary string
	// Spec, Conf and Result are as in [Initialized].
	Spec, Conf, Result string
	// Changes is set by a dry run: each file the import would create or replace.
	Changes []string
}

// Import reads the command tree a program builds in opts.Package and writes a spec and
// conf for it under cmd/<dir>, then validates and generates, seeding handler stubs from the
// imported spec. The program's files and go.mod are left as they are. Notes are returned even
// when the import fails after reading the tree.
func (p *Processor) Import(ctx context.Context, opts ImportOptions) (Imported, error) {
	return p.importWith(ctx, opts, newPlanner(false))
}

// ImportDryRun runs the import and plans everything [Processor.Import] would write, writing
// nothing.
func (p *Processor) ImportDryRun(ctx context.Context, opts ImportOptions) (Imported, error) {
	return p.importWith(ctx, opts, newPlanner(true))
}

func (p *Processor) importWith(ctx context.Context, opts ImportOptions, pl *planner) (Imported, error) {
	start := time.Now()
	if opts.Package == "" {
		return Imported{}, errors.New("a package is required, such as ./cmd")
	}
	if err := checkImportNames(opts); err != nil {
		return Imported{}, err
	}
	if _, err := frameworkFor(opts); err != nil {
		return Imported{}, err
	}
	f, err := normalizeFormat(opts.Format)
	if err != nil {
		return Imported{}, err
	}
	moduleRoot, _, modFile, err := findModuleFile()
	if err != nil {
		return Imported{}, err
	}
	var vendorNote string
	if filepath.Base(modFile) == "vendor.mod" {
		vendorNote = "the module is in vendor.mod, so the import read it in place of go.mod"
	}

	res, runNotes, err := importRunner(ctx, moduleRoot, opts)
	if err != nil {
		return Imported{}, err
	}
	if vendorNote != "" {
		runNotes = append(runNotes, importNote{Path: res.Command.Name, Level: "info", Msg: vendorNote})
	}
	name := opts.Name
	if name == "" {
		name = res.Command.Name
	}
	if !cliNameRe().MatchString(name) {
		return Imported{}, fmt.Errorf("the program's name %q can't name a rotini CLI; pass --name", name)
	}
	dir := opts.Dir
	if dir == "" {
		dir = name
	}

	sb := &importSpecBuilder{res: res, name: name, moduleRoot: moduleRoot, comments: f == formatYAML}
	version := seedVersion(p.version)
	specYAML := sb.build(version)
	if sb.findUnparsedExamples(specYAML) {
		// Build again without the examples that don't parse; the first build's notes go.
		sb.dropped, sb.completion = nil, false
		specYAML = sb.build(version)
	}
	dropped := groupDropped(sb.dropped)
	out := Imported{Summary: res.stats(slices.Concat(runNotes, dropped))}
	for _, n := range slices.Concat(runNotes, res.Notes, dropped) {
		out.Notes = append(out.Notes, n.String())
	}

	cmdDir := filepath.Join(moduleRoot, "cmd", dir)
	specPath := filepath.Join(cmdDir, ".rotini.spec."+string(f))
	confPath := filepath.Join(cmdDir, ".rotini.conf."+string(f))
	if err := checkImportTarget(moduleRoot, dir, specPath, confPath, opts); err != nil {
		return out, err
	}

	specBytes, err := importDocument(specYAML, f, FormatKindSpec)
	if err != nil {
		return out, err
	}
	confBytes, err := importDocument(importConf(version, dir, sb.completion), f, FormatKindConf)
	if err != nil {
		return out, err
	}
	if err := importNameClashes(specPath, specBytes, f); err != nil {
		return out, err
	}
	if err := pl.write(specPath, specBytes); err != nil {
		return out, err
	}
	if err := pl.write(confPath, confBytes); err != nil {
		return out, err
	}
	rs, rc, err := p.reconcileSeed(specPath, specBytes, confPath, confBytes, f)
	if err != nil {
		return out, err
	}
	if _, err := p.validateAndEmit(rs, rc, false, pl); err != nil {
		return out, errors.Join(errors.New("the imported spec doesn't validate, which is an importer bug; please report it with the program's tree"), err)
	}
	out.Spec, out.Conf, out.Result = displayPath(specPath), displayPath(confPath), reportTiming(start)
	if pl.dry {
		out.Changes = pl.Changes()
	}
	return out, nil
}

// importNameClashes refuses, before anything is written, a program whose commands, flags or
// other names would generate the same Go name, which no spec can express.
func importNameClashes(specPath string, specBytes []byte, f fileFormat) error {
	spec, derr := decodeData[Spec](f, specBytes, specPath)
	if derr != nil {
		return nil //nolint:nilerr // a spec that doesn't decode is reported by the validation after writing
	}
	problems := lintGoNames(spec)
	if len(problems) == 0 {
		return nil
	}
	locateProblems(problems, displayPath(specPath), newSourceLocator(f, specBytes))
	return errors.Join(append([]error{errors.New("the program has names that differ only in case, \"-\" or \"_\", which make the same Go name, so it can't be imported; rename them in the program and import again")}, problems...)...)
}

// checkImportNames refuses a --name or --dir that can't name a rotini CLI or its directory.
func checkImportNames(opts ImportOptions) error {
	if opts.Name != "" && !cliNameRe().MatchString(opts.Name) {
		return fmt.Errorf("invalid CLI name %q: must start with a letter and contain only letters, digits, '-' or '_'", opts.Name)
	}
	if opts.Dir != "" && !cliNameRe().MatchString(opts.Dir) {
		return fmt.Errorf("invalid --dir %q: must start with a letter and contain only letters, digits, '-' or '_'", opts.Dir)
	}
	return nil
}

// checkImportTarget refuses to write over an existing spec or conf, into an
// internal/cmd/<dir> rotini didn't generate, or beside the program's own main.go, unless
// opts.Force.
func checkImportTarget(moduleRoot, dir, specPath, confPath string, opts ImportOptions) error {
	if opts.Force {
		return nil
	}
	rel := func(p string) string {
		if r, err := filepath.Rel(moduleRoot, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	for _, p := range []string{specPath, confPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", rel(p))
		}
	}
	hint := fmt.Sprintf("pass --dir %s-rotini to write the rotini CLI beside it, or --force", dir)
	internal := filepath.Join(moduleRoot, "internal", "cmd", dir)
	if entries, err := os.ReadDir(internal); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(internal, "zz_rotini.go")); err != nil {
			return fmt.Errorf("%s holds code rotini didn't generate; %s", rel(internal), hint)
		}
	}
	if _, err := os.Stat(filepath.Join(moduleRoot, "cmd", dir, "main.go")); err == nil {
		return fmt.Errorf("%s already exists and would be kept, so the rotini CLI wouldn't be wired; %s", rel(filepath.Join(moduleRoot, "cmd", dir, "main.go")), hint)
	}
	return nil
}

// importDocument puts an import's YAML document in canonical form, then in format f. Only a
// YAML document keeps its comments.
func importDocument(src []byte, f fileFormat, kind string) ([]byte, error) {
	formatted, err := FormatYAML(src, kind, "")
	if err != nil {
		return nil, fmt.Errorf("format the imported %s: %w", kind, err)
	}
	return convert(formatted, f)
}

// importConf is the conf an import writes: init's seed, with the completion feature on when
// the spec has a completion command.
func importConf(version, dir string, completion bool) []byte {
	return fmt.Appendf(nil, `$schema: ./.rotini-schema.conf.json
version: %s
generate:
  schemas:
    conf:
      file: cmd/%[2]s/.rotini-schema.conf.json
    spec:
      file: cmd/%[2]s/.rotini-schema.spec.json
  packages:
    - type: main
      file: cmd/%[2]s/main.go
    - type: cmd
      file: internal/cmd/%[2]s/zz_rotini.go
  features:
    - type: help
      enabled: true
    - type: completion
      enabled: %[3]t
    - type: man
      enabled: false
    - type: markdown
      enabled: false
validate:
  fail: collect
`, version, dir, completion)
}
