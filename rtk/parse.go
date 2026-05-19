package rtk

import (
	"io"
	"time"
)

// Spec types describe a CLI's static shape — its commands, flags, arguments,
// stdin contract, and constraints. Codegen emits a [ProgramSpec] literal
// from the user's .rotini.spec.yaml; the parser consumes it to drive
// argv → Inputs resolution.
//
// The types are intentionally Go-shaped, not YAML-shaped: where YAML uses a
// nested `schema:` block to hold validation constraints, the spec types
// flatten those constraints onto the FlagSpec / ArgumentSpec directly.

// ProgramSpec describes a complete CLI program: its name, its program-level
// (root) flags, its command tree, and optionally a root-command stdin
// contract.
type ProgramSpec struct {
	// Name is the binary name (e.g., "rotini", "todo"). Used in help output
	// and shell-completion scripts.
	Name string

	// Summary is a one-line description of the program. Optional.
	Summary string

	// Description is a multi-line description of the program. Optional.
	Description string

	// Flags are the program-level (root) flags. These are accessible at every
	// command scope because they're declared on the root.
	Flags []FlagSpec

	// Commands is the nested command tree. Each entry's Path is set to the
	// hyphen-joined path from the root (e.g., "generate", "foo-bar-baz"); the
	// nested Commands slice carries direct children, recursively.
	Commands []CommandSpec

	// RootStdin describes the stdin contract for the root command, if any.
	// Most programs leave this nil and declare stdin per-command.
	RootStdin *StdinSpec
}

// CommandSpec describes a single command — its identifiers, its inputs, and
// (if any) its children.
//
// Path and Commands are dual-encoded. The parser uses Commands during argv
// walking to identify scope transitions; Path is the hyphen-joined cumulative
// name used as the lookup key by [Target.RotiniCommandPath] and the executor
// map.
type CommandSpec struct {
	// Path is the hyphen-joined cumulative command path from the root:
	// "" for the root command itself, "generate" for a direct child of the
	// root, "foo-bar-baz" for a three-level nested command.
	Path string

	// Name is the leaf command name (the last path segment). Empty for the
	// root command.
	Name string

	// Aliases lists alternative names accepted on the command line.
	Aliases []string

	// Summary is a one-line description used in help output. Optional.
	Summary string

	// Description is the long-form description. Optional.
	Description string

	// When non-empty, marks the command as deprecated and surfaces
	// the message when the user invokes it.
	Deprecated string

	// Flags are command-scoped flags. They are accessible only when this
	// command (or one of its descendants) is the active command.
	Flags []FlagSpec

	// Arguments are positional arguments consumed at this command's leaf.
	// At most one Variadic argument is allowed and it must be the last one.
	Arguments []ArgumentSpec

	// Stdin describes the stdin contract for this command, if any.
	Stdin *StdinSpec

	// Commands are nested children of this command.
	Commands []CommandSpec

	// Timeout, if non-zero, caps the duration of the Run hook for this
	// command. A zero value means no timeout.
	Timeout time.Duration
}

// FlagSpec describes a single flag — its identifiers, its type, its
// constraints, and its bindings to env vars / config keys.
type FlagSpec struct {
	// Name is the canonical flag name in the generated Inputs struct
	// (e.g., "output" becomes Inputs.<Cmd>.Flags.Output via PascalCase).
	Name string

	// Identifiers are the command-line forms users type
	// (e.g., ["-o", "--output"]). Order is preserved for help rendering.
	Identifiers []string

	// Type is the Go type the value coerces to. Supported built-ins:
	// "string", "int", "int64", "float64", "bool", "[]string",
	// "map[string]string". Codegen-emitted CoerceValueFn can extend this.
	Type string

	// Summary is a one-line description for help output. Optional.
	Summary string

	// Description is the long-form description. Optional.
	Description string

	// Required, when true, causes parsing to fail if the flag is not
	// supplied by any source (argv / env / config / default).
	Required bool

	// Default is the value used when no source supplies the flag. Encoded as
	// a string and coerced to Type at parse time.
	Default string

	// Enum, when non-empty, restricts allowed values. Parsing fails if the
	// supplied value is not one of these.
	Enum []string

	// Pattern, when non-empty, is a regex the supplied string value must
	// match. Applies only to string-typed flags.
	Pattern string

	// Min and Max constrain numeric values inclusively. nil means unbounded
	// on that side. Apply only to numeric types.
	Min *float64
	Max *float64

	// MinLength and MaxLength constrain string length inclusively. nil
	// means unbounded. Apply only to string types.
	MinLength *int
	MaxLength *int

	// MinItems and MaxItems constrain slice / map cardinality inclusively.
	// nil means unbounded. Apply only to []T and map[K]V types.
	MinItems *int
	MaxItems *int

	// Nullable, when true, generates the field as *T instead of T, so the
	// handler can distinguish "set to zero value" from "not supplied."
	Nullable bool

	// EnvKey is the environment-variable name the value falls back to when
	// argv does not supply the flag. Empty means no env binding.
	EnvKey string

	// ConfigKey is the dot-separated config-file path the value falls back
	// to when argv and env do not supply the flag (e.g.,
	// "server.port"). Empty means no config binding.
	ConfigKey string

	// EnvOnly restricts the value source to env vars — argv and config are
	// rejected. Useful for secrets that should never appear on the command
	// line or in config files.
	EnvOnly bool

	// When non-empty, marks the flag as deprecated and surfaces
	// the message when the user supplies it.
	Deprecated string
}

