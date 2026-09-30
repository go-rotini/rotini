package codegen

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// Layout resolution: the conf's package targets → absolute output dirs, package
// names, and import paths (pruning lives in generate_prune.go, naming in generate_naming.go).

// resolveLayout turns the defaulted conf package settings into absolute output directories,
// package names and import paths.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) layout {
	cmd := conf.Generate.cmdTarget() // the single cmd target (post-default)

	cmdFile := filepath.ToSlash(cmd.File)
	cmdPkgDir := path.Dir(cmdFile)

	// The Go package name is the explicit conf `package` when set, else derived
	// from the target directory's last segment.
	cmdPkgName := goPkgName(cmdPkgDir)
	if cmd.Package != "" {
		cmdPkgName = cmd.Package
	}

	lay := layout{
		cmdDir:     filepath.Join(moduleRoot, filepath.FromSlash(cmdPkgDir)),
		cmdPkgName: cmdPkgName,
		cmdFile:    path.Base(cmdFile),
		cmdImport:  moduleName + "/" + cmdPkgDir,
		cmdHeader:  cmd.Header,
	}

	if ep := conf.Generate.mainPkg(); ep != nil && ep.File != "" {
		epFile := filepath.ToSlash(ep.File)
		lay.entrypointDir = filepath.Join(moduleRoot, filepath.FromSlash(path.Dir(epFile)))
		lay.entrypointFile = path.Base(epFile)
		lay.mainHeader = ep.Header
	}

	// The typed structs live in the cmd file unless a `models` target moves them to
	// their own package (see GenerateConfig.modelsPkg for why one would).
	if m := conf.Generate.modelsPkg(); m != nil && m.File != "" {
		mFile := filepath.ToSlash(m.File)
		mDir := path.Dir(mFile)
		lay.modelsPkgName = m.Package
		if lay.modelsPkgName == "" {
			lay.modelsPkgName = goPkgName(mDir)
		}
		lay.modelsDir = filepath.Join(moduleRoot, filepath.FromSlash(mDir))
		lay.modelsFile = path.Base(mFile)
		lay.modelsImport = moduleName + "/" + mDir
		lay.modelsHeader = m.Header
		// Pointing models at the cmd file is a no-op split: same file, same package.
		lay.splitModels = lay.modelsDir != lay.cmdDir || lay.modelsFile != lay.cmdFile
	}

	return lay
}

// goPkgName derives the Go package name from a directory path: its last segment with every
// character that is not a valid identifier rune dropped. A CLI directory may contain hyphens —
// "agentic-cooking" — where a package name cannot, so it becomes "agenticcooking". The import
// path keeps the original segment.
func goPkgName(dir string) string {
	var b strings.Builder
	for _, r := range filepath.Base(dir) {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyConfDefaults fills in the conf defaults for any unset generation setting, so a missing
// or partial conf still generates. The cmd package defaults to "internal/cmd/<root>/" with its
// generated file at zz_rotini.gen.go. The entrypoint gets no default: main.go is written only
// when the conf declares one.
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

	// The generated code defaults to one self-contained file beside the editable stubs. The
	// runtime is imported, not emitted, so it has no target at all.
	frameworkFile := "internal/cmd/" + rootName + "/zz_rotini.gen.go"
	if p := ensure(typeCmd); p.File == "" {
		p.File = frameworkFile
	}

	// Each feature defaults its output dir to "<framework-package>/renders", which must stay
	// under the framework so //go:embed can reach it, and its editable template to
	// "<framework-package>/templates". Co-located features cannot collide: each carries a
	// unique prefix and suffix, and pruning is scoped to them.
	frameworkDir := path.Dir(filepath.ToSlash(g.cmdTarget().File))
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

// layout holds the resolved package locations and import paths for one generation pass. The
// cmd package holds the editable handler stubs and the one generated file — framework and
// rollup merged, referencing each other unqualified since they share the package. The runtime
// is an ordinary library import, not generated, and the entrypoint package is optional.
type layout struct {
	cmdDir     string // absolute output dir for the cmd package (editable stubs + the generated file)
	cmdPkgName string // cmd package name, e.g. "mycli"
	cmdFile    string // basename of the single generated file (framework + rollup), e.g. "zz_rotini.gen.go"
	cmdImport  string // cmd package import path (the entrypoint's Program import)

	// cmdHeader, mainHeader and modelsHeader are each target's conf-declared `header:` —
	// a license block, a copyright line, a //go:build constraint — written verbatim above
	// rotini's own generated-code line on every file that target produces.
	cmdHeader    string
	mainHeader   string
	modelsHeader string

	entrypointDir  string // absolute output dir for the entrypoint main.go; "" when no entrypoint declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint declared

	// The OPTIONAL models package (see GenerateConfig.modelsPkg). splitModels is the
	// one flag the emitters branch on: false keeps the typed structs in the cmd file.
	modelsDir     string // absolute output dir for the models file
	modelsFile    string // basename of the generated models file
	modelsPkgName string // Go package name written atop it
	modelsImport  string // its import path, for the cmd package's aliases
	splitModels   bool   // true when models resolves somewhere other than the cmd file
}
