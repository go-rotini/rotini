package rotini

import (
	"reflect"
	"slices"
	"sync"

	"github.com/go-rotini/recon"
)

// declaredEnv is the environment source the env channel and flag env fallbacks read: one
// variable per declared recon key, read through the run's view. It never enumerates the
// environment, so its cost follows the inputs a command declares, not the size of the
// environment; Keys lists only the declared keys whose variable is set.
type declaredEnv struct {
	view *osView
	tab  *envTable
	// emptyAbsent reports a variable that is set but empty as absent, so a flag's fallback
	// falls through to its configuration file and default (see firstSetEnv).
	emptyAbsent bool
}

// envTable is what a declaredEnv reads, fixed by the inputs type, the sub-struct and the env
// prefix: each declared key, in declaration order, with the variable it projects to and the
// env tag that names its variable instead.
type envTable struct {
	keys    []recon.Path
	entries []envEntry
}

// envEntry is one declared key of an envTable.
type envEntry struct {
	key, projected, tag string
}

// osEnvSourceName is what the environment source calls itself; fallbackOrigin recognizes it.
const osEnvSourceName = "osenv"

func (s declaredEnv) Name() string { return osEnvSourceName }

func (s declaredEnv) Get(path recon.Path) (recon.Value, bool, error) {
	v, ok := s.getKey(path.String())
	return v, ok, nil
}

// getKey is Get for the key's canonical string.
func (s declaredEnv) getKey(key string) (recon.Value, bool) {
	val, ok := s.text(key)
	if !ok {
		return recon.Value{}, false
	}
	return recon.NewValue(val), true
}

// text is the value of key's variable, and whether it counts as set. A key's variable is its
// env tag's chosen name when it has one, else its projection.
func (s declaredEnv) text(key string) (string, bool) {
	if s.tab == nil {
		return "", false
	}
	i := slices.IndexFunc(s.tab.entries, func(e envEntry) bool { return e.key == key })
	if i < 0 {
		return "", false
	}
	e := &s.tab.entries[i]
	name := e.projected
	if e.tag != "" {
		name = chosenEnv(s.view, e.tag)
	}
	val, set := s.view.inputLookup(name)
	if !set || (s.emptyAbsent && val == "") {
		return "", false
	}
	return val, true
}

func (s declaredEnv) Keys() []recon.Path {
	var out []recon.Path
	if s.tab == nil {
		return out
	}
	for _, k := range s.tab.keys {
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
	src := declaredEnv{view: view}
	if v.Kind() != reflect.Struct {
		return src
	}
	src.tab = envTableFor(v, structName, envPrefix)
	return src
}

// envTables caches envTableFor by inputs type, sub-struct and prefix.
var envTables sync.Map // envTableKey → *envTable

type envTableKey struct {
	t                  reflect.Type
	structName, prefix string
}

// envTableFor is the envTable of the struct v; see [newDeclaredEnv]. The first field declaring
// a key wins.
func envTableFor(v reflect.Value, structName, envPrefix string) *envTable {
	k := envTableKey{t: v.Type(), structName: structName, prefix: envPrefix}
	if got, ok := envTables.Load(k); ok {
		if tab, ok := got.(*envTable); ok {
			return tab
		}
	}
	prefix := ""
	if envPrefix != "" {
		prefix = envPrefix + "_"
	}
	project := recon.SnakeUpperPrefixTransform(prefix)
	tab := &envTable{}
	for _, ci := range v.Fields() {
		var structs [2]reflect.Value
		if structName == "Flags" {
			structs = [2]reflect.Value{commandFlags(ci), commandArgs(ci)} // an argument's fallback reads the same way
		} else if ci.Kind() == reflect.Struct {
			structs[0] = ci.FieldByName(structName)
		}
		for _, s := range structs {
			if s.IsValid() && s.Kind() == reflect.Struct {
				tab.addFields(s.Type(), project)
			}
		}
	}
	envTables.Store(k, tab)
	return tab
}

// addFields adds the recon-keyed fields of one sub-struct type; see [envTableFor].
func (tab *envTable) addFields(t reflect.Type, project recon.KeyTransform) {
	for _, f := range leafFields(t) {
		key := reconKey(f.Tag.Get("recon"))
		if key == "" || f.Tag.Get("envnest") != "" {
			continue
		}
		if slices.ContainsFunc(tab.entries, func(e envEntry) bool { return e.key == key }) {
			continue
		}
		path := recon.ParsePath(key)
		tab.entries = append(tab.entries, envEntry{key: key, projected: project(path), tag: f.Tag.Get("env")})
		tab.keys = append(tab.keys, path)
	}
}

// hasEnvChannel reports whether any command frame of the inputs struct v declares a non-empty
// Env sub-struct. A struct that describes no env input never builds the env registry.
func hasEnvChannel(v reflect.Value) bool { return typeFact(&envChannelFacts, v, envChannelOf) }

var envChannelFacts sync.Map // reflect.Type → bool

func envChannelOf(v reflect.Value) bool {
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
