package codegen

import "encoding/json"

// Hand-written typed accessors over the generated schema structs (schema_conf.go /
// schema_spec.go): the conf's discriminated package/feature ARRAYS are looked up by
// `type` through these helpers (so the discriminator lives in one place), and a
// command's flattened input channels are bundled back into one Inputs view.

// Package-type discriminators — the `type` values of generate.packages entries.
const (
	typeMain   = "main"   // the binary entrypoint (main.go, create-once)
	typeCmd    = "cmd"    // the cmd package: editable stubs + the one generated file
	typeModels = "models" // OPTIONAL: the typed input/output structs, in their own package
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

// cmdPkg is the single cmd-package target: the directory holding the editable handler stubs
// and the one generated file, framework and rollup merged into one package. The runtime it
// imports is an ordinary library dependency, not generated.
func (g *GenerateConfig) cmdPkg() *PackageConfig { return g.packageOf(typeCmd) }

// cmdTarget is cmdPkg for callers running after applyConfDefaults, where the target is
// guaranteed present. It panics if absent — defaults not applied is a programming error, made
// explicit rather than a latent nil-deref downstream. Pre-default readers use cmdPkg.
func (g *GenerateConfig) cmdTarget() *PackageConfig {
	if p := g.cmdPkg(); p != nil {
		return p
	}
	panic("codegen: cmd package target missing — applyConfDefaults must run before resolveLayout/emit")
}

// modelsPkg is the optional typed-structs target. When declared, the input and output types
// are written there instead of into the cmd file, which re-exports them as aliases. It exists
// to break the import cycle a command's `handler:` otherwise creates: the cmd package imports
// the handler package, so the handler package cannot import cmd back to reach its own input
// types — but both can import models.
func (g *GenerateConfig) modelsPkg() *PackageConfig { return g.packageOf(typeModels) }

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

// Inputs is the codegen and validation view of a command's typed input channels. The spec
// schema flattens these onto the command itself, so this bundles them back into one value for
// the helpers that reason about a command's inputs as a unit.
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

// variables is an input's `variable:` as a list. The key takes one name or several — the
// schema types it string-or-array, which decodes as a string or a []any — and every consumer
// wants the same thing: the names, in order, first preferred.
func variables(schema *InputSchema) []string {
	if schema == nil {
		return nil
	}
	switch v := schema.Variable.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// bound reads a numeric bound (minimum, maximum, exclusiveMinimum, exclusiveMaximum,
// multipleOf) as a number, or nil when it is absent or not a number. The keys take a number, or
// — on a duration or bytesize input — a string in that type's spelling (`1s`, `512Mi`), which
// normalizeBounds converts to the type's units right after decoding; a string still here is one
// that did not convert, and lintConstraintApplicability reports it.
func bound(v any) *float64 {
	var f float64
	switch n := v.(type) {
	case *float64:
		return n
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case int32:
		f = float64(n)
	case uint64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return nil
		}
		f = parsed
	default:
		return nil
	}
	return &f
}
