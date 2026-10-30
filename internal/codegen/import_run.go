package codegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ImporterVersion is the github.com/go-rotini/import release `rotini import` runs unless
// --importer-version names another.
const ImporterVersion = "v0.2.2"

// importerModule is the importer's module; each framework's importer is a package in it
// (importerModule + "/cobra").
const importerModule = "github.com/go-rotini/import"

// importTestTemplate is the test file an import adds to the program's package, through
// go test -overlay; nothing is written in the program's tree. It compiles into the package,
// so the root expression may name unexported identifiers, and every name it declares or
// imports carries a rotiniImport prefix so it can't collide with the package's own. The
// verbs are the package name, the importer's package, the test name, the importer call and
// the package's own imports the root expression names (see rootImports).
const importTestTemplate = `package %[1]s

import (
	rotiniimport "github.com/go-rotini/import"
	rotiniimport%[2]s "github.com/go-rotini/import/%[2]s"
	rotiniimportos "os"
	rotiniimporttesting "testing"%[5]s
)

func %[3]s(rotiniImportT *rotiniimporttesting.T) {
	rotiniImportResult, rotiniImportErr := %[4]s
	if rotiniImportErr != nil {
		rotiniImportT.Fatal(rotiniImportErr)
	}
	if rotiniImportErr := rotiniimport.WriteFile(rotiniimportos.Getenv("ROTINI_IMPORT_OUT"), rotiniImportResult); rotiniImportErr != nil {
		rotiniImportT.Fatal(rotiniImportErr)
	}
}
`

// importRun is one run of the importer against a package of the module at moduleRoot.
type importRun struct {
	ctx        context.Context
	moduleRoot string
	opts       ImportOptions
	fw         importFramework
	work       string // the temporary directory holding the modfile, overlay and output
	modfile    string
	// standIn maps the module root's go.mod to a copy in work when the module is in
	// vendor.mod: the go command finds the module root by its go.mod, and an overlay supplies
	// one without writing into the program's tree.
	standIn   map[string]string
	workspace bool // the environment names a go.work, which the run turns off
}

// importRunner reads the program's tree; tests replace it with a fixed description.
var importRunner = runImporter

