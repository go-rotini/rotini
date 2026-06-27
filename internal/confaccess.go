package internal

// Accessors over the conf's typed package/feature ARRAYS (schema-conf.json models
// generate.packages and generate.features as discriminated lists, one entry per
// `type`). The generator and validator look targets up by type through these
// helpers instead of map fields, so the discriminator lives in one place.
//
// Package-type vocabulary (ROTINI_REFACTOR.md): main (entrypoint) | handlers (the
// editable stubs + rollup) | models (typed inputs/outputs) | runtime_required (the
// always-present dispatch runtime) | runtime_optional (the include-gated services).
// Until the runtime-emission work (Phase 4) splits them into distinct files, the
// framework-bound types resolve to ONE merged framework file, with runtime_required
// as the canonical locator — see frameworkPkg.

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

// handlersPkg is the handler-logic target: the per-command stubs and the rollup.
func (g *GenerateConfig) handlersPkg() *PackageConfig { return g.packageOf("handlers") }

// frameworkPkg is the canonical generated-framework target (typed inputs, the
// definition, NewProgram, feature embeds). The framework is nominally split across
// models/runtime_required/runtime_optional; runtime_required is the locator the
// generator uses while they share one merged file (pre-emission).
func (g *GenerateConfig) frameworkPkg() *PackageConfig { return g.packageOf("runtime_required") }

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
