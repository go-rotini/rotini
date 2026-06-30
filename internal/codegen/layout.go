package codegen

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// Layout resolution: the conf's package targets → absolute output dirs, package
// names, and import paths (pruning lives in prune.go, naming in naming.go).

// resolveLayout turns the (defaulted) conf package settings into absolute output
// directories, package names, and import paths. The cmd package is the single
// `cmd` target — its directory holds the editable handler stubs and the one
// generated file (framework + rollup merged, unqualified). The entrypoint and
// runtime are optional/separate — their layout fields are set below.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) layout {
	cli := conf.Generate.cmdPkg() // the single cli (`cmd`) target

	cmdFile := filepath.ToSlash(cli.File)
	cliPkgDir := path.Dir(cmdFile)

	// The Go package name is the explicit conf `package` when set, else derived
	// from the target directory's last segment.
	cmdPkgName := goPkgName(cliPkgDir)
	if cli.Package != "" {
		cmdPkgName = cli.Package
	}

	lay := layout{
		cmdDir:     filepath.Join(moduleRoot, filepath.FromSlash(cliPkgDir)),
		cmdPkgName: cmdPkgName,
		cmdFile:    path.Base(cmdFile),
		cmdImport:  moduleName + "/" + cliPkgDir,
	}

	if ep := conf.Generate.mainPkg(); ep != nil && ep.File != "" {
		epFile := filepath.ToSlash(ep.File)
		lay.entrypointDir = filepath.Join(moduleRoot, filepath.FromSlash(path.Dir(epFile)))
		lay.entrypointFile = path.Base(epFile)
	}

	// The runtime is a separate emitted package the framework/handlers import: the
	// ENTIRE runtime merges into the single 'file' (runtimeFile), under runtimeDir
	// (its package). When that directory IS the embed source it is imported in place
	// and emission is skipped. The merged file declares runtimePkgName.
	if rt := conf.Generate.runtimePkg(); rt != nil && rt.File != "" {
		rtFile := filepath.ToSlash(rt.File)
		lay.runtimeDir = path.Dir(rtFile)
		lay.runtimeFile = path.Base(rtFile)
		lay.runtimePkgName = rt.Package
		if lay.runtimePkgName == "" {
			lay.runtimePkgName = goPkgName(lay.runtimeDir)
		}
		lay.runtimeImport = runtimeImportSpec(moduleName, lay.runtimeDir)
		lay.skipRuntimeEmit = lay.runtimeDir == runtimeSourceDir
	}

	return lay
}

// goPkgName derives the Go package NAME from a package directory path: its last
// segment with every character that is not a valid Go identifier rune dropped.
// A CLI name (and so its scaffold directory) may legitimately contain hyphens —
// "agentic-cooking" — but a Go package name cannot, so the package is named
// "agenticcooking". The import PATH keeps the original segment (import paths may
// contain hyphens); only the package name (and its qualifier) is sanitized.
func goPkgName(dir string) string {
	var b strings.Builder
	for _, r := range filepath.Base(dir) {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyConfDefaults fills in the sane rotini conf defaults for any unset
// generation settings, so a missing or partial conf still generates. The default
// cmd package is "internal/cmd/<root>/" with its generated file at
// "internal/cmd/<root>/zz_rotini.gen.go" (the `cmd` target — framework + rollup
// + typed inputs in one file), and the runtime in a "rotini" subpackage beside it.
// The entrypoint gets no default — main.go is only written when the conf declares a
// main.file. rootName is the spec's root command name, used to build the paths.
func applyConfDefaults(conf *Conf, rootName string) {
	if conf.Generate == nil {
		conf.Generate = &GenerateConfig{}
	}
	g := conf.Generate

	// ensure returns the package target of the given type, appending a fresh entry
	// when absent. The returned pointer is used before the next ensure call (whose
	// append may reallocate), so it stays valid.
	ensure := func(typ string) *PackageConfig {
		if p := g.packageOf(typ); p != nil {
			return p
		}
		g.Packages = append(g.Packages, PackageConfig{Type: typ})
		return &g.Packages[len(g.Packages)-1]
	}

	// The per-CLI generated code defaults to one self-contained file
	// "internal/cmd/<root>/zz_rotini.gen.go" — the `cmd` target (framework + rollup
	// + typed inputs, beside the editable stubs). The RUNTIME is a separate emitted
	// package, defaulting to a "rotini" subpackage beside it —
	// "internal/cmd/<root>/rotini/zz_runtime.gen.go" — the single file the entire
	// runtime merges into, which the framework imports. main gets no default
	// (written only when the conf declares its file).
	frameworkFile := "internal/cmd/" + rootName + "/zz_rotini.gen.go"
	runtimeFile := "internal/cmd/" + rootName + "/rotini/zz_runtime.gen.go"
	if p := ensure(typeCmd); p.File == "" {
		p.File = frameworkFile
	}
	if p := ensure(typeRuntime); p.File == "" {
		p.File = runtimeFile
	}

	// Each present feature defaults its two dirs from the framework package
	// (module-relative): rendered OUTPUT files to "<framework-package>/renders"
	// (always under the framework so //go:embed can reach them in embed mode), and
	// the editable TEMPLATE to "<framework-package>/templates". Co-located features
	// cannot collide: each carries a feature-unique suffix/prefix (see docFeature)
	// and pruning is scoped to them.
	frameworkDir := path.Dir(filepath.ToSlash(g.cmdPkg().File))
	for i := range g.Features {
		f := &g.Features[i]
		if f.EmbedDir == "" {
			f.EmbedDir = frameworkDir + "/renders"
		}
		if f.TemplateDir == "" {
			f.TemplateDir = frameworkDir + "/templates"
		}
	}
}

// layout holds the resolved package locations and import paths for a single generation
// pass. The cmd package (the conf's single `cmd` target) holds the editable handler
// stubs AND the one generated file — the framework (Definition, NewProgram,
// ProgramHandlers, the typed inputs) and the rollup (the handlers struct + Program + the
// command→handler wiring) merged into it, all referencing each other unqualified since
// they share the package. The runtime is the only separate, imported package. The
// entrypoint package is optional: when the conf declares one, generate writes the
// binary's main.go there (create-once).
type layout struct {
	cmdDir     string // absolute output dir for the cmd package (editable stubs + the generated file)
	cmdPkgName string // cmd package name, e.g. "mycli"
	cmdFile    string // basename of the single generated file (framework + rollup), e.g. "zz_rotini.gen.go"
	cmdImport  string // cmd package import path (the entrypoint's Program import)

	entrypointDir  string // absolute output dir for the entrypoint main.go; "" when no entrypoint declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint declared

	runtimeImport   string // pre-rendered runtime import spec line, e.g. `rotini "…/internal/runtime"` or `"…/rotini"` (identifier always `rotini`)
	runtimeDir      string // module-relative dir the emitted runtime is written into (slash path) — its package directory
	runtimeFile     string // basename of the single file the ENTIRE runtime merges into, e.g. "zz_runtime.gen.go"
	runtimePkgName  string // Go package name written atop the merged runtime file, e.g. "rotini"
	skipRuntimeEmit bool   // true when runtimeDir IS rotini's own embed source (internal/runtime) — import in place, write nothing
}
