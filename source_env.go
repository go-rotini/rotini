package rotini

import (
	"reflect"

	"github.com/go-rotini/recon"
)

// declaredEnv is the environment source the env channel and flag env fallbacks read: one
// variable per declared recon key, read through the run's view. It never enumerates the
// environment, so its cost follows the inputs a command declares, not the size of the
// environment; Keys lists only the declared keys whose variable is set.
type declaredEnv struct {
	view  *osView
	keys  []recon.Path
	names map[string]string // recon key → variable name
	// emptyAbsent reports a variable that is set but empty as absent, so a flag's fallback
	// falls through to its configuration file and default (see firstSetEnv).
	emptyAbsent bool
}

// osEnvSourceName is what the environment source calls itself; fallbackOrigin recognizes it.
const osEnvSourceName = "osenv"

func (s declaredEnv) Name() string { return osEnvSourceName }

func (s declaredEnv) Get(path recon.Path) (recon.Value, bool, error) {
	name, ok := s.names[path.String()]
	if !ok {
		return recon.Value{}, false, nil
	}
	val, set := s.view.inputLookup(name)
	if !set || (s.emptyAbsent && val == "") {
		return recon.Value{}, false, nil
	}
	return recon.NewValue(val), true, nil
}

func (s declaredEnv) Keys() []recon.Path {
	var out []recon.Path
	for _, k := range s.keys {
		if _, ok, err := s.Get(k); ok && err == nil {
			out = append(out, k)
		}
	}
	return out
}

func (declaredEnv) Close() error { return nil }

// newDeclaredEnv builds the source over the recon-keyed fields of each command's structName
// sub-struct ("Env", or "Flags" for fallbacks). A field's variable is its env tag's chosen name
// when it has one (exempt from envPrefix), else the SNAKE_UPPER projection of its key under
// envPrefix. Nested env families (envnest) are not leaf keys and are left out.
func newDeclaredEnv(v reflect.Value, structName, envPrefix string, view *osView) declaredEnv {
	src := declaredEnv{view: view, names: map[string]string{}}
	if v.Kind() != reflect.Struct {
		return src
	}
	prefix := ""
	if envPrefix != "" {
		prefix = envPrefix + "_"
	}
	project := recon.SnakeUpperPrefixTransform(prefix)
	for _, ci := range v.Fields() {
		var structs [2]reflect.Value
		if structName == "Flags" {
			structs = [2]reflect.Value{commandFlags(ci), commandArgs(ci)} // an argument's fallback reads the same way
		} else if ci.Kind() == reflect.Struct {
			structs[0] = ci.FieldByName(structName)
		}
		for _, s := range structs {
			if s.IsValid() && s.Kind() == reflect.Struct {
				src.addFields(s.Type(), project, view)
			}
		}
	}
	return src
}

// addFields adds the recon-keyed fields of one sub-struct type; see [newDeclaredEnv].
func (s *declaredEnv) addFields(t reflect.Type, project recon.KeyTransform, view *osView) {
	for f := range typeLeaves(t) {
		key := reconKey(f.Tag.Get("recon"))
		if key == "" || f.Tag.Get("envnest") != "" {
			continue
		}
		if _, dup := s.names[key]; dup {
			continue
		}
		path := recon.ParsePath(key)
		name := project(path)
		if tag := f.Tag.Get("env"); tag != "" {
			name = chosenEnv(view, tag)
		}
		s.names[key] = name
		s.keys = append(s.keys, path)
	}
}

// hasEnvChannel reports whether any command frame of the inputs struct v declares a non-empty
// Env sub-struct. A struct that describes no env input never builds the env registry.
func hasEnvChannel(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if s := ci.FieldByName("Env"); s.IsValid() && s.Kind() == reflect.Struct && s.NumField() > 0 {
			return true
		}
	}
	return false
}