// runImporter runs the importer over opts.Package and returns its description, plus info
// notes about the run itself (the root it picked, a framework upgrade). The program's files
// and go.mod (or vendor.mod) are never written: the importer is added to a copy of the
// module file in a temporary directory, which is removed on every path.
func runImporter(ctx context.Context, moduleRoot string, opts ImportOptions) (*importResult, []importNote, error) {
	fw, err := frameworkFor(opts)
	if err != nil {
		return nil, nil, err
	}
	work, err := os.MkdirTemp("", "rotini-import-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(work)

	r := &importRun{ctx: ctx, moduleRoot: moduleRoot, opts: opts, fw: fw, work: work, modfile: filepath.Join(work, "import.mod")}
	gowork := exec.CommandContext(ctx, "go", "env", "GOWORK")
	if gw, err := gowork.Output(); err == nil {
		v := strings.TrimSpace(string(gw))
		r.workspace = v != "" && v != "off"
	}
	if err := r.prepareModfile(); err != nil {
		return nil, nil, err
	}

	var notes []importNote
	listed, err := r.goOutput(r.modArgs("list", r.modfile, "-f", "{{.Dir}}\n{{.Name}}\n{{.ImportPath}}", opts.Package)...)
	if err != nil {
		return nil, nil, r.explainBuild(err)
	}
	pkg := strings.SplitN(listed, "\n", 3)
	if len(pkg) != 3 {
		return nil, nil, fmt.Errorf("go list %s: unexpected output %q", opts.Package, listed)
	}
	pkgDir, pkgName, importPath := pkg[0], pkg[1], pkg[2]

	root := opts.Root
	if root == "" {
		var how string
		if root, how, err = fw.findRoot(pkgDir); err != nil {
			return nil, nil, err
		}
		notes = append(notes, importNote{Level: "info", Msg: fmt.Sprintf("the root is %s (%s); pass --root to choose another", root, how)})
	}
	if n, ok := r.frameworkUpgrade(); ok {
		notes = append(notes, n)
	}

	res, err := r.runTest(pkgDir, pkgName, fw.call(root, opts, binaryName(importPath)))
	if err != nil {
		return nil, nil, err
	}
	for i := range notes {
		notes[i].Path = res.Command.Name
	}
	return res, notes, nil
}

// prepareModfile copies go.mod and go.sum (or vendor.mod and vendor.sum) into the work
// directory and adds the importer to the copy: the release ImporterVersion (or
// --importer-version) names, or a local checkout.
func (r *importRun) prepareModfile() error {
	modName, sumName := "go.mod", "go.sum"
	if _, err := os.Stat(filepath.Join(r.moduleRoot, "go.mod")); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(r.moduleRoot, "vendor.mod")); err == nil {
			modName, sumName = "vendor.mod", "vendor.sum"
			r.standIn = map[string]string{filepath.Join(r.moduleRoot, "go.mod"): filepath.Join(r.work, "orig.mod")}
		}
	}
	gomod, err := os.ReadFile(filepath.Join(r.moduleRoot, modName))
	if err != nil {
		return fmt.Errorf("read %s: %w", modName, err)
	}
	gosum, err := os.ReadFile(filepath.Join(r.moduleRoot, sumName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", sumName, err)
	}
	// import.mod gets the importer; orig.mod stays as the program has it, so its versions
	// can be compared without touching the program's own go.mod.
	copies := map[string][]byte{"import.mod": gomod, "import.sum": gosum, "orig.mod": gomod, "orig.sum": gosum}
	for name, data := range copies {
		if err := os.WriteFile(filepath.Join(r.work, name), data, 0o600); err != nil {
			return fmt.Errorf("copy %s and %s: %w", modName, sumName, err)
		}
	}
	if r.standIn != nil {
		if err := r.writeOverlay(nil); err != nil {
			return err
		}
	}

	version := r.opts.ImporterVersion
	if version == "" {
		version = ImporterVersion
	}
	if local := localImporter(version); local != "" {
		abs, err := filepath.Abs(local)
		if err != nil {
			return fmt.Errorf("--importer-version %s: %w", version, err)
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return fmt.Errorf("--importer-version %s is not a checkout of %s: %w", version, importerModule, err)
		}
		// Naming the file, not -modfile, edits it without looking for the module root.
		_, err = r.goOutput("mod", "edit",
			"-require="+importerModule+"@v0.0.0", "-replace="+importerModule+"="+abs, r.modfile)
		return err
	}
	if _, err := r.goOutput(r.modArgs("get", r.modfile, importerModule+"@"+version)...); err != nil {
		return fmt.Errorf("add the importer %s@%s (this needs the network or the module cache): %w", importerModule, version, err)
	}
	return nil
}

// localImporter returns v when it names a directory (an absolute path, or one starting
// with . ), the way --importer-version selects a local checkout of the importer, else "".
func localImporter(v string) string {
	if filepath.IsAbs(v) || strings.HasPrefix(v, ".") {
		return v
	}
	return ""
}

// frameworkUpgrade reports, as an info note, when adding the importer raised the version of
// the program's framework.
func (r *importRun) frameworkUpgrade() (importNote, bool) {
	before, err := r.goOutput(r.modArgs("list", filepath.Join(r.work, "orig.mod"), "-m", "-f", "{{.Version}}", r.fw.module)...)
	if err != nil {
		return importNote{}, false
	}
	after, err := r.goOutput(r.modArgs("list", r.modfile, "-m", "-f", "{{.Version}}", r.fw.module)...)
	if err != nil || after == before {
		return importNote{}, false
	}
	return importNote{Level: "info", Msg: fmt.Sprintf("%s upgraded from %s to %s for the import; the tree may differ", r.fw.label, before, after)}, true
}

