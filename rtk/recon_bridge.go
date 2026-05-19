package rtk

import (
	"fmt"
	"strings"

	"github.com/go-rotini/recon"
)

// resolveFlagsViaRecon fills in env / config / default values for any flag
// in flags that is not already populated in scope (i.e., not supplied via
// argv).
//
// It builds a per-call recon.Registry with three named sources — "env",
// "config", "defaults" — in that precedence order. For each unresolved
// flag, it queries the registry by the flag's canonical Name; if a value
// comes back it is stringified, coerced to the flag's declared Type, and
// appended into scope.
//
// argv-resolved values are never consulted through recon. They live in
// scope already (set by parseScopedFlags) and skip the recon path entirely.
// This preserves the "argv beats env beats config beats default" precedence
// of the parser without forcing recon to model the structural difference
// between argv tokenization and value-source resolution.
//
// The registry is constructed and closed per call. A CLI parses argv at
// most once per invocation; the allocation cost is in the noise compared to
// the rest of Parse.
func resolveFlagsViaRecon(flags []FlagSpec, scope map[string]any, in *Inputs, coerce CoerceValueFn) error {
	if in == nil {
		return nil
	}

	// Build the three per-source maps. Each maps flag.Name → raw value.
	envMap := make(map[string]any)
	if in.Env != nil {
		for i := range flags {
			f := &flags[i]
			if f.EnvKey == "" {
				continue
			}
			if v, ok := in.Env.Lookup(f.EnvKey); ok && v != "" {
				envMap[f.Name] = v
			}
		}
	}

	configMap := make(map[string]any)
	if in.Config != nil {
		for i := range flags {
			f := &flags[i]
			if f.ConfigKey == "" {
				continue
			}
			path := strings.Split(f.ConfigKey, ".")
			if v, ok := in.Config.Lookup(path); ok {
				configMap[f.Name] = v
			}
		}
	}

	defaultsMap := make(map[string]any)
	for i := range flags {
		f := &flags[i]
		if f.Default != "" {
			defaultsMap[f.Name] = f.Default
		}
	}

	// Skip the recon dance entirely when nothing is sourceable.
	if len(envMap) == 0 && len(configMap) == 0 && len(defaultsMap) == 0 {
		return nil
	}

	reg, err := recon.New(
		recon.WithSource(recon.NewMapSource("env", envMap)),
		recon.WithSource(recon.NewMapSource("config", configMap)),
		recon.WithSource(recon.NewMapSource("defaults", defaultsMap)),
		recon.WithPrecedence("env", "config", "defaults"),
	)
	if err != nil {
		return fmt.Errorf("rtk: build recon registry: %w", err)
	}
	defer func() { _ = reg.Close() }()

	for i := range flags {
		f := &flags[i]
		if _, alreadyResolved := scope[f.Name]; alreadyResolved {
			continue
		}
		v, ok, lookupErr := reg.GetAny(f.Name)
		if lookupErr != nil || !ok || v == nil {
			continue
		}
		strVal := stringifyForCoerce(v)
		coerced, _, err := coerce(f.Type, strVal)
		if err != nil {
			// Coercion failures from non-argv sources are non-fatal — they
			// match the rotiniold behavior for env/config/default values.
			continue
		}
		appendFlagValue(scope, f.Name, coerced)
	}

	return nil
}

// stringifyForCoerce reduces an arbitrary recon Value to a string the
// CoerceValueFn can re-parse. The parser's coerce layer takes strings (its
// job is precisely string → Go-type), so we round-trip even when recon
// already produced a typed value (e.g., a config-file int).
//
// This isn't lossy for the supported flag types: a recon-produced int is
// re-rendered via fmt.Sprintf("%v", ...) and then coerced back via
// strconv.Atoi.
func stringifyForCoerce(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case fmt.Stringer:
		return tv.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
