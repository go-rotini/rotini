package internal

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/rotini/rtk"
)

// templatesFS holds every codegen template the package emits. One
// framework file is rendered per Run invocation (rotini.gen.tmpl);
// the bridge and skeleton templates write to the user's cmd package
// under different rules.
//
// Templates:
//
//   - rotini.gen.tmpl       — single framework file containing spec
//     literal, inputs types, handler interfaces, lifecycle helpers,
//     executors, Program, completion accessor, RenderError / ExitCode.
//     Rendered to <framework-dir>/<conf.Framework.GenFile>.
//   - bridge.gen.tmpl       — user's internal/handlers/handlers.gen.go
//     (handlers struct + Program var + accessor methods). Always
//     overwritten.
//   - handler-skel.go.tmpl  — per-command stub. Rendered once per
//     command path; written only when the target file does not exist.
//
// Plus `rotini init` scaffolds:
//
//   - init-spec.yaml.tmpl  — initial .rotini.spec.yaml
//   - init-conf.yaml.tmpl  — initial .rotini.conf.yaml
//   - init-main.go.tmpl    — initial main.go
//
//go:embed templates/*.tmpl
var templatesFS embed.FS

// frameworkTemplate is the single template that renders the user's
// framework package. One template → one rotini.gen.go file —
// matching rotiniold's convention and standard Go codegen practice.
// The output filename comes from conf.Generate.Framework.GenFile
// (default "rotini.gen.go").
//
// Bridge and handler-skel templates emit to *different* directories
// under different write rules and are not part of this constant.
const frameworkTemplate = "rotini.gen.tmpl"

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
	// conf.Generate.Framework.Package (default "internal/cli/rotini").
	// The path is relative to the current working directory unless
	// absolute.
	OutputDir string

	// Package is the Go package name the rendered framework files
	// declare. When empty, the basename of OutputDir is used.
	Package string

	// CmdDir is the directory the user-handler bridge (handlers.gen.go)
	// and per-command skeleton files (root.go, add.go, ...) are
	// written to. When empty, derived from conf.Generate.Cmd.Package
	// (default "internal/cli/cmd").
	CmdDir string

	// CmdPackage is the Go package name the bridge + skeleton files
	// declare. When empty, the basename of CmdDir is used.
	CmdPackage string

	// ModulePath is the Go module path used to import the framework
	// package from the bridge / skeleton files (e.g.,
	// "example.com/me/myapp"). When empty, [Run] walks go.mod from
	// SpecPath's directory upward. If neither override nor go.mod is
	// available, [Run] skips bridge + skeleton emission with a
	// non-fatal warning surfaced via [RunResult.FilesSkipped].
	ModulePath string

	// SkipBridge, when true, suppresses bridge + skeleton emission.
	// Useful when the user manages their own handler-bridge wiring
	// or wants to render only the framework package.
	SkipBridge bool

	// ModuleRoot is the directory containing go.mod. Used to compute
	// the framework's import path when OutputDir is absolute (e.g.,
	// in tests):
	//
	//	frameworkImportPath = ModulePath + "/" + Rel(ModuleRoot, OutputDir)
	//
	// When empty, the current working directory is assumed. Production
	// callers (the rotini binary) run from the module root with
	// relative paths, so the field is rarely set explicitly.
	ModuleRoot string
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
	cmdDir, cmdPkg := resolveCmdTarget(opts, conf)

	programSpec := ToProgramSpec(spec)
	frameworkIn := NewRenderInput(pkg, programSpec)

	result := &RunResult{}

	// Render the single framework template into the configured
	// GenFile (default "rotini.gen.go"). One file holds spec,
	// inputs, handler interfaces, lifecycle, executors, program,
	// completion, and render — matching rotiniold's convention.
	frameworkBytes, err := Render(frameworkTemplate, frameworkIn)
	if err != nil {
		return nil, fmt.Errorf("internal: render %s: %w", frameworkTemplate, err)
	}
	frameworkPath := filepath.Join(outputDir, conf.Generate.Framework.GenFile)
	if err := fs.WriteFile(frameworkPath, frameworkBytes,
		fs.WithAtomic(true),
		fs.WithMkdirAll(true),
	); err != nil {
		return nil, fmt.Errorf("internal: write %s: %w", frameworkPath, err)
	}
	result.FilesWritten = append(result.FilesWritten, frameworkPath)

	if !opts.SkipBridge {
		if err := emitBridgeAndSkeletons(opts, conf, cmdDir, cmdPkg, outputDir, pkg, programSpec, result); err != nil {
			return nil, err
		}
	}

	sort.Strings(result.FilesWritten)
	sort.Strings(result.FilesSkipped)
	return result, nil
}

