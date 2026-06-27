package codegen

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Layout resolution, output pruning, and the naming / import-path helpers that
// place generated code and keep regeneration idempotent.

// pruneStubs removes handler .go files in the cli package that no longer correspond
// to an own command, preserving the generated cli file, the keep list, and any test
// files. The cli package is flat, so keepList entries (package-relative) are just file
// names for its top-level stubs. When the entrypoint shares the cli package, its
// create-once main.go is protected too (otherwise it would be pruned as an orphan).
func pruneStubs(gp *genProgram, lay layout, keepList []string) error {
	protected := map[string]bool{
		gp.root.filename: true,
		lay.cliFile:      true,
	}
	if lay.entrypointDir == lay.cliDir && lay.entrypointFile != "" {
		protected[lay.entrypointFile] = true
	}
	for _, c := range gp.own {
		protected[c.filename] = true
	}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.cliDir, protected)
}

// pruneEntrypoint removes orphaned .go files in the entrypoint directory, so a
// `keep` list on the main package is honored (the main.go itself is create-once
// and always protected; test files are kept automatically). It is a no-op when no
// entrypoint is declared, or when the entrypoint shares the cli package directory —
// pruneStubs already covers that dir (and is passed the merged keep list).
func pruneEntrypoint(lay layout, keepList []string) error {
	if lay.entrypointDir == "" || lay.entrypointDir == lay.cliDir {
		return nil
	}
	protected := map[string]bool{lay.entrypointFile: true}
	for _, k := range keepList {
		protected[filepath.ToSlash(k)] = true
	}
	return pruneGoDir(lay.entrypointDir, protected)
}

// pruneGoDir removes every non-test .go file in dir whose base name is not in the
// protected set. Sub-directories and *_test.go files are never touched.
func pruneGoDir(dir string, protected map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if protected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("prune %s: %w", name, err)
		}
	}
	return nil
}

// pruneCligen removes orphaned rotini-managed outputs in each enabled feature's
// dir (under the cli package) — the per-command pages for commands no longer
// in the spec. Only files matching the feature's unique suffix AND prefix are
// candidates, so features sharing one embed dir never prune each other's files.
// The editable per-feature template, test files, and any keep-listed
// (package-relative) path are preserved. Top-level cli package files (the gen file)
// are never auto-removed. keepList entries are package-relative to the cli
// package.
func pruneCligen(lay layout, keepList []string, outputs []featureOutput) error {
	keep := make(map[string]bool, len(keepList))
	for _, k := range keepList {
		keep[filepath.ToSlash(k)] = true
	}
	for _, o := range outputs {
		// The editable template is always protected (it is the author's content,
		// never pruned even when template:false leaves it inert). The current
		// command set's output pages are protected only in embed mode — an inline
		// feature writes none, so any on-disk pages are stale and get pruned.
		// Pruning scans the embed_dir for stale OUTPUT files. The editable
		// template lives in template_dir (a different tree) and is rotini's only
		// managed file there, so it is never a prune candidate. The current
		// command set's output files are protected only in embed mode — inline
		// features write none, so any on-disk pages are stale and get pruned.
		protected := map[string]bool{}
		if o.embed {
			for _, n := range o.nodes {
				protected[n.file] = true
			}
		}

		entries, err := os.ReadDir(o.absEmbedDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s embed_dir %s: %w", o.desc.name, o.absEmbedDir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, o.desc.ext) || strings.HasSuffix(name, "_test"+o.desc.ext) {
				continue
			}
			if o.desc.filePrefix != "" && !strings.HasPrefix(name, o.desc.filePrefix) {
				continue
			}
			if protected[name] {
				continue
			}
			// keep entries are package-relative (to the cli package).
			rel := name
			if r, err := filepath.Rel(lay.cliDir, filepath.Join(o.absEmbedDir, name)); err == nil {
				rel = filepath.ToSlash(r)
			}
			if keep[rel] {
				continue
			}
			if err := os.Remove(filepath.Join(o.absEmbedDir, name)); err != nil {
				return fmt.Errorf("prune %s: %w", rel, err)
			}
		}
	}
	return nil
}

