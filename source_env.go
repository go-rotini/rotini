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
	val, set := s.view.lookup(name)
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
		var s reflect.Value
		if structName == "Flags" {
			s = commandFlags(ci)
		} else if ci.Kind() == reflect.Struct {
			s = ci.FieldByName(structName)
		}
		if !s.IsValid() || s.Kind() != reflect.Struct {
			continue
		}
		for f := range s.Type().Fields() {
			key := reconKey(f.Tag.Get("recon"))
			if key == "" || f.Tag.Get("envnest") != "" {
				continue
			}
			if _, dup := src.names[key]; dup {
				continue
			}
			path := recon.ParsePath(key)
			name := project(path)
			if tag := f.Tag.Get("env"); tag != "" {
				name = chosenEnv(view, tag)
			}
			src.names[key] = name
			src.keys = append(src.keys, path)
		}
	}
	return src
}

// hasChannel reports whether any command frame of the inputs struct v declares a non-empty
// structName sub-struct ("Env" or "Config"). A struct that describes none of a channel's
// inputs never builds that channel's registry.
func hasChannel(v reflect.Value, structName string) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		if s := ci.FieldByName(structName); s.IsValid() && s.Kind() == reflect.Struct && s.NumField() > 0 {
			return true
		}
	}
	return false
}