// runTest adds the import test, which runs call, to the package through an overlay, with
// the package's own test files blanked (their tests, TestMain and compile errors stay out),
// runs it, and reads the description it writes.
func (r *importRun) runTest(pkgDir, pkgName, call string) (*importResult, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("prepare the import test: %w", err)
	}
	testName := "TestRotiniImport_" + hex.EncodeToString(suffix)
	testFile := filepath.Join(r.work, "zz_rotini_import_test.go")
	imports, ambiguous := rootImports(pkgDir, r.opts.Root, r.buildContext().MatchFile, r.packageNames)
	src := fmt.Sprintf(importTestTemplate, pkgName, r.fw.pkg, testName, call, imports)
	if err := os.WriteFile(testFile, []byte(src), 0o600); err != nil {
		return nil, fmt.Errorf("prepare the import test: %w", err)
	}
	replace := map[string]string{filepath.Join(pkgDir, "zz_rotini_import_test.go"): testFile}
	existing, err := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
	if err != nil {
		return nil, fmt.Errorf("prepare the import test: %w", err)
	}
	for _, f := range existing {
		replace[f] = ""
	}
	if err := r.writeOverlay(replace); err != nil {
		return nil, err
	}
	overlayFile := filepath.Join(r.work, overlayName)

	timeout := r.opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	out := filepath.Join(r.work, "out.json")
	stderr, err := r.goRun(r.testArgs(overlayFile, testName, timeout), "ROTINI_IMPORT_OUT="+out)
	if err != nil {
		if pkg := importerModule + "/" + r.fw.pkg; strings.Contains(stderr, "does not contain package "+pkg) ||
			strings.Contains(stderr, "no required module provides package "+pkg) {
			version := r.opts.ImporterVersion
			if version == "" {
				version = ImporterVersion
			}
			return nil, fmt.Errorf("the importer %s@%s has no %s package; pass an --importer-version that has it", importerModule, version, r.fw.pkg)
		}
		err = explainTestFailure(r.ctx, r.fw.pkg, err, stderr)
		if len(ambiguous) > 0 {
			err = fmt.Errorf("%w\nthe package's files import %s under one name, so the root expression gets none of them; "+
				"name the import in the expression's own way, or pass a --root that doesn't use it", err, strings.Join(ambiguous, " and "))
		}
		return nil, err
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("the importer wrote no description: %w\n%s", err, lastLines(stderr, 20))
	}
	defer f.Close()
	return readImportResult(f)
}

// overlayName is the -overlay file every go command of the run reads, once written.
const overlayName = "overlay.json"

// writeOverlay writes the run's -overlay file: replace plus the go.mod stand-in, if any.
func (r *importRun) writeOverlay(replace map[string]string) error {
	all := map[string]string{}
	maps.Copy(all, replace)
	maps.Copy(all, r.standIn)
	overlay, err := json.Marshal(map[string]any{"Replace": all})
	if err != nil {
		return fmt.Errorf("prepare the import: %w", err)
	}
	if err := os.WriteFile(filepath.Join(r.work, overlayName), overlay, 0o600); err != nil {
		return fmt.Errorf("prepare the import: %w", err)
	}
	return nil
}

// modArgs is a go command line for verb that reads modfile as the module file, plus the
// run's overlay when it stands in a go.mod, followed by rest.
func (r *importRun) modArgs(verb, modfile string, rest ...string) []string {
	args := []string{verb, "-modfile=" + modfile}
	if verb != "get" {
		args = append(args, "-mod=mod")
	}
	if r.standIn != nil {
		args = append(args, "-overlay="+filepath.Join(r.work, overlayName))
	}
	return append(args, rest...)
}

// buildContext is the build configuration the import test compiles under: the environment's
// GOOS and GOARCH, plus --tags.
func (r *importRun) buildContext() *build.Context {
	ctx := build.Default
	if r.opts.Tags != "" {
		ctx.BuildTags = strings.FieldsFunc(r.opts.Tags, func(c rune) bool { return c == ',' || c == ' ' })
	}
	return &ctx
}

