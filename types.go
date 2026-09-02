package rotini

import (
	"github.com/go-rotini/recon"

	"context"
	"time"
)

// Handlers is the lifecycle interface every command's handler set
// implements. The runtime invokes the hooks in order, sharing one [Context]
// across the chain; handlers read their typed inputs with [Parser.Parse].
type Handlers interface {
	CascadingPreRun(ctx context.Context, rtx *Context)
	PreRun(ctx context.Context, rtx *Context)
	Run(ctx context.Context, rtx *Context)
	PostRun(ctx context.Context, rtx *Context)
	CascadingPostRun(ctx context.Context, rtx *Context)
}

// Definition is the compiled command tree for a generated rotini program. The
// generated package emits it as a Go literal and the rollup passes it to
// [NewProgram]; the runtime uses it to parse argv, dispatch, and render help and
// completion. It is data only — behavior lives in the handlers.
type Definition struct {
	Name             string
	Handler          string // ProgramHandlers method for the root command, e.g. "Rotini"
	Flags            []FlagDef
	Arguments        []ArgDef
	FlagGroups       []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands         []CommandDef
	RemoteCommands   []RemoteDef         // co-located plugin sub-commands (Model 3)
	Discovery        *RemoteDiscoveryDef // plugin auto-discovery on the root command (nil = off)
	Passthrough      bool                // every token after the program name is a raw positional (no flag parsing)
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
// matching the convention of cobra/clap). Enforced by the [Parser]; a violation is
// a usage error.
type FlagGroup struct {
	Kind  FlagGroupKind
	Flags []string // logical flag names that make up the group
}

// FlagDependency is a conditional cross-flag requirement: when the When flag is
// explicitly set on argv, every flag in Requires must also be set. Flags are referenced
// by their logical Name and "set" follows the same explicit-argv convention as
// [FlagGroup]. Enforced by the [Parser]; a violation is a usage error.
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
	Summary string        // one-line description (completion candidates carry it as "name\tsummary")
	Binary  string        // expected executable name, e.g. "kubectl-ctx"
	Timeout time.Duration // 0 means no timeout
}

// BindMeta is the generated, data-only descriptor the default binder ([Binder])
// consumes to fill the non-argv input channels. It carries document-level concerns
// that the dispatch-time Definition deliberately omits. The generated package emits it as
// `var BindMeta = rotini.BindMeta{…}`; main.go passes it to [NewBinder].
type BindMeta struct {
	ConfigFiles []ConfigFile // per-command config_files sources, each tagged with its Scope; the binder scopes them to the invoked chain (cascade, nearest-wins)
	// EnvPrefix scopes every DERIVED env-var name (the SNAKE_UPPER projections:
	// plain env inputs without variable:, envnest bases, flags' env fallbacks)
	// under "<EnvPrefix>_". Explicit variable: names are exempt, and with a
	// prefix set the unprefixed conventional names no longer bind. The spec's
	// document-level env_prefix; "" = no prefix (the default projection).
	EnvPrefix string
	// Sources are custom recon sources (a secrets manager, a remote config
	// service — anything implementing recon.Source) joined into the Binder's
	// config precedence AFTER the declared configuration_files: explicit
	// files beat ambient services; custom sources beat nothing. Appended by
	// the program's own code (conventionally main.go: meta := cli.BindMeta;
	// meta.Sources = append(meta.Sources, vaultSource) — codegen never
	// emits one), they serve config inputs and flags' config fallbacks
	// alike. Per-input `file:` pins stay configuration_files anchors and
	// cannot name a custom source. Source names must not collide with
	// declared file names — the registry rejects duplicates loudly.
	Sources []recon.Source
	// StdinSchemas maps a command's stdin payload type name ("<Prefix>Stdin") to a
	// self-contained JSON Schema the binder validates the decoded payload against.
	StdinSchemas map[string]string
}

// ConfigFile is one configuration-file source the binder reads (reconciled by recon).
// Exactly one of Path and Discover locates the file (the spec enforces this).
type ConfigFile struct {
	Name string // logical name
	// Scope is the command path ("root", "root/sub", …) this source is declared
	// on. config_files cascade: a source is in scope for the invoked chain when
	// its Scope is one of the chain's commands (D-W3.1). "" means UNSCOPED — in
	// scope for every command (a manually-built BindMeta or a legacy global list);
	// generated descriptors always set it.
	Scope    string
	Path     string       // fixed file path (may contain ~)
	Format   string       // "json" | "yaml" | "toml"; "" lets the binder infer from the extension
	Discover *DiscoverDef // run-time location strategy, instead of a fixed Path
	PathFrom *PathFromDef // runtime inputs that supply/override the path (spec config_source)
	// Schema is the self-contained JSON Schema the binder validates the loaded
	// document against at bind time (the spec entry's `schema:`); "" = none.
	// The file that actually resolved — fixed, discovered, or
	// config_source-supplied — is the file validated; an absent optional file
	// passes vacuously.
	Schema string
}

