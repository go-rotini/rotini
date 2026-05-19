package internal

import (
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-rotini/fs"
)

// templatesFS holds every codegen template the package emits. Each
// template renders one .gen.go file in the user's project (plus
// .rotini.spec.yaml / .rotini.conf.yaml / main.go for `rotini init`).
//
// Per-template breakdown — split per emitted concern so a future change
// to one output file doesn't ripple through unrelated ones:
//
//   - lifecycle.gen.tmpl  — Execution, RunExecution, SafeCall
//   - render.gen.tmpl     — RenderError, ExitCode
//   - spec.gen.tmpl       — `var Spec = rtk.ProgramSpec{...}` literal
//   - inputs.gen.tmpl     — per-command *Flags / *Arguments / *Inputs
//     types + RotiniCommandPath + PopulateFromArgv
//   - handlers.gen.tmpl   — per-command Handler interfaces + Ctx aliases
//   - aggregate Handlers interface
//   - executors.gen.tmpl  — per-command build<Cmd>Execution + the
//     executor dispatch map
//   - program.gen.tmpl    — Program type + NewProgram + Execute body
//     auto-binding rtk services
//   - config.gen.tmpl     — emitted only when `configs:` is declared
//   - bridge.gen.tmpl     — user's internal/handlers/handlers.gen.go
//     (Program var + accessor methods)
//   - handler-skel.go.tmpl — per-command skeleton, written only when
//     the file does not already exist
//
// Plus `rotini init` scaffolds:
//
//   - spec.yaml.tmpl   — initial .rotini.spec.yaml
//   - conf.yaml.tmpl   — initial .rotini.conf.yaml
//   - main.go.tmpl     — initial main.go
//
//go:embed templates/*.tmpl
var templatesFS embed.FS

// frameworkTemplates is the ordered list of templates [Run] emits into
// the user's framework package directory. Order is stable so the
// returned [RunResult.FilesWritten] is deterministic.
//
// As more wiring templates land (M2.5b), the list grows. Bridge and
// handler-skel templates emit to *different* directories under
// different write rules and are not part of this set.
var frameworkTemplates = []string{
	"spec.gen.tmpl",
	"render.gen.tmpl",
	"inputs.gen.tmpl",
	"handlers.gen.tmpl",
}

// RunOptions configures a [Run] invocation. SpecPath is required; the
// rest have sensible defaults so the simplest "run codegen against this
// spec" invocation is one line:
//
//	res, err := internal.Run(internal.RunOptions{SpecPath: "rotini.spec.yaml"})
type RunOptions struct {
	// SpecPath is the path to the .rotini.spec.{yaml,json,toml,jsonc}
	// file. Required.
	SpecPath string

	// ConfPath is the path to the .rotini.conf.{yaml,json,toml,jsonc}
	// file. When empty, [Run] looks for
	// "<dir(SpecPath)>/.rotini.conf.yaml" and silently skips conf
	// loading when no file is present (the rotini defaults apply).
	ConfPath string

	// OutputDir is the framework-package directory the .gen.go files
	// are written to. When empty, the directory is derived from
	// conf.Generate.Framework.Package (default "internal/cli/cmd").
	// The path is relative to the current working directory unless
	// absolute.
	OutputDir string

	// Package is the Go package name the rendered files declare. When
	// empty, the basename of OutputDir is used.
	Package string
}

// RunResult reports the outcome of a [Run] invocation.
type RunResult struct {
	// FilesWritten lists every absolute path [Run] created or
	// overwrote, in deterministic order.
	FilesWritten []string

	// FilesSkipped lists paths [Run] left untouched because they
	// already existed under "write only if missing" rules (e.g.,
	// handler skeletons). Empty for M2.6's foundation-template set.
	FilesSkipped []string
}

// Run is the full emission flow: load spec + conf, validate both,
// translate the spec to [rtk.ProgramSpec], render every framework
// template, and atomically write the results to disk.
//
// On any pipeline failure, [Run] returns an error describing the
// failing step. Partial writes do not occur: each output file is
// written via fs.WriteFile with WithAtomic, so an interrupted run
// either leaves the prior file intact or the new one fully in place,
// never a half-written byte stream.
func Run(opts RunOptions) (*RunResult, error) {
	if opts.SpecPath == "" {
		return nil, ErrMissingSpecPath
	}

	spec, err := LoadSpec(opts.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("internal: load spec: %w", err)
	}
	if err := Validate(spec); err != nil {
		return nil, fmt.Errorf("internal: validate spec: %w", err)
	}

	conf, err := loadConfOrDefault(opts)
	if err != nil {
		return nil, fmt.Errorf("internal: load conf: %w", err)
	}
	if conf == nil {
		// No conf file present: rotini defaults apply. Skip schema
		// validation since there is no document to validate.
		conf = &Conf{}
	} else if err := ValidateConf(conf); err != nil {
		return nil, fmt.Errorf("internal: validate conf: %w", err)
	}
	ApplyConfDefaults(conf)

	outputDir, pkg := resolveOutputTarget(opts, conf)

	in := NewRenderInput(pkg, ToProgramSpec(spec))

	result := &RunResult{}
	for _, name := range frameworkTemplates {
		out, err := Render(name, in)
		if err != nil {
			return nil, fmt.Errorf("internal: render %s: %w", name, err)
		}
		path := filepath.Join(outputDir, templateOutputFilename(name))
		if err := fs.WriteFile(path, out,
			fs.WithAtomic(true),
			fs.WithMkdirAll(true),
		); err != nil {
			return nil, fmt.Errorf("internal: write %s: %w", path, err)
		}
		result.FilesWritten = append(result.FilesWritten, path)
	}

	sort.Strings(result.FilesWritten)
	return result, nil
}

// ErrMissingSpecPath is returned by [Run] when [RunOptions.SpecPath] is
// empty.
var ErrMissingSpecPath = errors.New("internal: SpecPath is required")

// loadConfOrDefault reads the conf file referenced by opts (or its
// default location). Returns (nil, nil) when the user did not supply
// a ConfPath and no default file is present — the caller should treat
// nil as "use rotini defaults."
//
// Returns an error only when an explicit ConfPath is unloadable or the
// default location's file is malformed.
func loadConfOrDefault(opts RunOptions) (*Conf, error) {
	path := opts.ConfPath
	if path == "" {
		path = filepath.Join(filepath.Dir(opts.SpecPath), ".rotini.conf.yaml")
		if !fs.Exists(path) {
			return nil, nil //nolint:nilnil // signals "no conf file" to Run
		}
	}
	return LoadConf(path)
}

// resolveOutputTarget figures out where Run should write its output and
// what package name to use. Explicit options win over conf defaults.
func resolveOutputTarget(opts RunOptions, conf *Conf) (dir, pkg string) {
	dir = opts.OutputDir
	if dir == "" {
		dir = conf.Generate.Framework.Package
	}
	pkg = opts.Package
	if pkg == "" {
		pkg = filepath.Base(dir)
		if strings.ContainsRune(pkg, filepath.Separator) || pkg == "." || pkg == "" {
			pkg = "rotini"
		}
	}
	return dir, pkg
}

// templateOutputFilename derives the .gen.go filename from a
// template name: "spec.gen.tmpl" → "spec.gen.go". Unknown
// extensions pass through unchanged.
func templateOutputFilename(tmplName string) string {
	if base, ok := strings.CutSuffix(tmplName, ".tmpl"); ok {
		return base + ".go"
	}
	return tmplName + ".go"
}
