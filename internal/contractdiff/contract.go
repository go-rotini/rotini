package contractdiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// format is the only contract format this package compares.
const format = "rotini-contract/1"

// document is the part of a contract document the diff reads. Fields it doesn't know are
// ignored, since a newer writer of the same format may add more. Schemas stay untyped.
type document struct {
	Format        string              `json:"format"`
	Name          string              `json:"name"`
	Commands      []command           `json:"commands"`
	Definitions   map[string]any      `json:"definitions"`
	Errors        json.RawMessage     `json:"errors"`
	Completion    *completion         `json:"completion"`
	ResponseFiles *responseFiles      `json:"response_files"`
	Multicall     *multicall          `json:"multicall"`
	Topics        []topic             `json:"topics"`
	legacy        bool                // written before rotini 1.4; see projectLegacy
	byPath        map[string]*command // commands by their path joined with spaces
}

type completion struct {
	MessagesEnv     string `json:"messages_env"`
	DescriptionsEnv string `json:"descriptions_env"`
}

type responseFiles struct {
	Prefix string `json:"prefix"`
}

type multicall struct {
	Prefix   string `json:"prefix"`
	Complete string `json:"complete"`
}

type topic struct {
	Name string `json:"name"`
}

type command struct {
	Name                  string            `json:"name"`
	Path                  []string          `json:"path"`
	Aliases               []string          `json:"aliases"`
	Hidden                bool              `json:"hidden"`
	Deprecated            string            `json:"deprecated"`
	DeprecatedSince       string            `json:"deprecated_since"`
	RemovedIn             string            `json:"removed_in"`
	DeprecatedIdentifiers []string          `json:"deprecated_identifiers"`
	IdentifiersRemovedIn  map[string]string `json:"deprecated_identifiers_removed_in"`
	Plugin                bool              `json:"plugin"`
	OptionsFirst          bool              `json:"options_first"`
	Passthrough           bool              `json:"passthrough"`
	Arguments             []input           `json:"arguments"`
	Flags                 []input           `json:"flags"`
	FlagGroups            []flagGroup       `json:"flag_groups"`
	FlagDependencies      []flagDependency  `json:"flag_dependencies"`
	Env                   []input           `json:"env"`
	Config                []input           `json:"config"`
	ConfigFiles           []configFile      `json:"config_files"`
	Stdin                 *stdin            `json:"stdin"`
	PluginDiscovery       *pluginDiscovery  `json:"plugin_discovery"`
	Output                any               `json:"output"`
	Stream                bool              `json:"stream"`
	ExitStatus            []exitStatus      `json:"exit_status"`
	HiddenAliases         []string          `json:"hidden_aliases"`
	ReplacedBy            string            `json:"replaced_by"`
	Stability             string            `json:"stability"`
	Effects               *effects          `json:"effects"`
	Agent                 *bool             `json:"agent"`
}

// input is an argument, flag, environment variable or config key. The four share most facts,
// so one type decodes them all; each kind leaves the others' fields empty.
type input struct {
	Name                  string               `json:"name"`
	Identifiers           []string             `json:"identifiers"` // flag
	Negated               []string             `json:"negated"`     // flag
	Variables             []string             `json:"variables"`   // env
	Key                   string               `json:"key"`         // config
	File                  string               `json:"file"`        // config
	Type                  string               `json:"type"`
	Kind                  string               `json:"kind"`
	Required              bool                 `json:"required"`
	Variadic              bool                 `json:"variadic"`    // argument
	Passthrough           bool                 `json:"passthrough"` // argument
	Glob                  bool                 `json:"glob"`        // argument
	Cascading             bool                 `json:"cascading"`
	ShortCircuit          bool                 `json:"short_circuit"`
	Role                  string               `json:"role"`
	Inherited             bool                 `json:"inherited"`
	Env                   []string             `json:"env"`
	ConfigKey             string               `json:"config_key"`
	VariableFile          string               `json:"variable_file"`
	ConfigSource          string               `json:"config_source"`
	Nesting               string               `json:"nesting"`
	Separator             string               `json:"separator"`
	From                  []string             `json:"from"`
	ImplicitValue         any                  `json:"implicit_value"`
	IgnoreCase            bool                 `json:"ignore_case"`
	DottedKeys            bool                 `json:"dotted_keys"`
	Layouts               []string             `json:"layouts"`
	Relative              string               `json:"relative"`
	Expand                []string             `json:"expand"`
	RelativeTo            string               `json:"relative_to"`
	Secret                bool                 `json:"secret"`
	Hidden                bool                 `json:"hidden"`
	Deprecated            string               `json:"deprecated"`
	DeprecatedSince       string               `json:"deprecated_since"`
	RemovedIn             string               `json:"removed_in"`
	DeprecatedIdentifiers []string             `json:"deprecated_identifiers"`
	IdentifiersRemovedIn  map[string]string    `json:"deprecated_identifiers_removed_in"`
	EnumValues            map[string]enumValue `json:"enum_values"`
	Schema                any                  `json:"schema"`
	Repeatable            *bool                `json:"repeatable"`
	Stability             string               `json:"stability"`
	ValuesFrom            string               `json:"values_from"`
	HiddenIdentifiers     []string             `json:"hidden_identifiers"`
	ReplacedBy            string               `json:"replaced_by"`
	RoleValue             string               `json:"role_value"` // flag
	Effects               *effects             `json:"effects"`    // flag
	Agent                 *bool                `json:"agent"`
}

