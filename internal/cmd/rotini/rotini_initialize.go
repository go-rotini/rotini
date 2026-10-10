package rotini

import (
	"context"
	"fmt"
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
		rtx.RecordWarning(fmt.Errorf("go.mod does not require %s yet; run `go get %s` before building ./cmd/%s", runtimeModule, runtimeModule, args.Name))
	}
}

// seedLines lists the spec and conf of every CLI init wrote, the named one first.
func seedLines(in codegen.Initialized) string {
	var out strings.Builder
	for _, s := range append([]codegen.SeedFiles{{Spec: in.Spec, Conf: in.Conf}}, in.Also...) {
		fmt.Fprintf(&out, "spec: %s\nconf: %s\n", s.Spec, s.Conf)
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

// requiresRuntime reports whether the nearest go.mod at or above dir requires the rotini
// runtime or is the runtime's own. With no readable go.mod it reports false, so the warning
// is shown.
func requiresRuntime(dir string) bool {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			return modRequires(string(data), runtimeModule)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
