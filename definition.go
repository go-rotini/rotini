package rotini

import (
	"github.com/go-rotini/recon"

	"time"
)

// Definition is the compiled command tree for a generated rotini program: codegen emits it as
// a Go literal, and the runtime parses argv, dispatches, and renders help and completion
// against it. Every shape in this file is data only, with no behavior, which is what lets the
// generated file read as a description of the CLI rather than as code.
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
	PluginPath       string              // extra directory searched for BOTH declared remotes and discovered plugins
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

// FlagGroup constrains which of a command's flags may, or must, appear together. Flags are
// referenced by their logical Name, and "set" means explicitly provided on argv — a default or
// fallback does not count. The [Parser] enforces it; a violation is a usage error.
type FlagGroup struct {
	Kind  FlagGroupKind
	Flags []string // logical flag names that make up the group
}

// FlagDependency is a conditional cross-flag requirement: when the When flag is set on argv,
// every flag in Requires must be too. "Set" follows the same convention as [FlagGroup].
type FlagDependency struct {
	When     string   // the flag whose presence triggers the requirement
	Requires []string // flags that must also be set when When is set
}

// RemoteDef describes a co-located sub-command, kubectl/git plugin style: invoking it execs
// the sibling Binary with the remaining arguments passed through.
type RemoteDef struct {
	Name    string
	Aliases []string
	Summary string        // one-line description (completion candidates carry it as "name\tsummary")
	Binary  string        // expected executable name, e.g. "kubectl-ctx"
	Timeout time.Duration // 0 means no timeout
}

// BindMeta is the generated descriptor the [Binder] consumes to fill the non-argv input
// channels. It carries the document-level concerns the dispatch-time [Definition] omits.
type BindMeta struct {
	ConfigFiles []ConfigFile // per-command config_files sources, each tagged with its Scope; the binder scopes them to the invoked chain (cascade, nearest-wins)
	// EnvPrefix scopes every derived env-var name under "<EnvPrefix>_". Explicit variable
	// names are exempt, and with a prefix set the unprefixed names no longer bind.
	EnvPrefix string
	// Sources are custom recon sources — a secrets manager, a remote config service —
	// joined into the config precedence after the declared configuration_files, so explicit
	// files beat ambient services. Codegen never emits one; the program appends its own.
	// A per-input `file:` pin stays a configuration_files anchor and cannot name a custom
	// source, and a source name colliding with a declared file is rejected loudly.
	Sources []recon.Source
	// StdinSchemas maps a command's stdin payload type name ("<Prefix>Stdin") to a
	// self-contained JSON Schema the binder validates the decoded payload against.
	StdinSchemas map[string]string
}

// ConfigFile is one configuration-file source the binder reads (reconciled by recon).
// Exactly one of Path and Discover locates the file (the spec enforces this).
type ConfigFile struct {
	Name string // logical name
	// Scope is the command path this source is declared on. Sources cascade: one is in
	// scope for the invoked chain when its Scope is one of the chain's commands. "" is
	// unscoped, in scope for every command; generated descriptors always set it.
	Scope    string
	Path     string       // fixed file path (may contain ~)
	Format   string       // "json" | "yaml" | "toml"; "" lets the binder infer from the extension
	Discover *DiscoverDef // run-time location strategy, instead of a fixed Path
	PathFrom *PathFromDef // runtime inputs that supply/override the path (spec config_source)
	// Schema is the self-contained JSON Schema the binder validates the loaded document
	// against at bind time; "" is none. The file that actually resolved is the one
	// validated, and an absent optional file passes vacuously.
	Schema string
}

// PathFromDef names the runtime inputs that supply a [ConfigFile]'s path — the declarative
// two-phase parse, where argv and env are read first and the file channel then opens whatever
// they pointed at. Precedence: the flag set on argv, then the env variable, then the flag's
// default, then the entry's own path or discover. A path supplied this way must exist.
type PathFromDef struct {
	Flag string // logical flag name searched across the resolved chain
	Env  string // environment variable read directly; comma-separated names: the first one set wins
}

// DiscoverDef locates a configuration file at run time. The strategy orders the directories
// searched for File; the first containing it wins, and a file found nowhere is simply absent.
type DiscoverDef struct {
	Strategy string // "walk-up" (working directory up to the filesystem root) | "xdg" ($XDG_CONFIG_HOME/<app>, default ~/.config/<app>)
	File     string // the file name looked for in each searched directory
	App      string // the application directory under the XDG config root (xdg only)
}

// RemoteDiscoveryDef enables plugin discovery on a command: an unmatched token execs the
// sibling binary Prefix+<token>, and `<Prefix>*` executables are offered as completion
// candidates unless Hidden. A nil pointer means discovery is off for that command.
type RemoteDiscoveryDef struct {
	Prefix string // executable-name prefix, e.g. "acme-"
	Hidden bool   // dispatch discovered plugins but omit them from completion listings
}

// CommandDef describes one command node within a [Definition]. Handler is the ProgramHandlers
// method name the runtime invokes to obtain this command's [Handlers].
type CommandDef struct {
	Name                  string
	Aliases               []string
	Summary               string   // one-line description (completion candidates carry it as "name\tsummary")
	Handler               string   // ProgramHandlers method, e.g. "RotiniGenerate"
	Hidden                bool     // omitted from completion candidates (it still dispatches); help omission happens at codegen
	DeprecatedIdentifiers []string // aliases (subset of Aliases) that [Deprecations] reports when used to invoke
	Flags                 []FlagDef
	Arguments             []ArgDef
	FlagGroups            []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies      []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands              []CommandDef
	Remotes               []RemoteDef         // co-located remote binaries dispatched as sub-commands of this command
	Discovery             *RemoteDiscoveryDef // plugin auto-discovery on this command (nil = off)
	PluginPath            string              // extra directory searched for BOTH this command's declared remotes and its discovered plugins
	Passthrough           bool                // every token after this command is a raw positional (no flag parsing)
}