// packageNames asks the go command for the package name of each import path; a path it
// can't load is left out.
func (r *importRun) packageNames(paths []string) map[string]string {
	out, err := r.goOutput(r.modArgs("list", r.modfile, append([]string{"-e", "-f", "{{.ImportPath}} {{.Name}}"}, paths...)...)...)
	names := map[string]string{}
	if err != nil {
		return names
	}
	for line := range strings.SplitSeq(out, "\n") {
		if path, name, ok := strings.Cut(line, " "); ok && name != "" {
			names[path] = name
		}
	}
	return names
}

// testArgs is the go test command line that runs the import test. Vet is off: the probe
// needs none, and before Go 1.23 vet doesn't read overlay files, so it fails on the import test.
func (r *importRun) testArgs(overlayFile, testName string, timeout time.Duration) []string {
	args := []string{"test", "-modfile", r.modfile, "-mod=mod", "-overlay", overlayFile, "-vet=off", "-count=1",
		"-run", "^" + testName + "$", "-timeout", timeout.String()}
	if r.opts.Tags != "" {
		args = append(args, "-tags", r.opts.Tags)
	}
	return append(args, r.opts.Package)
}

// explainTestFailure names the cause of a failed import run when it is a known one, such as
// the importer's own error (prefixed "<pkg> import:"), else shows the end of go test's output.
func explainTestFailure(ctx context.Context, pkg string, err error, output string) error {
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("import stopped: %w", context.Cause(ctx))
	case strings.Contains(output, "flag provided but not defined: -test."):
		return errors.New("the package calls flag.Parse in an init function, which runs before go test defines its own flags; " +
			"move the flag.Parse call into main, or import from a package that doesn't call it")
	}
	for line := range strings.SplitSeq(output, "\n") {
		if i := strings.Index(line, pkg+" import: "); i >= 0 {
			return errors.New(strings.TrimSpace(line[i:]))
		}
	}
	if strings.Contains(output, "panic: test timed out") {
		return fmt.Errorf("the import timed out; the package's init or the root expression may be waiting on something (raise --timeout if it is only slow)\n%s", lastLines(output, 20))
	}
	return fmt.Errorf("the import failed: %w\n%s", err, lastLines(output, 20))
}

// explainBuild adds why a package that doesn't list may fail in this run: imports run with
// go.work turned off.
func (r *importRun) explainBuild(err error) error {
	if r.workspace {
		return fmt.Errorf("%w\nrotini import runs with GOWORK=off, because a temporary go.mod can't be used in workspace mode; "+
			"import from a package that builds from its own module", err)
	}
	return err
}

