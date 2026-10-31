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

var _ rotini.HandlerE = (*rotiniInitializeHandler)(nil)

type rotiniInitializeHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniInitializeHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniInitializeInputs]()
	if err != nil {
		return inputError(err)
	}
	args := inputs.RotiniInitialize.Arguments
	flags := inputs.RotiniInitialize.Flags

	// The name becomes the generated Go package's name, which a keyword can't be, and main
	// can't be imported.
	if token.IsKeyword(args.Name) || args.Name == "main" {
		return rotini.UsageError(fmt.Errorf("invalid CLI name %q: it would name the generated Go package, and %s; choose another name", args.Name, reservedWhy(args.Name)))
	}

	version := rtx.Version()
	opt := codegen.InitOptions{Format: flags.Format, Template: flags.Template, Force: flags.Force}
	if flags.DryRun {
		rtx.SetDependencyIfAbsent(initializeDryRunDep, codegen.NewProcessor(version).InitializeDryRun)
		planned, err := rtx.MustDependency(initializeDryRunDep)(args.Name, opt)
		if err != nil {
			return err
		}
		printWarnings(rtx)(planned.Notices)
		if _, err := fmt.Fprint(rtx.Stdout, seedLines(planned)); err != nil {
			return writeError(err)
		}
		return reportPlanned(rtx, planned.Result, planned.Changes)
	}

	rtx.SetDependencyIfAbsent(initializeDep, codegen.NewProcessor(version).Initialize)
	initialize := rtx.MustDependency(initializeDep)

	written, err := initialize(args.Name, opt)
	if err != nil {
		return err
	}

	// The notices are generate's: validation warnings, and each handler disabled or restored.
	printWarnings(rtx)(written.Notices)
	if _, err := fmt.Fprintf(rtx.Stdout, "%s%s\n", seedLines(written), written.Result); err != nil {
		return writeError(err)
	}

	// init does not edit go.mod, so it warns when the module is not yet set up to build the
	// scaffold and run its go:generate line.
	if err := moduleSetup(".", "building ./cmd/"+args.Name); err != nil {
		rtx.RecordWarning(err)
	}
	return nil
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

// companionTool is the companion's package, which the seeded //go:generate line runs as
// `go tool rotini`. It is in runtimeModule, so `go get -tool` adds the runtime as well.
const companionTool = runtimeModule + "/cmd/rotini"

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

// modTool reports whether go.mod text declares pkg as a tool, on a `tool` line or in a tool
// block.
func modTool(gomod, pkg string) bool {
	inBlock := false
	for line := range strings.SplitSeq(gomod, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0:
			continue
		case inBlock && fields[0] == ")":
			inBlock = false
			continue
		case len(fields) == 2 && fields[0] == "tool" && fields[1] == "(":
			inBlock = true
			continue
		case fields[0] == "tool":
			fields = fields[1:]
		case !inBlock:
			continue
		}
		if len(fields) > 0 && fields[0] == pkg {
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

// hasCompanionTool reports whether the module file nearest dir declares the companion as a
// tool, so `go tool rotini` runs it.
func hasCompanionTool(dir string) bool {
	file := moduleFile(dir)
	if file == "" {
		return false
	}
	data, err := os.ReadFile(file)
	return err == nil && modTool(string(data), companionTool)
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

// moduleSetup is the warning for a module not yet set up for the scaffold, naming the commands
// that set it up before the step that needs them, or nil when it is. Building needs the runtime,
// and the seeded //go:generate line needs the companion as a tool: `go get -tool` adds both. A
// module kept in vendor.mod names that file to go get; with no module file, `go mod init` comes
// first. The runtime's own module needs neither.
func moduleSetup(dir, before string) error {
	file := moduleFile(dir)
	if file == "" {
		return fmt.Errorf("no go.mod found; run `go mod init <module>`, then `go get -tool %s`, before %s", companionTool, before)
	}
	if hasCompanionTool(dir) || ownModule(file) {
		return nil
	}
	name, modfile := filepath.Base(file), ""
	if name == "vendor.mod" {
		modfile = " -modfile=" + filepath.ToSlash(file)
		if abs, err := filepath.Abs(dir); err == nil {
			if rel, err := filepath.Rel(abs, file); err == nil {
				modfile = " -modfile=" + filepath.ToSlash(rel)
			}
		}
	}
	get := fmt.Sprintf("go get%s -tool %s", modfile, companionTool)
	if !requiresRuntime(dir) {
		return fmt.Errorf("%s does not require %s yet; run `%s` before %s (it adds the package and the tool the go:generate line runs)", name, runtimeModule, get, before)
	}
	return fmt.Errorf("%s has no tool line for %s, which the go:generate line runs as `go tool rotini`; run `%s` before go generate", name, companionTool, get)
}

// ownModule reports whether the module file declares the runtime's own module.
func ownModule(file string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" && fields[1] == runtimeModule {
			return true
		}
	}
	return false
}
