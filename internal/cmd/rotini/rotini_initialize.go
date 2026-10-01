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

var _ rotini.Handlers = (*rotiniInitializeHandlers)(nil)

type rotiniInitializeHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func (*rotiniInitializeHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if answerHelp(rtx, func(in RotiniInitializeInputs) bool { return in.RotiniInitialize.Flags.Help }) {
		return
	}

	inputs, err := rotini.Collect[RotiniInitializeInputs](rtx)
	if err != nil {
		haltWithInputError(rtx, err)
		return
	}
	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	version := rtx.Version()
	rtx.BindIfAbsent("initialize", codegen.NewProcessor(version).Initialize)
	initialize := rtx.MustGet[codegen.InitializeFn]("initialize")

	written, err := initialize(args.Name, flags.Format, flags.Force)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	// The same report generate and validate give: the files it wrote, then the timing line.
	fmt.Fprintf(rtx.Stdout, "spec: %s\nconf: %s\n%s\n", written.Spec, written.Conf, written.Result)

	// The scaffold imports the rotini runtime, and rotini does not add it to the user's go.mod —
	// that is a network operation with a side effect on a file rotini does not own. So when
	// the module does not require it yet, the next `go build` would fail on a missing module,
	// and the warning says what to run instead.
	if !requiresRuntime(".") {
		rtx.RecordWarning(fmt.Errorf("go.mod does not require %s yet; run `go get %s` before building ./cmd/%s", runtimeModule, runtimeModule, args.Name))
	}
}

// runtimeModule is the module the generated code imports.
const runtimeModule = "github.com/go-rotini/rotini"

// modRequires reports whether go.mod text requires module or declares it as its own module. It
// reads the file line by line rather than through golang.org/x/mod: every project that runs
// rotini as a tool would otherwise take that module on for this one question. A requirement's
// line starts with the module path — inside a require block, or after `require` on one line.
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

// requiresRuntime reports whether the go.mod governing dir — the nearest one at or above it —
// already requires the rotini runtime, or IS the runtime. With no readable go.mod it reports
// false, so the `go get` step is shown rather than silently dropped.
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
