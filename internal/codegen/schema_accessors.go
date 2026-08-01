package codegen

// Hand-written typed accessors over the generated schema structs (schema_conf.go /
// schema_spec.go): the conf's discriminated package/feature ARRAYS are looked up by
// `type` through these helpers (so the discriminator lives in one place), and a
// command's flattened input channels are bundled back into one Inputs view.

// Package-type discriminators — the `type` values of generate.packages entries.
const (
	typeMain    = "main"    // the binary entrypoint (main.go, create-once)
	typeCmd     = "cmd"     // the cmd package: editable stubs + the one generated file
	typeRuntime = "runtime" // the entire rotini runtime, merged into one file
)

// packageOf returns the package target with the given type, or nil when absent.
func (g *GenerateConfig) packageOf(typ string) *PackageConfig {
	if g == nil {
		return nil
	}
	for i := range g.Packages {
		if g.Packages[i].Type == typ {
			return &g.Packages[i]
		}
	}
	return nil
}

// mainPkg is the binary entrypoint target (nil when no main.go is written).
func (g *GenerateConfig) mainPkg() *PackageConfig { return g.packageOf(typeMain) }

// cmdPkg is the single cmd-package target: the directory holding the editable handler
// stubs AND the one generated file — the framework glue the rotini.go template emits
// (the definition literal, the typed NewProgram wrapper, BindMeta, ProgramHandlers, the
// input structs, feature embeds) PLUS the rollup (handlers struct + Program +
// command→handler wiring), all in one package. The RUNTIME it imports is the only
// separate package (see runtimePkg).
func (g *GenerateConfig) cmdPkg() *PackageConfig { return g.packageOf(typeCmd) }

// cmdTarget is cmdPkg for callers that run AFTER applyConfDefaults, where the cmd
// target is guaranteed present (the defaults ensure it). It panics if absent — a
// programming error (defaults not applied), made explicit rather than a latent
// nil-deref downstream. Pre-default readers (the lint rules, child-conf probes) keep
// using the nilable cmdPkg.
func (g *GenerateConfig) cmdTarget() *PackageConfig {
	if p := g.cmdPkg(); p != nil {
		return p
	}
	panic("codegen: cmd package target missing — applyConfDefaults must run before resolveLayout/emit")
}

// runtimePkg is the EMITTED-runtime target: the single 'file' the entire rotini
// runtime is merged into (its parent directory is the runtime package). The cmd
// package imports it, qualified `rotini.`.
func (g *GenerateConfig) runtimePkg() *PackageConfig { return g.packageOf(typeRuntime) }

// featureOf returns the derived-output feature with the given type, or nil.
func (g *GenerateConfig) featureOf(typ string) *Feature {
	if g == nil {
		return nil
	}
	for i := range g.Features {
		if g.Features[i].Type == typ {
			return &g.Features[i]
		}
	}
	return nil
}

// Inputs is the codegen/validation view of a command's typed input channels.
// The spec schema flattens these onto the command itself (no `inputs:` wrapper —
// W3 reshape), so the generated Command carries the channel fields directly; this
// type bundles them back into one value for the many helpers that reason about
// "a command's inputs" as a unit (flagFields, eachInputSchema, deriveUsage, …).
type Inputs struct {
	Flags            []FlagInput
	Arguments        []ArgumentInput
	ConfigFiles      []ConfigurationFile
	Config           []ConfigInput
	Env              []EnvInput
	Stdin            *StdinSpec
	FlagGroups       []FlagGroup
	FlagDependencies []FlagDependency
}

// inputs returns this command's channels as a single Inputs view, or nil when the
// command declares none — preserving the "no inputs block" sentinel the callers
// relied on when `inputs:` was its own object.
func (c *Command) inputs() *Inputs {
	if len(c.Flags) == 0 && len(c.Arguments) == 0 && len(c.ConfigFiles) == 0 &&
		len(c.Config) == 0 && len(c.Env) == 0 && c.Stdin == nil &&
		len(c.FlagGroups) == 0 && len(c.FlagDependencies) == 0 {
		return nil
	}
	return &Inputs{
		Flags:            c.Flags,
		Arguments:        c.Arguments,
		ConfigFiles:      c.ConfigFiles,
		Config:           c.Config,
		Env:              c.Env,
		Stdin:            c.Stdin,
		FlagGroups:       c.FlagGroups,
		FlagDependencies: c.FlagDependencies,
	}
}
