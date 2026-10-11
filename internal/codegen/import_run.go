package codegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ImporterVersion is the github.com/go-rotini/import release `rotini import` runs unless
// --importer-version names another.
const ImporterVersion = "v0.2.0"

// importerModule is the importer's module; each framework's importer is a package in it
// (importerModule + "/cobra").
const importerModule = "github.com/go-rotini/import"

// importTestTemplate is the test file an import adds to the program's package, through
// go test -overlay; nothing is written in the program's tree. It compiles into the package,
// so the root expression may name unexported identifiers, and every name it declares or
// imports carries a rotiniImport prefix so it can't collide with the package's own. The
// verbs are the package name, the importer's package, the test name and the importer call.
const importTestTemplate = `package %[1]s

import (
	rotiniimport "github.com/go-rotini/import"
	rotiniimport%[2]s "github.com/go-rotini/import/%[2]s"
	rotiniimportos "os"
	rotiniimporttesting "testing"
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
	workspace  bool // the environment names a go.work, which the run turns off
}

// importRunner reads the program's tree; tests replace it with a fixed description.
var importRunner = runImporter

// runImporter runs the importer over opts.Package and returns its description, plus info
// notes about the run itself (the root it picked, a framework upgrade). The program's files
// and go.mod are never written: the importer is added to a copy of go.mod in a temporary
// directory, which is removed on every path.
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
	listed, err := r.goOutput("list", "-modfile="+r.modfile, "-mod=mod", "-f", "{{.Dir}}\n{{.Name}}\n{{.ImportPath}}", opts.Package)
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

// prepareModfile copies go.mod and go.sum into the work directory and adds the importer to
// the copy: the release ImporterVersion (or --importer-version) names, or a local checkout.
func (r *importRun) prepareModfile() error {
	gomod, err := os.ReadFile(filepath.Join(r.moduleRoot, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	gosum, err := os.ReadFile(filepath.Join(r.moduleRoot, "go.sum"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read go.sum: %w", err)
	}
	// import.mod gets the importer; orig.mod stays as the program has it, so its versions
	// can be compared without touching the program's own go.mod.
	copies := map[string][]byte{"import.mod": gomod, "import.sum": gosum, "orig.mod": gomod, "orig.sum": gosum}
	for name, data := range copies {
		if err := os.WriteFile(filepath.Join(r.work, name), data, 0o600); err != nil {
			return fmt.Errorf("copy go.mod and go.sum: %w", err)
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
		_, err = r.goOutput("mod", "edit", "-modfile="+r.modfile,
			"-require="+importerModule+"@v0.0.0", "-replace="+importerModule+"="+abs)
		return err
	}
	if _, err := r.goOutput("get", "-modfile="+r.modfile, importerModule+"@"+version); err != nil {
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
	before, err := r.goOutput("list", "-m", "-modfile="+filepath.Join(r.work, "orig.mod"), "-mod=mod", "-f", "{{.Version}}", r.fw.module)
	if err != nil {
		return importNote{}, false
	}
	after, err := r.goOutput("list", "-m", "-modfile="+r.modfile, "-mod=mod", "-f", "{{.Version}}", r.fw.module)
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
	src := fmt.Sprintf(importTestTemplate, pkgName, r.fw.pkg, testName, call)
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
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return nil, fmt.Errorf("prepare the import test: %w", err)
	}
	overlayFile := filepath.Join(r.work, "overlay.json")
	if err := os.WriteFile(overlayFile, overlay, 0o600); err != nil {
		return nil, fmt.Errorf("prepare the import test: %w", err)
	}

	timeout := r.opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	out := filepath.Join(r.work, "out.json")
	args := []string{"test", "-modfile", r.modfile, "-mod=mod", "-overlay", overlayFile, "-count=1",
		"-run", "^" + testName + "$", "-timeout", timeout.String()}
	if r.opts.Tags != "" {
		args = append(args, "-tags", r.opts.Tags)
	}
	args = append(args, r.opts.Package)
	stderr, err := r.goRun(args, "ROTINI_IMPORT_OUT="+out)
	if err != nil {
		if pkg := importerModule + "/" + r.fw.pkg; strings.Contains(stderr, "does not contain package "+pkg) ||
			strings.Contains(stderr, "no required module provides package "+pkg) {
			version := r.opts.ImporterVersion
			if version == "" {
				version = ImporterVersion
			}
			return nil, fmt.Errorf("the importer %s@%s has no %s package; pass an --importer-version that has it", importerModule, version, r.fw.pkg)
		}
		return nil, explainTestFailure(r.ctx, r.fw.pkg, err, stderr)
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, fmt.Errorf("the importer wrote no description: %w\n%s", err, lastLines(stderr, 20))
	}
	defer f.Close()
	return readImportResult(f)
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