// emitBridgeAndSkeletons renders the user-handler bridge (always
// overwrite) and per-command skeleton stubs (write-only-if-missing)
// into cmdDir. Module-path resolution falls back to a go.mod walk
// from the spec file's directory; when unresolvable, this step is a
// no-op and bridge emission is reported through
// [RunResult.FilesSkipped] (a runnable test harness or the rotini
// binary can supply ModulePath explicitly).
func emitBridgeAndSkeletons(
	opts RunOptions,
	conf *Conf,
	cmdDir, cmdPkg, frameworkDir, frameworkPkg string,
	programSpec rtk.ProgramSpec,
	result *RunResult,
) error {
	modulePath := opts.ModulePath
	if modulePath == "" {
		if mp, err := findModulePath(filepath.Dir(opts.SpecPath)); err == nil {
			modulePath = mp
		}
	}
	if modulePath == "" {
		// No module path resolvable; skip bridge emission with a
		// breadcrumb so callers know what happened.
		result.FilesSkipped = append(result.FilesSkipped,
			filepath.Join(cmdDir, conf.Generate.Cmd.GenFile)+" (no module path resolvable; pass RunOptions.ModulePath or run inside a Go module)")
		return nil
	}

	frameworkImportPath, err := computeImportPath(modulePath, opts.ModuleRoot, frameworkDir)
	if err != nil {
		return fmt.Errorf("internal: compute framework import path: %w", err)
	}

	bridgeIn := NewRenderInput(cmdPkg, programSpec).
		WithFramework(frameworkImportPath, frameworkPkg)

	out, err := Render("bridge.gen.tmpl", bridgeIn)
	if err != nil {
		return fmt.Errorf("internal: render bridge.gen.tmpl: %w", err)
	}
	bridgePath := filepath.Join(cmdDir, conf.Generate.Cmd.GenFile)
	if err := fs.WriteFile(bridgePath, out,
		fs.WithAtomic(true),
		fs.WithMkdirAll(true),
	); err != nil {
		return fmt.Errorf("internal: write %s: %w", bridgePath, err)
	}
	result.FilesWritten = append(result.FilesWritten, bridgePath)

	// Render handler skeletons (one per command, plus root).
	paths := append([]string{""}, bridgeIn.SortedCommandPaths()...)
	for _, path := range paths {
		skelPath := filepath.Join(cmdDir, HandlerFilename(programSpec.Name, path))
		if fs.Exists(skelPath) {
			result.FilesSkipped = append(result.FilesSkipped, skelPath)
			continue
		}
		skelIn := bridgeIn.WithCurrentPath(path)
		skelBytes, err := Render("handler-skel.go.tmpl", skelIn)
		if err != nil {
			return fmt.Errorf("internal: render handler-skel for %q: %w", path, err)
		}
		if err := fs.WriteFile(skelPath, skelBytes,
			fs.WithAtomic(true),
			fs.WithMkdirAll(true),
		); err != nil {
			return fmt.Errorf("internal: write %s: %w", skelPath, err)
		}
		result.FilesWritten = append(result.FilesWritten, skelPath)
	}
	return nil
}

// computeImportPath produces the Go import path of the framework
// package, given the module's path (from go.mod), the module root
// directory (defaults to cwd), and the framework output directory.
//
// When dir is relative, it is interpreted as already-relative-to-
// module-root and appended directly. When dir is absolute, the
// result is modulePath + "/" + filepath.Rel(root, dir).
//
// Returns an error if dir is absolute but root is empty / not
// resolvable, or if dir escapes root.
func computeImportPath(modulePath, root, dir string) (string, error) {
	rel := dir
	if filepath.IsAbs(dir) {
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("get working directory: %w", err)
			}
			root = cwd
		}
		r, err := filepath.Rel(root, dir)
		if err != nil {
			return "", fmt.Errorf("compute rel(%s, %s): %w", root, dir, err)
		}
		rel = r
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", fmt.Errorf("%w: %s is outside module root %s", ErrPathOutsideModule, dir, root)
	}
	return modulePath + "/" + rel, nil
}

// ErrPathOutsideModule is returned by [computeImportPath] when the
// output directory resolves to a path outside the module root —
// e.g., the user passed an absolute OutputDir that doesn't sit
// under ModuleRoot.
var ErrPathOutsideModule = errors.New("internal: output directory is outside the module root")

// resolveCmdTarget figures out where bridge + skeleton files should
// land and what package name they should declare. Explicit options
// win over conf defaults.
func resolveCmdTarget(opts RunOptions, conf *Conf) (dir, pkg string) {
	dir = opts.CmdDir
	if dir == "" {
		dir = conf.Generate.Cmd.Package
	}
	pkg = opts.CmdPackage
	if pkg == "" {
		pkg = filepath.Base(dir)
		if strings.ContainsRune(pkg, filepath.Separator) || pkg == "." || pkg == "" {
			pkg = "cmd"
		}
	}
	return dir, pkg
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
