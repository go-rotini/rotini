package rotini

import (
	"context"
	"time"
)

// CommandHandlers is the lifecycle interface every command's handler set
// implements. The runtime invokes the hooks in order, sharing one [Context]
// across the chain; handlers read their typed inputs with the rtk package's Parse.
type CommandHandlers interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
	CascadingPostRun(ctx context.Context, rtx *Context)
}

// Definition is the compiled command tree for a generated rotini program. The
// framework package (rtg) emits it as a Go literal and the rollup passes it to
// [NewProgram]; the runtime uses it to parse argv, dispatch, and render help and
// completion. It is data only — behavior lives in the handlers.
type Definition struct {
	Name             string
	Aliases          []string
	Handler          string // ProgramHandlers method for the root command, e.g. "Rotini"
	Flags            []FlagDef
	Arguments        []ArgDef
	FlagGroups       []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands         []CommandDef
	RemoteCommands   []RemoteDef         // co-located plugin sub-commands (Model 3)
	Discovery        *RemoteDiscoveryDef // plugin auto-discovery on the root command (nil = off)
}

// FlagGroupKind names a cross-flag presence rule. The value is the spec's `kind`.
type FlagGroupKind string

const (
	// FlagGroupMutuallyExclusive: at most one of the group's flags may be set.
	FlagGroupMutuallyExclusive FlagGroupKind = "mutually_exclusive"
	// FlagGroupRequiredTogether: set all of the group's flags, or none.
	FlagGroupRequiredTogether FlagGroupKind = "required_together"
	// FlagGroupOneOf: exactly one of the group's flags must be set.
	FlagGroupOneOf FlagGroupKind = "one_of"
	// FlagGroupAtLeastOne: at least one of the group's flags must be set.
	FlagGroupAtLeastOne FlagGroupKind = "at_least_one"
)

// FlagGroup is a constraint on which of a command's flags may (or must) appear
// together on the command line. Flags are referenced by their logical Name; "set"
// means explicitly provided on argv (a default or env/config fallback does not count,
// matching the convention of cobra/clap). Enforced by the rtk parser; a violation is
// a usage error.
type FlagGroup struct {
	Kind  FlagGroupKind
	Flags []string // logical flag names that make up the group
}

// FlagDependency is a conditional cross-flag requirement: when the When flag is
// explicitly set on argv, every flag in Requires must also be set. Flags are referenced
// by their logical Name and "set" follows the same explicit-argv convention as
// [FlagGroup]. Enforced by the rtk parser; a violation is a usage error.
type FlagDependency struct {
	When     string   // the flag whose presence triggers the requirement
	Requires []string // flags that must also be set when When is set
}

// RemoteDef describes a remote/co-located sub-command (kubectl/git plugin
// style): invoking it execs the sibling binary Binary, passing through the
// remaining arguments.
type RemoteDef struct {
	Name    string
	Aliases []string
	Binary  string        // expected executable name, e.g. "kubectl-ctx"
	Timeout time.Duration // 0 means no timeout
}

// BindMeta is the generated, data-only descriptor the default binder (rtk.Binder)
// consumes to fill the non-argv input channels. It carries document-level concerns
// that the dispatch-time Definition deliberately omits. The rtg package emits it as
// `var BindMeta = rotini.BindMeta{…}`; main.go hands it to rtk.NewProgram's binder.
type BindMeta struct {
	ConfigFiles []ConfigFile // document-level configuration_files sources, in declared order
	// StdinSchemas maps a command's stdin payload type name ("<Prefix>Stdin") to a
	// self-contained JSON Schema the binder validates the decoded payload against.
	StdinSchemas map[string]string
}

// ConfigFile is one configuration-file source the binder reads (reconciled by recon).
type ConfigFile struct {
	Name   string // logical name
	Path   string // file path (may contain ~)
	Format string // "json" | "yaml" | "toml"; "" lets the binder infer from the extension
}

// RemoteDiscoveryDef enables kubectl/git/gh-style plugin discovery on a command:
// an unmatched token execs the sibling binary Prefix+<token>, and `<Prefix>*`
// executables are offered as completion candidates (unless Hidden). A nil
// *RemoteDiscoveryDef means discovery is off for that command.
type RemoteDiscoveryDef struct {
	Prefix string // executable-name prefix, e.g. "acme-"
	Path   string // extra directory to scan, in addition to the host dir and PATH
	Hidden bool   // dispatch discovered plugins but omit them from completion listings
}

// CommandDef describes one command node within a [Definition]. Handler is the
// ProgramHandlers method name the runtime invokes (via reflection) to obtain
// this command's [CommandHandlers].
type CommandDef struct {
	Name                  string
	Aliases               []string
	Handler               string   // ProgramHandlers method, e.g. "RotiniGenerate"
	DeprecatedIdentifiers []string // aliases (subset of Aliases) that rtk's Deprecations reports when used to invoke
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies      []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands              []CommandDef
	Discovery             *RemoteDiscoveryDef // plugin auto-discovery on this command (nil = off)
}

// Constraints carries the optional JSON-schema-style validation bounds a spec may
// declare on a flag or argument; the parser enforces them after reconciliation (so a
// value supplied via env/config is checked too). A zero value means "unset",
// consistent with the rest of a Definition: a 0 numeric bound, a 0 length/item bound,
// or an empty Pattern is not enforced. A consequence of that zero-sentinel
// representation is that `minimum: 0` and `maximum: 0` are treated as no bound — a
// documented limitation (use a small epsilon, or rely on the unsigned type, if a
// literal-zero bound matters).
type Constraints struct {
	Minimum   float64 // numeric lower bound (int/float types); 0 = unset
	Maximum   float64 // numeric upper bound (int/float types); 0 = unset
	MinLength int     // minimum string length in runes; 0 = unset
	MaxLength int     // maximum string length in runes; 0 = unset
	MinItems  int     // minimum item count (repeatable flag / variadic argument); 0 = unset
	MaxItems  int     // maximum item count; 0 = unset
	Pattern   string  // regular expression the value must contain (string types); "" = unset
}

// FlagDef describes a single flag of a command. Name is the logical name and
// matches the `rotini:"<name>"` tag on the corresponding generated input field.
type FlagDef struct {
	Name                  string
	Identifiers           []string // CLI forms, e.g. {"--loud", "-l"}
	Type                  string   // resolved Go type, e.g. "bool", "string", "[]string", "int", "time.Duration"
	Required              bool
	Default               string
	Enum                  []string
	Secret                bool     // when true, the value is redacted in usage/validation error output
	DeprecatedIdentifiers []string // identifiers (subset of Identifiers) that rtk's Deprecations reports when used
	Constraints
}

// ArgDef describes a single positional argument of a command. Variadic is true
// for a trailing slice argument that absorbs the remaining positionals.
type ArgDef struct {
	Name     string
	Type     string
	Required bool
	Variadic bool
	Default  string
	Enum     []string
	Secret   bool // when true, the value is redacted in usage/validation error output
	Constraints
}
