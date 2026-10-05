package codegen

import "encoding/json"

// Hand-written accessors over the generated schema structs (schema_conf.go, schema_spec.go).

// Package-type discriminators: the `type` values of generate.packages entries.
const (
	typeMain   = "main"   // the binary entrypoint (main.go, create-once)
	typeCmd    = "cmd"    // the cmd package: editable stubs plus the generated file
	typeModels = "models" // optional: the typed input/output structs in their own package
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

// cmdPkg is the cmd-package target: the directory holding the editable handler stubs and the
// generated file.
func (g *GenerateConfig) cmdPkg() *PackageConfig { return g.packageOf(typeCmd) }

// cmdTarget is cmdPkg for callers running after applyConfDefaults, which guarantees the
// target exists. It panics if absent, since that is a programming error.
func (g *GenerateConfig) cmdTarget() *PackageConfig {
	if p := g.cmdPkg(); p != nil {
		return p
	}
	panic("codegen: cmd package target missing — applyConfDefaults must run before resolveLayout/emit")
}

// modelsPkg is the optional typed-structs target. When declared, input and output types are
// written there and aliased from the cmd file, so a `handler:` package can use them without
// importing cmd (which imports the handler package).
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

// Inputs bundles the input channels the spec schema flattens onto a command, for helpers
// that treat a command's inputs as a unit.
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

// inputs returns the command's channels as an Inputs view, or nil when it declares none.
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

// variables returns an input's `variable:` (a string or an array) as an ordered list of
// names, first preferred.
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
// multipleOf) as a number, or nil when absent or not numeric. A string bound on a duration or
// bytesize input is converted by normalizeBounds; one left as text failed to convert and is
// reported by lintConstraintApplicability.
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
