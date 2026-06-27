package codegen

// Accessors over the conf's typed package/feature ARRAYS (schema-conf.json models
// generate.packages and generate.features as discriminated lists, one entry per
// `type`). The generator and validator look targets up by type through these
// helpers instead of map fields, so the discriminator lives in one place.
//
// Package-type vocabulary: main (entrypoint) | cmd (the cli package: the editable
// handler stubs + the one generated file — framework + rollup + typed inputs) |
// runtime (the ENTIRE rotini runtime, merged into one self-contained file — see
// runtimePkg).

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
func (g *GenerateConfig) mainPkg() *PackageConfig { return g.packageOf("main") }

// frameworkPkg is the single cli-package target (`cmd`): the directory holding
// the editable handler stubs AND the one generated file — the framework glue the
// rotini.go template emits (the definition literal, the typed NewProgram wrapper,
// BindMeta, ProgramHandlers, the input structs, feature embeds) PLUS the rollup
// (handlers struct + Program + command→handler wiring), all in one package. The
// RUNTIME it imports is the only separate package (see runtimePkg).
func (g *GenerateConfig) frameworkPkg() *PackageConfig { return g.packageOf("cmd") }

// runtimePkg is the EMITTED-runtime target: the single 'file' the entire rotini
// runtime is merged into (its parent directory is the runtime package). The
// framework and handlers import this package, qualified `rotini.`.
func (g *GenerateConfig) runtimePkg() *PackageConfig { return g.packageOf("runtime") }

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