// ArgumentSpec describes a single positional argument — its type, its
// constraints, and its bindings to env vars / config keys.
type ArgumentSpec struct {
	// Name is the canonical argument name in the generated Inputs struct.
	Name string

	// Type is the Go type the value coerces to. See [FlagSpec.Type].
	Type string

	// Summary is a one-line description for help output. Optional.
	Summary string

	// Description is the long-form description. Optional.
	Description string

	// Required, when true, causes parsing to fail if the argument is not
	// supplied by any source.
	Required bool

	// Variadic, when true, consumes all remaining positional tokens at the
	// command's leaf. At most one variadic argument per command and it must
	// be the last one declared.
	Variadic bool

	// Default is the value used when no source supplies the argument.
	// Encoded as a string and coerced to Type at parse time.
	Default string

	// Enum, when non-empty, restricts allowed values.
	Enum []string

	// Pattern, when non-empty, is a regex the supplied string value must
	// match.
	Pattern string

	// Min, Max, MinLength, MaxLength, MinItems, MaxItems mirror the
	// constraints on FlagSpec.
	Min       *float64
	Max       *float64
	MinLength *int
	MaxLength *int
	MinItems  *int
	MaxItems  *int

	// Nullable, when true, generates the field as *T instead of T.
	Nullable bool

	// EnvKey is the environment-variable name the value falls back to.
	EnvKey string

	// ConfigKey is the dot-separated config-file path the value falls back
	// to.
	ConfigKey string

	// When non-empty, marks the argument as deprecated.
	Deprecated string
}

// StdinSpec describes a command's stdin contract — the format of incoming
// stdin and (for structured formats) the fields the parser exposes on the
// generated Inputs struct.
type StdinSpec struct {
	// Format identifies the wire format. Supported:
	//   - "text"  — pass through raw, trailing newline trimmed
	//   - "raw"   — pass through raw bytes, no transformation
	//   - "json"  — unmarshal as JSON into a map[string]any
	//   - "yaml"  — unmarshal as YAML
	//   - "toml"  — unmarshal as TOML
	//   - "jsonc" — unmarshal as JSON-with-comments
	Format string

	// Fields, for structured formats (json/yaml/toml/jsonc), declares the
	// shape codegen should expose on the typed StdinData struct. Ignored
	// for text/raw.
	Fields []StdinField
}

// StdinField describes one field of a structured stdin payload.
type StdinField struct {
	// Name is the JSON/YAML/TOML key, in the source's native casing
	// (typically snake_case or lowercase). Codegen PascalCases it for the
	// Go field name.
	Name string

	// Type is the Go type — same vocabulary as [FlagSpec.Type].
	Type string

	// Required, when true, fails parsing if the field is absent.
	Required bool
}

// Inputs bundles everything a parser needs for a single Parse call: the raw
// argv, the stdin reader, and lookup interfaces for env and config sources.
//
// Generated code constructs an Inputs value during Program.Execute and
// binds the resulting [Parser] under the "parser" registry key. Handler code
// retrieves the parser and calls [Parser.Parse] without re-supplying these
// inputs.
type Inputs struct {
	// Argv is the raw command-line argument slice — typically
	// os.Args[1:]. The parser tokenizes this against the [ProgramSpec]'s
	// command tree.
	Argv []string

	// Stdin is the reader the parser consults when a command declares a
	// [StdinSpec] or when argv contains a "-" stdin marker.
	Stdin io.Reader

	// Env is the environment-variable lookup. The default rtk.OS
	// implementation satisfies this interface.
	Env EnvLookup

	// Config is the config-file lookup. Generated code wraps the bound
	// "config" service (a *recon.Registry, if the spec declared configs:)
	// in an adapter satisfying this interface.
	Config ConfigLookup
}

