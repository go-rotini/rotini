package rotini

import (
	"context"
	"errors"
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
	parser := rtx.Parser()

	var inputs RotiniInitializeInputs
	if err := parser.Parse(rtx, &inputs); err != nil {
		rtx.HaltWith(err)
		return
	}

	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	if flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	if args.Name == "" {
		rtx.HaltWith(rotini.UsageError(errors.New("a name argument is required")))
		return
	}

	version := rtx.Version()
	rtx.BindIfAbsent("initialize", codegen.NewProcessor(version).Initialize)
	initialize := rtx.MustGet[codegen.InitializeFn]("initialize")

	if err := initialize(args.Name, flags.Format, flags.Force); err != nil {
		rtx.HaltWith(err)
		return
	}

	// The scaffold imports the rotini runtime but rotini does not touch the user's
	// go.mod — adding a require is a network operation with a side effect on a file
	// rotini does not own, so it is reported, not performed. Without this the next
	// `go build` fails on a missing module with no hint of what to do. It is reported only
	// when it is needed: telling a module that already requires rotini to `go get` it sends
	// the user to do something that changes nothing.
	fmt.Fprintf(rtx.Stdout, "initialized cmd/%s\n\nNext steps:\n", args.Name)
	if !requiresRuntime(".") {
		fmt.Fprintln(rtx.Stdout, "  go get github.com/go-rotini/rotini    # the runtime the generated code imports")
	}
	fmt.Fprintf(rtx.Stdout, "  go build ./cmd/%s\n", args.Name)
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
