package rotini

import (
	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// The codegen entry points the handlers call. Each handler registers the real one with
// SetDependencyIfAbsent, so a test that registered a double first keeps it.
var (
	generateDep   = rotini.NewDependency[codegen.GenerateFn]("rotini.generate")
	validateDep   = rotini.NewDependency[codegen.ValidateFn]("rotini.validate")
	initializeDep = rotini.NewDependency[codegen.InitializeFn]("rotini.initialize")

	generateDryRunDep   = rotini.NewDependency[codegen.GenerateDryRunFn]("rotini.generate.dry-run")
	initializeDryRunDep = rotini.NewDependency[codegen.InitializeDryRunFn]("rotini.initialize.dry-run")
)
