package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// This file resolves a configuration file's `profiles` block to the data the input reader
// reads: the key holding the profiles, the selector flag and variables, and the one default.

// profileSelector is the input(s) a `profiles.select` names: the nearest flag and the nearest
// env input with that name along a chain. Either may be nil.
type profileSelector struct {
	flag *FlagInput
	env  *EnvInput
}

// findProfileSelector looks up name among the flags and env inputs of chain (root first), the
// nearest command winning.
func findProfileSelector(chain []*Inputs, name string) profileSelector {
	var sel profileSelector
	for _, in := range slices.Backward(chain) {
		if in == nil {
			continue
		}
		for i := range in.Flags {
			if sel.flag == nil && in.Flags[i].Name == name {
				sel.flag = &in.Flags[i]
			}
		}
		for i := range in.Env {
			if sel.env == nil && in.Env[i].Name == name {
				sel.env = &in.Env[i]
			}
		}
	}
	return sel
}

// variables are the selector's environment variables, first preferred: the flag's own, then
// the env input's.
func (s profileSelector) variables(envPrefix string) []string {
	var out []string
	if s.flag != nil {
		if v := flagEnvVar(s.flag.Schema, flagReconKey(s.flag.Name, s.flag.Schema), envPrefix); v != "" {
			out = append(out, strings.Split(v, ",")...)
		}
	}
	if s.env != nil {
		for v := range strings.SplitSeq(envVarName(*s.env, envPrefix), ",") {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// defaultFor is the profile used when nothing selects one: the selector flag's default, else
// the env input's, else the file's own `default`.
func (s profileSelector) defaultFor(p *ConfigurationFileProfiles) string {
	if s.flag != nil && s.flag.Schema != nil {
		if d := defaultString(s.flag.Schema.Default); d != "" {
			return d
		}
	}
	if s.env != nil && s.env.Schema != nil {
		if d := defaultString(s.env.Schema.Default); d != "" {
			return d
		}
	}
	return p.Default
}

// scopeChain returns the inputs of the commands along scope (a config file's declaring command
// path, such as demo/deploy), root first.
func (gp *program) scopeChain(scope string) []*Inputs {
	chain := []*Inputs{gp.rootInputs}
	nodes := gp.tree
	segs := strings.Split(scope, "/")
	for _, seg := range segs[min(1, len(segs)):] {
		i := slices.IndexFunc(nodes, func(n rnode) bool { return n.name == seg })
		if i < 0 {
			break
		}
		chain = append(chain, nodes[i].inputs)
		nodes = nodes[i].children
	}
	return chain
}

// renderProfiles renders a config file's Profiles field, or nothing when it has no profiles.
func renderProfiles(b *strings.Builder, gp *program, f scopedConfigFile) {
	p := f.Profiles
	if p == nil {
		return
	}
	sel := findProfileSelector(gp.scopeChain(f.Scope), p.Select)
	fmt.Fprintf(b, ", Profiles: &%s.ProfilesDef{Under: %q", rotiniPkgName, p.Under)
	if sel.flag != nil {
		fmt.Fprintf(b, ", Flag: %q", sel.flag.Name)
	}
	if vars := sel.variables(gp.envPrefix); len(vars) > 0 {
		fmt.Fprintf(b, ", Env: %q", strings.Join(vars, ","))
	}
	if d := sel.defaultFor(p); d != "" {
		fmt.Fprintf(b, ", Default: %q", d)
	}
	b.WriteString("}")
}