type enumValue struct {
	Aliases           []string `json:"aliases"`
	DeprecatedAliases []string `json:"deprecated_aliases"`
	Hidden            bool     `json:"hidden"`
	Deprecated        string   `json:"deprecated"`
	DeprecatedSince   string   `json:"deprecated_since"`
	RemovedIn         string   `json:"removed_in"`
	ReplacedBy        string   `json:"replaced_by"`
}

type flagGroup struct {
	Kind  string   `json:"kind"`
	Flags []string `json:"flags"`
}

type flagDependency struct {
	When     string   `json:"when"`
	Requires []string `json:"requires"`
	Equals   []any    `json:"equals"`
	Unless   []string `json:"unless"`
	Forbids  []string `json:"forbids"`
}

type configFile struct {
	Name     string    `json:"name"`
	Format   string    `json:"format"`
	Path     string    `json:"path"`
	As       string    `json:"as"`
	Discover *discover `json:"discover"`
	Profiles *profiles `json:"profiles"`
}

// profiles is how a configuration file's named profiles are chosen.
type profiles struct {
	Under  string `json:"under"`
	Select struct {
		Flag string   `json:"flag"`
		Env  []string `json:"env"`
	} `json:"select"`
	Default string `json:"default"`
}

// effects is what running a command, or giving a flag, does, as declared.
type effects struct {
	Kind       string `json:"kind"`
	Idempotent *bool  `json:"idempotent"`
	OpenWorld  *bool  `json:"open_world"`
}

type discover struct {
	Strategy string `json:"strategy"`
	App      string `json:"app"`
	File     string `json:"file"`
}

type stdin struct {
	Format         string `json:"format"`
	Type           string `json:"type"`
	Required       bool   `json:"required"`
	Schema         any    `json:"schema"`
	Separator      string `json:"separator"`
	UnlessArgument string `json:"unless_argument"`
}

type pluginDiscovery struct {
	Prefix string `json:"prefix"`
}

type exitStatus struct {
	Code      int    `json:"code"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Retryable bool   `json:"retryable"`
	Output    any    `json:"output"`
}

// errNotContract is returned for a document that isn't a contract this package reads.
var errNotContract = errors.New("not a rotini contract")

// decode reads one contract document. which names it in errors ("old", "new").
func decode(which string, raw []byte) (*document, error) {
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("the %s contract is not JSON: %w", which, err)
	}
	if d.Format != format {
		if d.Format == "" {
			return nil, fmt.Errorf("the %s contract: %w (no format)", which, errNotContract)
		}
		return nil, fmt.Errorf("the %s contract: %w: format %q; this rotini reads %q", which, errNotContract, d.Format, format)
	}
	d.legacy = isLegacy(&d)
	d.byPath = map[string]*command{}
	for i := range d.Commands {
		d.byPath[pathKey(d.Commands[i].Path)] = &d.Commands[i]
	}
	return &d, nil
}

// isLegacy reports whether a contract was written before rotini 1.4, which added most of the
// facts the diff compares. Such a contract's error-line schema has no `candidates`, and its
// inputs have no `kind`, which rotini 1.4 always writes.
func isLegacy(d *document) bool {
	if len(d.Errors) > 0 && !bytes.Contains(d.Errors, []byte(`"candidates"`)) {
		return true
	}
	for _, c := range d.Commands {
		for _, list := range [][]input{c.Arguments, c.Flags, c.Env, c.Config} {
			for _, in := range list {
				if in.Kind == "" {
					return true
				}
			}
		}
	}
	return false
}

// projectLegacy reduces d to the facts a contract written before rotini 1.4 records, so a
// fact the old contract couldn't state reads as unknown rather than as added. `hidden` is
// kept: older contracts left hidden items out, and an item that appears hidden gives no
// finding.
func projectLegacy(d *document) *document {
	out := &document{
		Format: d.Format, Name: d.Name, Definitions: d.Definitions, Errors: d.Errors,
		byPath: map[string]*command{}, legacy: true,
	}
	if d.Completion != nil && d.Completion.MessagesEnv != "" {
		out.Completion = &completion{MessagesEnv: d.Completion.MessagesEnv}
	}
	for _, c := range d.Commands {
		p := command{
			Name: c.Name, Path: c.Path, Aliases: c.Aliases, Hidden: c.Hidden, Deprecated: c.Deprecated,
			Plugin: c.Plugin, Output: c.Output,
		}
		for _, list := range []struct {
			from []input
			to   *[]input
		}{{c.Arguments, &p.Arguments}, {c.Flags, &p.Flags}, {c.Env, &p.Env}, {c.Config, &p.Config}} {
			for _, in := range list.from {
				*list.to = append(*list.to, input{
					Name: in.Name, Identifiers: in.Identifiers, Variables: in.Variables, Key: in.Key, File: in.File,
					Required: in.Required, Variadic: in.Variadic, Cascading: in.Cascading, ShortCircuit: in.ShortCircuit,
					Inherited: in.Inherited, Env: in.Env, ConfigKey: in.ConfigKey, Secret: in.Secret && len(in.Variables) > 0,
					Hidden: in.Hidden, Deprecated: in.Deprecated, Schema: in.Schema,
				})
			}
		}
		if c.Stdin != nil {
			p.Stdin = &stdin{Format: c.Stdin.Format, Required: c.Stdin.Required, Schema: c.Stdin.Schema}
		}
		for _, e := range c.ExitStatus {
			p.ExitStatus = append(p.ExitStatus, exitStatus{Code: e.Code, Summary: e.Summary, Output: e.Output})
		}
		out.Commands = append(out.Commands, p)
	}
	for i := range out.Commands {
		out.byPath[pathKey(out.Commands[i].Path)] = &out.Commands[i]
	}
	return out
}