// resolveLayout turns the (defaulted) conf package settings into absolute output
// directories, package names, and import paths. The cli package is the single
// `cmd` target — its directory holds the editable handler stubs and the one
// generated file (framework + rollup merged, unqualified). The entrypoint and
// runtime are optional/separate — their layout fields are set below.
func resolveLayout(conf *Conf, moduleRoot, moduleName string) layout {
	cli := conf.Generate.cmdPkg() // the single cli (`cmd`) target

	cliFile := filepath.ToSlash(cli.File)
	cliPkgDir := path.Dir(cliFile)

	// The Go package name is the explicit conf `package` when set, else derived
	// from the target directory's last segment.
	cliPkgName := goPkgName(cliPkgDir)
	if cli.Package != "" {
		cliPkgName = cli.Package
	}

	lay := layout{
		cliDir:     filepath.Join(moduleRoot, filepath.FromSlash(cliPkgDir)),
		cliPkgName: cliPkgName,
		cliFile:    path.Base(cliFile),
		cliImport:  moduleName + "/" + cliPkgDir,
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
// cli package is "internal/cmd/<root>/" with its generated file at
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
	if p := ensure("cmd"); p.File == "" {
		p.File = frameworkFile
	}
	if p := ensure("runtime"); p.File == "" {
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

// fieldImport returns the Go import path backing a field's schema: the explicit
// spec `import:` when set, otherwise the import rotini knows is needed for its own
// built-in type aliases (duration/time/datetime/date → "time"). "" means no import.
func fieldImport(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	if imp := strings.TrimSpace(schema.Import); imp != "" {
		return imp
	}
	// An array's element type carries the import: explicit items.import first,
	// then the built-in vocabulary (items duration → "time").
	if schema.Items != nil && jsonSchemaTypeToGo(schema.Type) == "[]string" {
		if imp := strings.TrimSpace(schema.Items.Import); imp != "" {
			return imp
		}
		return builtinImport(schema.Items.Type)
	}
	return builtinImport(schema.Type)
}

// builtinImport returns the import path rotini's own type vocabulary requires, or
// "" when the type needs none. Only the time-family aliases (which jsonSchemaTypeToGo
// maps to time.Time/time.Duration) carry an implicit import.
func builtinImport(rotiniType string) string {
	switch rotiniType {
	case "duration", "time", "datetime", "date":
		return "time"
	}
	return ""
}

// parseAliasPath splits the `alias path` external-Go-binding form (shared by an input
// type's `import:` and a command's `handler.import` — D-W9.5) into its alias and path;
// a bare path derives its alias from the last segment.
func parseAliasPath(imp string) (alias, importPath string) {
	imp = strings.TrimSpace(imp)
	if a, p, ok := strings.Cut(imp, " "); ok {
		return strings.TrimSpace(a), strings.TrimSpace(p)
	}
	return identAlias(filepath.Base(imp)), imp
}

// renderImports turns a set of spec `import:` values into sorted Go import specs:
// a plain path becomes "path"; the aliased form "alias path" becomes alias "path".
func renderImports(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for imp := range set {
		if alias, path, ok := strings.Cut(imp, " "); ok {
			out = append(out, alias+" "+strconv.Quote(strings.TrimSpace(path)))
		} else {
			out = append(out, strconv.Quote(imp))
		}
	}
	sort.Strings(out)
	return out
}

// toPascalCase converts a name to PascalCase, treating '-', '_' and ' ' as word
// boundaries (e.g. "foo_bar" -> "FooBar", "generate" -> "Generate").
func toPascalCase(s string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range s {
		if r == '-' || r == '_' || r == ' ' {
			capitalize = true
			continue
		}
		if capitalize {
			b.WriteRune(unicode.ToUpper(r))
			capitalize = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lowerFirst returns s with its first rune lower-cased.
func lowerFirst(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// goReservedFilenames are the trailing "_"-separated tokens the go tool reads
// specially from a file's name alone: "test" (a "_test.go" test file, excluded from
// the normal build) and the GOOS/GOARCH names (an implicit build constraint, e.g.
// "app_windows.go" builds only on Windows). Kept as one set since stubFilename only
// needs membership, not which rule matched.
var goReservedFilenames = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []string{
		"test",
		// GOOS
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
		"wasip1", "windows", "zos",
		// GOARCH
		"386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64",
		"mips", "mips64", "mips64le", "mips64p32", "mips64p32le", "mipsle", "ppc",
		"ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64",
		"wasm",
	} {
		m[s] = true
	}
	return m
}()

// reservedTrailingToken reports whether stem's trailing "_"-separated token is one the
// go tool reads specially from a file's name — "test" (a "_test.go" test file) or a
// GOOS/GOARCH (an implicit build constraint).
func reservedTrailingToken(stem string) bool {
	parts := strings.Split(stem, "_")
	return goReservedFilenames[parts[len(parts)-1]]
}

// stubFilename builds a handler-stub file name from base (a command's root name or
// "<root>_<path>"), escaping the names the go tool would read specially from the
// filename alone — a "_test.go" test file, or a "_<GOOS>.go"/"_<GOARCH>.go" build
// constraint — by appending a trailing underscore. That makes the trailing
// "_"-separated token empty, which matches none of those rules, so a command named
// "test"/"windows"/"wasm"/… still compiles into the ordinary build.
func stubFilename(base string) string {
	if reservedTrailingToken(base) {
		base += "_"
	}
	return base + ".go"
}

// commandStubFilename returns a command's handler-stub file name: its explicit
// `filename` override when set, else the derived "<root>[_<path>].go" (reserved-name
// escaped by stubFilename). path is the underscore-joined command path relative to the
// root, "" for the root command itself. The same derivation is shared by codegen (to
// name the stub) and lintHandlerFilenames (to validate uniqueness), so they agree.
func commandStubFilename(rootName, path, override string) string {
	if override != "" {
		return override
	}
	base := rootName
	if path != "" {
		base += "_" + path
	}
	return stubFilename(base)
}