// EnvLookup is the interface a parser uses to query environment variables.
// The default rtk.OS implementation satisfies it; tests inject fakes.
type EnvLookup interface {
	// Lookup returns the value bound to key and whether the key is set.
	// Matches the os.LookupEnv signature.
	Lookup(key string) (value string, ok bool)
}

// ConfigLookup is the interface a parser uses to query config-file values
// by dot-separated path (e.g., ["server", "port"] for "server.port").
type ConfigLookup interface {
	// Lookup returns the value at path and whether it was found. The
	// value's runtime type matches whatever the config source produced —
	// typically string, float64, bool, []any, or map[string]any.
	Lookup(path []string) (value any, ok bool)
}

// Target is the interface every codegen-emitted Inputs type satisfies. The
// parser uses [Target.RotiniCommandPath] to identify which [CommandSpec]
// applies, then calls [Target.PopulateFromArgv] to fill the struct from the
// resolved [Result].
//
// Handler code declares a target Inputs value and passes a pointer to
// [Parser.Parse]:
//
//	var inputs RotiniGenerateInputs
//	if err := rp.Parse(&inputs); err != nil { /* ... */ }
type Target interface {
	// RotiniCommandPath returns the hyphen-joined cumulative command path
	// this Inputs type targets. The root command's Inputs returns "".
	RotiniCommandPath() string

	// PopulateFromArgv fills the receiver from a resolved [Result]. Codegen
	// emits the body; it calls [AssignFlag] / [AssignStringArg] / etc.
	// against the relevant scope and argument positions.
	PopulateFromArgv(result *Result) error
}

// Result is what a [Parser] hands to [Target.PopulateFromArgv]. It captures
// the resolved view of one Parse call: which command path was matched, what
// flag values landed in each scope, what positional arguments remained, what
// stdin was shaped into, and any non-fatal warnings (deprecations etc.).
type Result struct {
	// CommandPath is the matched command path as a slice of segments
	// (e.g., ["generate"] or ["foo", "bar", "baz"]). Empty for the root
	// command.
	CommandPath []string

	// FlagsByScope maps scope names (the leaf segment of each ancestor
	// path, plus "" for the root) to maps of flag-name → coerced value.
	// Codegen-emitted PopulateFromArgv walks this map.
	FlagsByScope map[string]map[string]any

	// ParsedArgs are positional arguments consumed at the matched
	// command's leaf, in order.
	ParsedArgs []string

	// Stdin is the shaped stdin value when the active command declared a
	// [StdinSpec]. Its runtime type depends on StdinSpec.Format:
	// string for "text", []byte for "raw", map[string]any for the
	// structured formats.
	Stdin any

	// Warnings carries non-fatal diagnostics (deprecated flag/arg/command
	// usage, recoverable validation issues). Parsing succeeded; the
	// program may still emit these to stderr.
	Warnings []error
}

// Parser is the rotini argv → typed Inputs engine. It is the swap point for
// the "parser" service: bind any value satisfying this interface under the
// "parser" registry key and generated code uses it.
//
// Most CLIs use the default implementation returned by [NewParser]. Users
// integrating with a third-party flag library (e.g., spf13/pflag) write a
// thin adapter against this interface.
type Parser interface {
	// Parse tokenizes the bound argv, identifies the active command via
	// target.RotiniCommandPath(), resolves flag and argument values
	// through the argv → env → config → default precedence chain,
	// validates against the spec's constraints, and calls
	// target.PopulateFromArgv with the resolved [Result].
	//
	// The active command's scope and ancestor scopes are populated; other
	// scopes are absent from Result.FlagsByScope.
	Parse(target Target) error
}

// Ptr returns a pointer to v. Useful for constructing FlagSpec / ArgumentSpec
// literals that require *T for optional constraints (Min, Max, MinLength,
// MaxLength, MinItems, MaxItems):
//
//	rtk.FlagSpec{
//		Name: "port", Type: "int",
//		Min: rtk.Ptr(1.0), Max: rtk.Ptr(65535.0),
//	}
//
//nolint:modernize // Ptr takes a value; new(T) returns *T pointing to zero
func Ptr[T any](v T) *T { return &v }
