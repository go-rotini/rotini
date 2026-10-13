package rotini

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

var _ rotini.Handler = (*rotiniInitializeHandler)(nil)

type rotiniInitializeHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniInitializeHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniInitializeInputs]()
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	// The name becomes the generated Go package's name, which a keyword can't be, and main
	// can't be imported.
	if token.IsKeyword(args.Name) || args.Name == "main" {
		rtx.HaltWith(rotini.UsageError(fmt.Errorf("invalid CLI name %q: it would name the generated Go package, and %s; choose another name", args.Name, reservedWhy(args.Name))))
		return
	}

	version := rtx.Version()
	opt := codegen.InitOptions{Format: flags.Format, Template: flags.Template, Force: flags.Force}
	if flags.DryRun {
		rtx.SetDependencyIfAbsent(initializeDryRunDep, codegen.NewProcessor(version).InitializeDryRun)
		planned, err := rtx.MustGetDependency(initializeDryRunDep)(args.Name, opt)
		if err != nil {
			rtx.HaltWith(err)
			return
		}
		if _, err := fmt.Fprint(rtx.Stdout, seedLines(planned)); err != nil {
			haltWithWriteError(rtx, err)
			return
		}
		reportPlanned(rtx, planned.Result, planned.Changes)
		return
	}

	rtx.SetDependencyIfAbsent(initializeDep, codegen.NewProcessor(version).Initialize)
	initialize := rtx.MustGetDependency(initializeDep)

	written, err := initialize(args.Name, opt)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintf(rtx.Stdout, "%s%s\n", seedLines(written), written.Result); err != nil {
		haltWithWriteError(rtx, err)
		return
	}

	// init does not edit go.mod, so it warns when the module does not yet require the runtime
	// the scaffold imports.
	if !requiresRuntime(".") {
		rtx.RecordWarning(missingRuntime(".", "building ./cmd/"+args.Name))
	}
}

func reservedWhy(name string) string {
	if name == "main" {
		return "a package named main can't be imported"
	}
	return name + " is a Go keyword"
}

// seedLines lists the spec and conf of every CLI init wrote, the named one first.
func seedLines(in codegen.Initialized) string {
	var out strings.Builder
	for _, s := range append([]codegen.SeedFiles{{Spec: in.Spec, Conf: in.Conf}}, in.Also...) {
		fmt.Fprintf(&out, "spec: %s\nconf: %s\n", filepath.ToSlash(s.Spec), filepath.ToSlash(s.Conf))
	}
	return out.String()
}

// runtimeModule is the module the generated code imports.
const runtimeModule = "github.com/go-rotini/rotini"

// modRequires reports whether go.mod text requires module or declares it as its own module. It
// scans lines rather than using golang.org/x/mod, to avoid the dependency: a matching line
// starts with the module path, inside a require block or after `require` or `module`.
func modRequires(gomod, module string) bool {
	for line := range strings.SplitSeq(gomod, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && (fields[0] == "require" || fields[0] == "module") {
			fields = fields[1:]
		}
		if len(fields) > 0 && fields[0] == module {
			return true
		}
	}
	return false
}

// requiresRuntime reports whether the module file nearest dir (see moduleFile) requires the
// rotini runtime or is the runtime's own. With no readable module file it reports false, so
// the warning is shown.
func requiresRuntime(dir string) bool {
	file := moduleFile(dir)
	if file == "" {
		return false
	}
	data, err := os.ReadFile(file)
	return err == nil && modRequires(string(data), runtimeModule)
}

// moduleFile returns the module file of the module dir is in: the nearest directory at or above
// it holding go.mod, or vendor.mod when it holds no go.mod. It returns "" when there is none.
func moduleFile(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		for _, name := range []string{"go.mod", "vendor.mod"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				return filepath.Join(dir, name)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// missingRuntime is the warning for a module that doesn't require the runtime yet, naming the
// go get that adds it before the step that needs it. A module kept in vendor.mod names that
// file to go get.
func missingRuntime(dir, before string) error {
	name, modfile := "go.mod", ""
	if file := moduleFile(dir); filepath.Base(file) == "vendor.mod" {
		name, modfile = "vendor.mod", " -modfile="+filepath.ToSlash(file)
		if abs, err := filepath.Abs(dir); err == nil {
			if rel, err := filepath.Rel(abs, file); err == nil {
				modfile = " -modfile=" + filepath.ToSlash(rel)
			}
		}
	}
	return fmt.Errorf("%s does not require %s yet; run `go get%s %s` before %s", name, runtimeModule, modfile, runtimeModule, before)
}
