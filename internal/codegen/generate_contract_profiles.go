package codegen

import "strings"

// contractProfiles is how a configuration file's named profiles are chosen: the key holding
// them, the selector flag and variables, and the profile used when nothing selects one.
type contractProfiles struct {
	Under   string                  `json:"under"`
	Select  contractProfileSelector `json:"select"`
	Default string                  `json:"default,omitempty"`
}

// contractProfileSelector is the selector, resolved: the flag's name and the variables read,
// first preferred.
type contractProfileSelector struct {
	Flag string   `json:"flag,omitempty"`
	Env  []string `json:"env,omitempty"`
}

// contractProfilesOf describes cf's profiles as the command at path (names below the root)
// reads them, or nil when it has none.
func (p *program) contractProfilesOf(path []string, cf ConfigurationFile) *contractProfiles {
	if cf.Profiles == nil {
		return nil
	}
	scope := strings.Join(append([]string{p.rootName}, path...), "/")
	sel := findProfileSelector(p.scopeChain(scope), cf.Profiles.Select)
	out := &contractProfiles{Under: cf.Profiles.Under, Default: sel.defaultFor(cf.Profiles)}
	if sel.flag != nil {
		out.Select.Flag = sel.flag.Name
	}
	out.Select.Env = sel.variables(p.envPrefix)
	return out
}
