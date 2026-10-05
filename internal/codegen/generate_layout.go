package codegen

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// resolveLayout turns the defaulted conf package settings into absolute output directories,
// package names and import paths.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) layout {
	cmd := conf.Generate.cmdTarget()

	cmdFile := filepath.ToSlash(cmd.File)
	cmdPkgDir := path.Dir(cmdFile)

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

	// The typed structs live in the cmd file unless a `models` target moves them.
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

// goPkgName derives a Go package name from a directory path: its last segment with every
// non-identifier rune dropped, so "agentic-cooking" becomes "agenticcooking". The import path
// keeps the original segment.
func goPkgName(dir string) string {
	var b strings.Builder
	for _, r := range filepath.Base(dir) {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyConfDefaults fills in defaults for unset generation settings, so a missing or partial
// conf still generates. The cmd target defaults to internal/cmd/<root>/zz_rotini.go. The
// entrypoint has no default: main.go is written only when the conf declares one.
func applyConfDefaults(conf *Conf, rootName string) {
	if conf.Generate == nil {
		conf.Generate = &GenerateConfig{}
	}
	g := conf.Generate

	// ensure returns the package target of the given type, appending one when absent. The
	// pointer must be used before the next ensure call, whose append may reallocate.
	ensure := func(typ string) *PackageConfig {
		if p := g.packageOf(typ); p != nil {
			return p
		}
		g.Packages = append(g.Packages, PackageConfig{Type: typ})
		return &g.Packages[len(g.Packages)-1]
	}

	frameworkFile := "internal/cmd/" + rootName + "/zz_rotini.go"
	if p := ensure(typeCmd); p.File == "" {
		p.File = frameworkFile
	}

	// Feature embed_dir defaults to <cmd-package>/renders (it must sit under the cmd package
	// for //go:embed to reach it) and template_dir to <cmd-package>/templates. Features sharing
	// a directory cannot collide: each owns distinct file names, and pruning is scoped to them.
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
// cmd package holds the editable handler stubs and the single generated file. The entrypoint
// and models packages are optional.
type layout struct {
	cmdDir     string // absolute output dir for the cmd package
	cmdPkgName string // cmd package name, e.g. "mycli"
	cmdFile    string // basename of the generated file, e.g. "zz_rotini.go"
	cmdImport  string // cmd package import path

	// cmdHeader, mainHeader and modelsHeader are each target's conf `header:` (a license
	// block, a //go:build constraint), written verbatim above the generated-code line.
	cmdHeader    string
	mainHeader   string
	modelsHeader string

	entrypointDir  string // absolute output dir for main.go; "" when no entrypoint is declared
	entrypointFile string // entrypoint file name, e.g. "main.go"; "" when no entrypoint is declared

	// splitModels is the one flag the emitters branch on: false keeps the typed structs in
	// the cmd file.
	modelsDir     string // absolute output dir for the models file
	modelsFile    string // basename of the generated models file
	modelsPkgName string // models package name
	modelsImport  string // models import path
	splitModels   bool   // true when models resolves somewhere other than the cmd file
}