// goOutput runs go with args in the working directory and returns its trimmed stdout.
func (r *importRun) goOutput(args ...string) (string, error) {
	cmd := exec.CommandContext(r.ctx, "go", args...)
	cmd.Env = importGoEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// goRun runs go with args and extra environment in the working directory, with stdin empty, and
// returns its combined output.
func (r *importRun) goRun(args []string, env ...string) (string, error) {
	cmd := exec.CommandContext(r.ctx, "go", args...)
	cmd.Env = append(importGoEnv(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// importGoEnv is the environment for every go command an import runs: GOWORK off, since
// -modfile is refused in workspace mode, and GOFLAGS without -mod or -modfile, which the run
// sets itself.
func importGoEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	var flags []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GOWORK="):
		case strings.HasPrefix(kv, "GOFLAGS="):
			for f := range strings.FieldsSeq(strings.TrimPrefix(kv, "GOFLAGS=")) {
				if !strings.HasPrefix(f, "-mod=") && !strings.HasPrefix(f, "-modfile=") {
					flags = append(flags, f)
				}
			}
		default:
			env = append(env, kv)
		}
	}
	return append(env, "GOWORK=off", "GOFLAGS="+strings.Join(flags, " "))
}

// lastLines returns the last n non-empty lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// rootImports returns the import lines, each starting with a newline and a tab, for the
// packages a --root expression names that the package imports in its own files: imports are
// per file, so the import test must repeat them for the expression to resolve as it would in
// the package. Only files the build includes (match, for GOOS, GOARCH and tags) count. A name
// the package declares itself is left alone, and so is a name its files import from different
// paths, which is returned in ambiguous. pkgNames, when not nil, gives the package name of each
// import path, for an imported package whose name isn't the one its path suggests.
func rootImports(pkgDir, root string, match func(dir, name string) (bool, error), pkgNames func(paths []string) map[string]string) (lines string, ambiguous []string) {
	used := selectorPackages(root)
	if len(used) == 0 {
		return "", nil
	}
	declared, imports := packageScope(pkgDir, match)
	paths := importNames(imports, nil)
	if pkgNames != nil {
		for name := range used {
			if _, ok := paths[name]; !ok && !declared[name] {
				var unnamed []string
				for _, imp := range imports {
					if imp.name == "" {
						unnamed = append(unnamed, imp.path)
					}
				}
				if len(unnamed) > 0 {
					paths = importNames(imports, pkgNames(unnamed))
				}
				break
			}
		}
	}
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(used)) {
		found, ok := paths[name]
		if !ok || declared[name] || strings.HasPrefix(name, "rotiniimport") {
			continue
		}
		if len(found) > 1 {
			ambiguous = append(ambiguous, fmt.Sprintf("%s (%s)", name, strings.Join(found, ", ")))
			continue
		}
		fmt.Fprintf(&b, "\n\t%s %q", name, found[0])
	}
	return b.String(), ambiguous
}

// importNames maps each name the package's files import under to the distinct paths imported
// under it, sorted. An unnamed import goes under its package name from pkgNames, else under the
// name its path suggests.
func importNames(imports []importSpec, pkgNames map[string]string) map[string][]string {
	paths := map[string][]string{}
	for _, imp := range imports {
		name := imp.name
		if name == "" {
			name = pkgNames[imp.path]
			if name == "" {
				name = defaultImportName(imp.path)
			}
		}
		if name == "_" || name == "." || slices.Contains(paths[name], imp.path) {
			continue
		}
		paths[name] = append(paths[name], imp.path)
		slices.Sort(paths[name])
	}
	return paths
}

// selectorPackages returns the identifiers a Go expression selects from (the x of x.Y), the
// names that may be packages.
func selectorPackages(expr string) map[string]bool {
	e, err := parser.ParseExpr(expr)
	if err != nil {
		return nil
	}
	used := map[string]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	return used
}

// importSpec is one import of one file: its explicit name ("" for none) and path.
type importSpec struct {
	name, path string
}

// packageScope reads the package in dir, test files and files match excludes aside: the
// names it declares at package level, and every import of its files.
func packageScope(dir string, match func(dir, name string) (bool, error)) (declared map[string]bool, imports []importSpec) {
	declared = map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return declared, nil
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		if match != nil {
			if ok, err := match(dir, filepath.Base(file)); err == nil && !ok {
				continue
			}
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, name := range declaredNames(f) {
			declared[name] = true
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			spec := importSpec{path: path}
			if imp.Name != nil {
				spec.name = imp.Name.Name
			}
			imports = append(imports, spec)
		}
	}
	return declared, imports
}

// declaredNames lists the names f declares at package level: functions, variables,
// constants and types.
func declaredNames(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						out = append(out, n.Name)
					}
				case *ast.TypeSpec:
					out = append(out, spec.Name.Name)
				}
			}
		}
	}
	return out
}

// defaultImportName guesses the name a package is imported under from its path: the last element,
// without a major-version element or suffix (/v2, .v3) or a go- prefix or -go suffix.
func defaultImportName(path string) string {
	elems := strings.Split(path, "/")
	last := elems[len(elems)-1]
	if len(elems) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = elems[len(elems)-2]
	}
	if i := strings.Index(last, ".v"); i > 0 {
		last = last[:i]
	}
	last = strings.TrimSuffix(strings.TrimPrefix(last, "go-"), "-go")
	return strings.ReplaceAll(last, "-", "")
}
