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
// compares: the command line's, never a default or fallback. A flag that is set is named as
// the user typed it.
func validateFlagDependencies(chain []Command, store *parsedInputs) error {
	for i := range chain {
		if !store.covers(i) {
			continue
		}
		f := ruleFrame(chain, i)
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
					forbidden = append(forbidden, typedRuleLabel(f, &store.scopes[i], name))
				}
			}
			if len(missing) > 0 {
				noun, verb := "flag", "is"
				if len(missing) > 1 {
					noun, verb = "flags", "are"
				}
				return scoped(constraintViolation("%s %s %s required %s", noun, joinAnd(missing), verb, trigger), f.Name, "")
			}
			if len(forbidden) > 0 {
				noun := "flag"
				if len(forbidden) > 1 {
					noun = "flags"
				}
				return scoped(constraintViolation("%s %s can't be used %s", noun, joinAnd(forbidden), trigger), f.Name, "")
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
		if len(dep.Equals) == 0 {
			parts = append(parts, "when "+typedRuleLabel(f, &store.scopes[i], dep.When)+" is set")
		} else {
			got, ok := store.scopes[i].dependencyValue(whenFD)
			if !ok {
				return "", false
			}
			got = dependencyCanonical(whenFD, got)
			if !slices.ContainsFunc(dep.Equals, func(v string) bool { return dependencyCanonical(whenFD, v) == got }) {
				return "", false
			}
			parts = append(parts, "when "+flagLabel(whenFD)+" is "+redactValue(got, whenFD.Secret))
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

// typedRuleLabel names a flag of frame f that the command line set, in a flag-group or
// dependency message: as the user typed it, else as [dependencyLabel] does.
func typedRuleLabel(f Command, si *scopeInputs, name string) string {
	if t := si.typed[name]; t != "" {
		return t
	}
	return dependencyLabel(f, name)
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
		if fd.Base == basePrefixed {
			v = prefixedText(v)
		}
		// Integers compare exactly; a float64 would merge those above 2^53.
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && isIntegerType(fd.Type) {
			return strconv.FormatInt(n, 10)
		}
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && isIntegerType(fd.Type) {
			return strconv.FormatUint(n, 10)
		}
		parse := parseNumber
		if fd.Base == basePrefixed {
			parse = prefixedNumber
		}
		if n, ok := parse(v); ok {
			return formatNum(n)
		}
	}
	return flagEnum(&fd).canonical([]string{v})[0]
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

// isIntegerType reports whether typ (or its element type) is a Go integer type or the JSON
// Schema "integer".
func isIntegerType(typ string) bool {
	switch constraintElemType(typ) {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "integer":
		return true
	}
	return false
}
