package rotini

import (
	"fmt"
	"os/user"
	"reflect"
	"slices"
)

// stdinFormats is every format a Stdin field may read, in canonical order.
var stdinFormats = []string{"text", "lines", "bytes", "json", "jsonl", "yaml", "jsonc", "toml"}

// Stdin wires reading the leaf command's Stdin field in the listed formats, among text, lines,
// bytes, json, jsonl, yaml, jsonc and toml; with none listed, every one. A document format also
// needs its codec ([JSON], [YAML], [JSONC], [TOML]; jsonl needs JSON).
func Stdin(formats ...string) Feature {
	if len(formats) == 0 {
		formats = stdinFormats
	}
	formats = slices.Clone(formats)
	return Feature{name: stdinFeatureName(formats), apply: func(s *featureSet) {
		if s.stdin == nil {
			s.stdin = &stdinHooks{fill: (*InputReader).bindStdin}
		}
		for _, f := range formats {
			if !slices.Contains(s.stdinFormats, f) {
				s.stdinFormats = append(s.stdinFormats, f)
			}
		}
	}}
}

// Env wires env inputs: the Env sub-structs of an inputs struct.
func Env() Feature {
	return Feature{name: "env", apply: func(s *featureSet) {
		s.env = &envHooks{registry: envRegistry}
		s.wireChannels()
	}}
}

// ConfigFiles wires configuration files: config inputs (the Config sub-structs), per-input
// file pins, discovery, custom [InputSettings.Sources], [ConfigFilePath] and writing values
// back. Each file's format also needs its codec.
func ConfigFiles() Feature {
	return Feature{name: "config-files", apply: func(s *featureSet) {
		s.config = &configHooks{
			regs:        (*InputReader).configRegs,
			fileSources: (*InputReader).fileSources,
			locate:      locateConfigFile,
		}
		s.wireChannels()
	}}
}

// wireChannels wires filling and checking the Env and Config sub-structs, which env inputs and
// config inputs share.
func (s *featureSet) wireChannels() {
	if s.channels == nil {
		s.channels = &channelHooks{fill: fillChannels, validate: validateChannels}
	}
}

// Profiles wires configuration file profiles ([ConfigFile.Profiles]): selecting one named
// section of a file per run.
func Profiles() Feature {
	return Feature{name: "config-profiles", apply: func(s *featureSet) {
		s.profiles = &profileHooks{
			choices: (*InputReader).profileChoices,
			check:   (*InputReader).checkProfiles,
			names:   profileNames,
			sources: profiledSources,
		}
	}}
}

// EnvFiles wires .env files (a configuration file read as environment variables) and
// variable_file inputs.
func EnvFiles() Feature {
	return Feature{name: "env-files", apply: func(s *featureSet) {
		s.envFiles = &envFileHooks{view: (*InputReader).inputView}
	}}
}

// Fallbacks wires flag and argument fallbacks to the environment and configuration files.
func Fallbacks() Feature {
	return Feature{name: "fallbacks", apply: func(s *featureSet) {
		s.fallbacks = &fallbackHooks{reconcile: (*InputReader).reconcileFlags, flagEnv: flagEnvSource}
	}}
}

// PathExpansion wires declared path expansion ("~", "~user", $VAR, relative to a
// configuration file) and "~user" in configuration file and plugin paths.
func PathExpansion() Feature {
	return Feature{name: "path-expansion", apply: func(s *featureSet) {
		s.expand = &expandHooks{
			store:    expandStore,
			channels: expandChannels,
			fallback: expandFallback,
			lookupUser: func(name string) (string, error) {
				u, err := user.Lookup(name)
				if err != nil {
					return "", err //nolint:wrapcheck // reported by the caller
				}
				return u.HomeDir, nil
			},
		}
	}}
}

// ObjectFlags wires flags whose value is an object ([FlagDef.ObjectSchema]). It also needs
// [JSON], [YAML] and [Schemas].
func ObjectFlags() Feature {
	return Feature{name: "object-flags", apply: func(s *featureSet) {
		s.objects = &objectHooks{bind: bindObjectFlag}
	}}
}

// Schemas wires JSON Schema checks of stdin documents and records, configuration files and
// object flags.
func Schemas() Feature {
	return Feature{name: "schemas", apply: func(s *featureSet) {
		s.schemas = &schemaHooks{
			validator: func(schema string) (mapValidator, error) {
				v, err := schemaValidator(schema)
				if err != nil {
					return nil, err
				}
				return v, nil
			},
			document:        validateDocumentJSON,
			record:          checkJSONLRecord,
			patternMessages: applyPatternMessages,
		}
	}}
}

// missingIn is [missingFeature] for a run of the command chain.
func missingIn(chain []Command, feature, ctor, subject string) *WiringError {
	we := missingFeature(feature, ctor, subject)
	we.Command = pathOf(chain)
	return we
}

// fallbackSubject names the first flag or argument of v with a fallback, for a wiring error.
func fallbackSubject(v reflect.Value) string {
	if name, ok := firstTagged(v, []string{"Flags", "Arguments"}, func(t reflect.StructTag) bool { return reconKey(t.Get("recon")) != "" }); ok {
		return fmt.Sprintf("input %s falls back to the environment or a configuration file", name)
	}
	return "an input falls back to the environment or a configuration file"
}

// expandSubject names the first input of v that declares path expansion, for a wiring error.
func expandSubject(v reflect.Value) string {
	if name, ok := firstTagged(v, []string{"Flags", "Arguments", "Env", "Config"}, func(t reflect.StructTag) bool { return tagExpandRule(t).declared() }); ok {
		return fmt.Sprintf("input %s declares expand or relative_to", name)
	}
	return "an input declares expand or relative_to"
}

// firstTagged is the rotini name of the first leaf, in the named sub-structs of v's command
// frames, whose tag satisfies match.
func firstTagged(v reflect.Value, structs []string, match func(reflect.StructTag) bool) (string, bool) {
	if v.Kind() != reflect.Struct {
		return "", false
	}
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		for _, name := range structs {
			s := ci.FieldByName(name)
			if !s.IsValid() || s.Kind() != reflect.Struct {
				continue
			}
			for f := range typeLeaves(s.Type()) {
				if match(f.Tag) {
					return f.Tag.Get("rotini"), true
				}
			}
		}
	}
	return "", false
}