// Constraints carries the validation bounds a spec may declare on a flag or argument. The
// parser enforces them after reconciliation, so a value supplied via env or config is checked
// too. The numeric bounds are presence-carrying pointers — nil is unset, so `minimum: 0` is a
// real, enforced bound. The length and count bounds keep the zero-sentinel convention: a 0
// minimum is vacuous and a 0 maximum is not expressible.
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

// Ptr returns a pointer to v, for the presence-carrying [Constraints] bounds:
// Constraints{Minimum: rotini.Ptr(0.0)} declares an enforced >= 0.
//
//go:fix inline
func Ptr[T any](v T) *T { return new(v) }

// takesValue reports whether a flag consumes a value token — everything but the presence
// flags, bool (inline value form only) and count.
func takesValue(fd FlagDef) bool { return fd.Type != "bool" && fd.Type != "count" }

// FlagDef describes a single flag of a command. Name is the logical name and
// matches the `rotini:"<name>"` tag on the corresponding generated input field.
type FlagDef struct {
	Name        string
	Identifiers []string // CLI forms, e.g. {"--loud", "-l"}
	Summary     string   // one-line description (completion candidates carry it as "identifier\tsummary")
	Type        string   // resolved Go type, e.g. "bool", "string", "[]string", "int", "time.Duration"
	Required    bool
	Default     string
	// Defaults is the multi-value default for a REPEATABLE input (a `[]…` or map type):
	// each element is seeded as one occurrence, exactly as if the user had repeated the
	// flag. It is used only when Default is empty, and only when the input is unset from
	// every channel — a default never merges with a supplied value.
	//
	// It exists because Default is one string: before it, a repeatable flag could not
	// express a multi-value default at all, and the only advice was to seed it in the
	// handler, which is the one thing declaring inputs in a spec exists to avoid.
	Defaults []string
	Enum     []string
	// IgnoreCase matches a value against Enum without regard to case (`--mode FAST` against
	// fast/slow) and binds the declared spelling, so a handler compares against one form.
	IgnoreCase bool
	// Separator splits each value of a list or map flag into several (`--tags a,b` is two
	// tags), CSV-style: a quoted item keeps the separator (`--tags '"a,b",c'`). Empty means
	// one value per occurrence.
	Separator string
	// ImplicitValue is the value a flag takes when given without one (`--color` means
	// "always"), making its value optional: a value must then be attached (`--color=never`),
	// since the next argument is never consumed. Empty means the flag requires a value.
	ImplicitValue         string
	Secret                bool     // when true, the value is redacted in usage/validation error output
	Hidden                bool     // omitted from completion candidates (it still parses); help omission happens at codegen
	DeprecatedIdentifiers []string // identifiers (subset of Identifiers) that [Deprecations] reports when used
	// Negatable adds a "--no-<x>" form for every long identifier of a bool flag, which sets
	// it false. It is how an author expresses "turn this off for one run" when a default, a
	// config file or an environment variable already turned it on — the direction a plain
	// bool cannot express at all.
	Negatable  bool
	DottedKeys bool     // map flag whose key=value keys are '.'-separated paths into nested maps (spec dotted_keys)
	KeyPaths   []string // a map flag's declared key paths (from its schema's properties), completed up to the '='
	From       []string // extra acquisition modes (spec from:): "file" resolves @path values, "stdin" resolves a bare "-"
	// Complete is the declarative shell-completion hint for this flag's value (spec
	// complete:). The zero value means no hint.
	Complete Completion
	Constraints
}

// Completion is a declarative hint about what an input's VALUE is, for the shell to complete.
//
// It covers the case between a static Enum and a [FlagValueCompleter]: "this is a file", which
// is the commonest value shape there is and the one that previously required writing Go. The
// hint reaches the shell as a directive on the last line of the hidden __complete output, and
// each generated script translates it into that shell's own path completion.
//
// A dynamic completer still wins when it answers — the hint is the fallback, not a ceiling.
type Completion struct {
	// Kind is "file", "directory", or "none". Empty means no hint: the shell applies its
	// own default, which for bash and zsh is file completion.
	//
	// "none" is not the same as empty. It SUPPRESSES the shell's default, which is how an
	// opaque identifier — a container id, an API resource name — stops a shell offering
	// the contents of the current directory as if they were plausible values.
	Kind string
	// Extensions narrows Kind "file" to these suffixes, written without a dot
	// ("yaml", "json"). Empty offers every file.
	Extensions []string
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
	// IgnoreCase matches a value against Enum without regard to case and binds the declared
	// spelling; see [FlagDef.IgnoreCase].
	IgnoreCase bool
	// Separator splits each value of a variadic argument into several; see [FlagDef.Separator].
	Separator string
	// Complete is the declarative shell-completion hint for this argument's value.
	Complete Completion
	Secret   bool // when true, the value is redacted in usage/validation error output
	Hidden   bool // omitted from completion candidates (it still parses); help omission happens at codegen
	Constraints
}
