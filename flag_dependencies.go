package rotini

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// validateFlagDependencies enforces each command's conditional cross-flag rules. A rule
// triggers when its When flag is set on argv (with one of its Equals values, if any) and none of
// its Unless flags is; then every flag it Requires must be set and none it Forbids may be. Set
// follows the same explicit-argv convention as flag groups, and so does the value Equals
// compares: the command line's, never a default or fallback.
func validateFlagDependencies(chain []Command, store *parsedInputs) error {
	for i, f := range chain {
		if !store.covers(i) {
			continue
		}
		for _, dep := range f.FlagDependencies {
			trigger, ok := dependencyTrigger(f, i, store, dep)
			if !ok {
				continue
			}
			var missing, forbidden []string
			for _, name := range dep.Requires {
				if !store.setOnArgv(i, name) {
					missing = append(missing, dependencyLabel(f, name))
				}
			}
			for _, name := range dep.Forbids {
				if store.setOnArgv(i, name) {
					forbidden = append(forbidden, dependencyLabel(f, name))
				}
			}
			if len(missing) > 0 {
				noun, verb := "flag", "is"
				if len(missing) > 1 {
					noun, verb = "flags", "are"
				}
				return constraintViolation("%s %s %s required %s", noun, joinAnd(missing), verb, trigger)
			}
			if len(forbidden) > 0 {
				noun := "flag"
				if len(forbidden) > 1 {
					noun = "flags"
				}
				return constraintViolation("%s %s can't be used %s", noun, joinAnd(forbidden), trigger)
			}
		}
	}
	return nil
}

// dependencyTrigger reports whether dep applies in chain frame i, and if so phrases why for a
// message: "when --format is csv", "unless --config is set", or both.
func dependencyTrigger(f Command, i int, store *parsedInputs, dep FlagDependency) (string, bool) {
	var parts []string
	if dep.When != "" {
		whenFD, ok := findFlagDef(f.Flags, dep.When)
		if !ok || !store.setOnArgv(i, dep.When) {
			return "", false // an unknown trigger flag is a spec lint error
		}
		label := flagLabel(whenFD)
		if len(dep.Equals) == 0 {
			parts = append(parts, "when "+label+" is set")
		} else {
			got, ok := store.scopes[i].dependencyValue(whenFD)
			if !ok {
				return "", false
			}
			got = dependencyCanonical(whenFD, got)
			if !slices.ContainsFunc(dep.Equals, func(v string) bool { return dependencyCanonical(whenFD, v) == got }) {
				return "", false
			}
			parts = append(parts, "when "+label+" is "+redactValue(got, whenFD.Secret))
		}
	}
	if len(dep.Unless) > 0 {
		labels := make([]string, len(dep.Unless))
		for k, name := range dep.Unless {
			if store.setOnArgv(i, name) {
				return "", false
			}
			labels[k] = dependencyLabel(f, name)
		}
		parts = append(parts, "unless "+strings.Join(labels, " or ")+" is set")
	}
	return strings.Join(parts, ", "), len(parts) > 0
}

// dependencyLabel names a flag of frame f in a dependency message.
func dependencyLabel(f Command, name string) string {
	if fd, ok := findFlagDef(f.Flags, name); ok {
		return flagLabel(fd)
	}
	return "--" + name
}

// dependencyValue is the value a dependency's Equals compares for fd: the typed value a check
// of typed inputs recorded, else the last value the command line gave.
func (si scopeInputs) dependencyValue(fd FlagDef) (string, bool) {
	if v, ok := si.depValues[fd.Name]; ok {
		return v, true
	}
	vals := si.flags[fd.Name]
	if len(vals) == 0 {
		return "", false
	}
	return vals[len(vals)-1], true
}

// dependencyCanonical spells v the way fd stores it, so values compare as the flag reads them:
// an enum's main value, a bool's true or false, a number's shortest form.
func dependencyCanonical(fd FlagDef, v string) string {
	switch {
	case fd.Type == "bool":
		if b, err := parseBool(v); err == nil {
			return strconv.FormatBool(b)
		}
	case isNumericType(fd.Type):
		if n, ok := parseNumber(v); ok {
			return formatNum(n)
		}
	}
	return flagEnum(fd).canonical([]string{v})[0]
}

// noteDependencyValue records a typed flag's value for the dependencies that compare it, in a
// store built from typed inputs rather than the command line.
func (si *scopeInputs) noteDependencyValue(name string, f reflect.Value) {
	if f = indirectValue(f); !f.IsValid() {
		return
	}
	if si.depValues == nil {
		si.depValues = map[string]string{}
	}
	si.depValues[name] = typedText(f)
}

// dependencyOffers reports whether completion treats dep as triggered, given the flags set so
// far: its When flag is set (or it has none) and none of its Unless flags is. A rule narrowed by
// Equals is left out, since completion doesn't read values.
func dependencyOffers(dep FlagDependency, set map[string]bool) bool {
	if len(dep.Equals) > 0 || (dep.When != "" && !set[dep.When]) {
		return false
	}
	return !slices.ContainsFunc(dep.Unless, func(n string) bool { return set[n] })
}
