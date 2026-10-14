package rotini

import (
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/go-rotini/recon"
)

// fallbackLookup is what a flag's or argument's fallback is read from: a recon registry, or,
// for a read whose only source is the environment and whose keys are plain
// ([plainFallbackKeys]), the declared environment itself. The registry over that one source
// holds exactly each declared key whose variable is set, so none is built.
type fallbackLookup struct {
	reg *recon.Registry
	env *declaredEnv
}

// lookup is the value reconciled at key, with the name of the source that supplied it.
func (l fallbackLookup) lookup(key string) (val recon.Value, source string, found bool, err error) {
	if l.env != nil {
		v, ok := l.env.getKey(key)
		if !ok {
			return recon.Value{}, "", false, nil
		}
		return v, osEnvSourceName, true, nil
	}
	v, ok, err := l.reg.Get(key)
	return v, v.Source(), ok, err
}

// allKeys is every key holding a value, sorted, for reading a map's entries below its key. The
// environment alone holds no key below a declared key ([plainFallbackKeys]), so there it is
// nil.
func (l fallbackLookup) allKeys() []string {
	if l.env != nil {
		return nil
	}
	return l.reg.AllKeys()
}

// plainFallbackKeys reports whether the recon keys of v's flags and arguments are plain: each
// declared once, in its canonical spelling, without an index, and none below another. A read
// of such a key from the environment alone is then the variable's value and nothing else: no
// key's value can come from, or yield to, a map at a parent or the keys below it.
func plainFallbackKeys(v reflect.Value) bool { return typeFact(&plainKeyFacts, v, plainKeysOf) }

var plainKeyFacts sync.Map // reflect.Type → bool

func plainKeysOf(v reflect.Value) bool {
	if v.Kind() != reflect.Struct {
		return false
	}
	var keys []string
	for _, field := range v.Fields() {
		for _, s := range [2]reflect.Value{commandFlags(field), commandArgs(field)} {
			if !s.IsValid() {
				continue
			}
			for _, sf := range leafFields(s.Type()) {
				key := reconKey(sf.Tag.Get("recon"))
				if key == "" {
					continue
				}
				if strings.IndexByte(key, '[') >= 0 || recon.ParsePath(key).String() != key || slices.Contains(keys, key) {
					return false
				}
				keys = append(keys, key)
			}
		}
	}
	for _, a := range keys {
		for _, b := range keys {
			if strings.HasPrefix(b, a+recon.DefaultDelimiter) {
				return false
			}
		}
	}
	return true
}
