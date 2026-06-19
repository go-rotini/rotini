package internal

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