// PathFromDef names the runtime inputs that supply a [ConfigFile]'s path (the
// spec's config_source) — the declarative two-phase parse: argv and env are
// read first, then the file channel opens whatever they pointed at. The path
// precedence is: the flag explicitly set on argv, then the env variable, then
// the flag's declared default, then the entry's own path/discover. A path
// supplied this way must exist — the user explicitly asked for it.
type PathFromDef struct {
	Flag string // logical flag name searched across the resolved chain
	Env  string // environment variable read directly
}

// DiscoverDef locates a configuration file at run time (the spec's discover:).
// The strategy orders the directories searched for File; the first directory
// containing it wins, and a file found nowhere is simply absent.
type DiscoverDef struct {
	Strategy string // "walk-up" (working directory up to the filesystem root) | "xdg" ($XDG_CONFIG_HOME/<app>, default ~/.config/<app>)
	File     string // the file name looked for in each searched directory
	App      string // the application directory under the XDG config root (xdg only)
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
// this command's [Handlers].
type CommandDef struct {
	Name                  string
	Aliases               []string
	Summary               string   // one-line description (completion candidates carry it as "name\tsummary")
	Handler               string   // ProgramHandlers method, e.g. "RotiniGenerate"
	Hidden                bool     // omitted from completion candidates (it still dispatches); help omission happens at codegen
	DeprecatedIdentifiers []string // aliases (subset of Aliases) that [Parser.Deprecations] reports when used to invoke
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies      []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands              []CommandDef
	Remotes               []RemoteDef         // co-located remote binaries dispatched as sub-commands of this command
	Discovery             *RemoteDiscoveryDef // plugin auto-discovery on this command (nil = off)
	Passthrough           bool                // every token after this command is a raw positional (no flag parsing)
}

// Constraints carries the optional JSON-schema-style validation bounds a spec may
// declare on a flag or argument; the parser enforces them after reconciliation (so a
// value supplied via env/config is checked too). The numeric bounds are
// presence-carrying pointers — nil means "unset", so `minimum: 0` is a real,
// enforced bound ([Ptr] builds one in a hand-authored Definition). The
// length/count bounds keep the zero-sentinel convention: a 0 minimum is
// vacuous, and a 0 maximum is not expressible (no real-world demand recorded).
type Constraints struct {
	Minimum          *float64 // inclusive numeric lower bound; nil = unset
	Maximum          *float64 // inclusive numeric upper bound; nil = unset
	ExclusiveMinimum *float64 // strict numeric lower bound (value must be >); nil = unset
	ExclusiveMaximum *float64 // strict numeric upper bound (value must be <); nil = unset
	MultipleOf       *float64 // the value must be an integer multiple (strictly positive); nil = unset
	MinLength        int      // minimum string length in runes; 0 = unset
	MaxLength        int      // maximum string length in runes; 0 = unset
	MinItems         int      // minimum item count (repeatable flag / variadic argument); 0 = unset
	MaxItems         int      // maximum item count; 0 = unset
	Pattern          string   // regular expression the value must contain (string types); "" = unset
}

// Ptr returns a pointer to v — sugar for the presence-carrying [Constraints]
// bounds in a hand-authored [Definition] (generated code uses it too):
// Constraints{Minimum: rotini.Ptr(0.0)} declares an enforced >= 0.
//
//go:fix inline
func Ptr[T any](v T) *T { return new(v) }

// takesValue reports whether a flag consumes a value token: everything except
// the presence flags — bool (value form is inline-only) and count (no value at
// all; each occurrence increments the generated int field).
func takesValue(fd FlagDef) bool { return fd.Type != "bool" && fd.Type != "count" }

// FlagDef describes a single flag of a command. Name is the logical name and
// matches the `rotini:"<name>"` tag on the corresponding generated input field.
type FlagDef struct {
	Name                  string
	Identifiers           []string // CLI forms, e.g. {"--loud", "-l"}
	Summary               string   // one-line description (completion candidates carry it as "identifier\tsummary")
	Type                  string   // resolved Go type, e.g. "bool", "string", "[]string", "int", "time.Duration"
	Required              bool
	Default               string
	Enum                  []string
	Secret                bool     // when true, the value is redacted in usage/validation error output
	Hidden                bool     // omitted from completion candidates (it still parses); help omission happens at codegen
	DeprecatedIdentifiers []string // identifiers (subset of Identifiers) that [Parser.Deprecations] reports when used
	DottedKeys            bool     // map flag whose key=value keys are '.'-separated paths into nested maps (spec dotted_keys)
	KeyPaths              []string // a map flag's declared key paths (from its schema's properties), completed up to the '='
	From                  []string // extra acquisition modes (spec from:): "file" resolves @path values, "stdin" resolves a bare "-"
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
	Hidden   bool // omitted from completion candidates (it still parses); help omission happens at codegen
	Constraints
}
