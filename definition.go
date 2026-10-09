package rotini

import (
	"time"
)

// Definition is the compiled command tree of a generated rotini program. Codegen emits it as a
// Go literal; the runtime parses argv, dispatches and completes against it. Help pages are
// rendered at codegen and supplied through [Program.WithHelp]. The Definition types are data
// only, with no behavior.
//
// A Definition is the runtime form of the command tree `rotini generate` produces from your
// spec. Building one by hand is supported for tests ([NewContextFor]) and for tooling; it is
// not a way to define a CLI, which the spec is. Fields are added as the spec gains keys,
// including in minor releases, so construct values with field names
// (rotini.FlagDef{Name: "verbose"}), not positionally.
type Definition struct {
	Name             string
	Handler          string // ProgramHandlers method for the root command, e.g. "Rotini"
	Flags            []FlagDef
	Arguments        []ArgDef
	FlagGroups       []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands         []CommandDef
	Plugins          []PluginDef         // co-located plugin binaries dispatched as sub-commands of the root
	PluginDiscovery  *PluginDiscoveryDef // plugin auto-discovery on the root command (nil = off)
	PluginPath       string              // extra directory searched for BOTH declared and discovered plugins
	Passthrough      bool                // every token after the program name is a raw positional (no flag parsing)
	OptionsFirst     bool                // flags stop at the root's first argument; see [CommandDef.OptionsFirst]
	Output           *OutputDef          // what the root command writes to stdout (nil = not declared)

	// CompletionMessages turns on completion messages, when the conf's completion feature
	// declares `messages`; nil leaves them off. See [Context.AddCompletionMessage].
	CompletionMessages *CompletionMessagesDef

	// CompletionDescriptions is how completion descriptions are switched off at run time, when
	// the conf's completion feature declares `descriptions_env`; nil leaves them always on. See
	// [Program.WithCompletionDescriptions].
	CompletionDescriptions *CompletionDescriptionsDef

	// ResponseFiles turns on response files: a word starting with the prefix names a file whose
	// lines are read as more words. nil leaves them off.
	ResponseFiles *ResponseFilesDef
}

// ResponseFilesDef is how a program reads response files (the spec's root `response_files`).
// Before the first "--", a word starting with Prefix (`@args.rsp`) is replaced by the file's
// lines, one word per line; a doubled prefix (`@@x`) stands for the literal word with one
// prefix (`@x`). Expansion happens once, before command resolution, so every parse sees the
// expanded words, and [Context.Argv] holds them. [NewContextFor] doesn't expand.
type ResponseFilesDef struct {
	Prefix string // one character, such as "@"
}

// EnumValue describes one declared value of an enum input beyond its spelling: the spec's
// `{value, summary}` form of an enum item.
type EnumValue struct {
	Value   string
	Summary string // one line saying what the value means; "" when none
}

// CompletionMessagesDef is how completion messages are switched at run time, from the conf's
// completion feature.
type CompletionMessagesDef struct {
	// Env is the environment variable the conf's `messages_env` names, "" for none. Set to
	// 0, false or off, in any case, it hides every message. [Program.WithCompletionMessages]
	// replaces this check.
	Env string
}

// CompletionDescriptionsDef is how completion descriptions are switched at run time, from the
// conf's completion feature.
type CompletionDescriptionsDef struct {
	// Env is the environment variable the conf's `descriptions_env` names. Set to 0, false or
	// off, in any case, it hides every candidate's description in every shell.
	// [Program.WithCompletionDescriptions] replaces this check.
	Env string
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

// PluginDef describes a co-located sub-command, kubectl/git plugin style: invoking it execs
// the sibling Binary with the remaining arguments passed through.
type PluginDef struct {
	Name    string
	Aliases []string
	Summary string        // one-line description (completion candidates carry it as "name\tsummary")
	Binary  string        // expected executable name, e.g. "kubectl-ctx"
	Timeout time.Duration // 0 means no timeout
}

// PluginDiscoveryDef enables plugin discovery on a command: an unmatched token execs the
// sibling binary Prefix+<token>, and `<Prefix>*` executables are offered as completion
// candidates unless Hidden. A nil pointer means discovery is off for that command.
type PluginDiscoveryDef struct {
	Prefix string // executable-name prefix, e.g. "acme-"
	Hidden bool   // dispatch discovered plugins but omit them from completion listings
}

// CommandDef describes one command node within a [Definition]. Handler is the ProgramHandlers
// method name the runtime invokes to obtain this command's [Handler].
type CommandDef struct {
	Name                  string
	Aliases               []string
	Summary               string   // one-line description (completion candidates carry it as "name\tsummary")
	Handler               string   // ProgramHandlers method, e.g. "RotiniGenerate"
	Hidden                bool     // omitted from completion candidates (it still dispatches); help omission happens at codegen
	DeprecatedIdentifiers []string // aliases (subset of Aliases) that [Deprecations] reports when used to invoke
	// Deprecated is the command's deprecation message: invoking it by any name reports a
	// [Deprecation] carrying it. Empty means the command is not deprecated as a whole.
	Deprecated       string
	Flags            []FlagDef
	Arguments        []ArgDef
	FlagGroups       []FlagGroup      // cross-flag presence rules validated at parse time
	FlagDependencies []FlagDependency // conditional cross-flag requirements validated at parse time
	Commands         []CommandDef
	Plugins          []PluginDef         // plugin binaries dispatched as sub-commands of this command
	PluginDiscovery  *PluginDiscoveryDef // plugin auto-discovery on this command (nil = off)
	PluginPath       string              // extra directory searched for BOTH this command's declared plugins and its discovered plugins
	Passthrough      bool                // every token after this command is a raw positional (no flag parsing)
	Output           *OutputDef          // what the command writes to stdout (nil = not declared)

	// OptionsFirst stops flag parsing at this command's first argument when it is the invoked
	// command: every later word is an argument, flag-shaped words and "--" included. Words
	// before that argument parse as usual. Sub-commands don't inherit it.
	OptionsFirst bool
}

// Constraints carries the validation bounds a spec may declare on a flag or argument. The
// parser enforces them after reconciliation, so a value supplied via env or config is checked
// too. The numeric bounds and the two maximums are pointers, nil meaning unset, so `minimum: 0`
// and `maxItems: 0` are enforced bounds. For the two minimums 0 means unset.
type Constraints struct {
	Minimum          *float64 // inclusive numeric lower bound; nil = unset
	Maximum          *float64 // inclusive numeric upper bound; nil = unset
	ExclusiveMinimum *float64 // strict numeric lower bound (value must be >); nil = unset
	ExclusiveMaximum *float64 // strict numeric upper bound (value must be <); nil = unset
	MultipleOf       *float64 // the value must be an integer multiple (strictly positive); nil = unset
	MinLength        int      // minimum string length in runes; 0 = unset
	MaxLength        *int     // maximum string length in runes; nil = unset
	MinItems         int      // minimum item count (repeatable flag / variadic argument); 0 = unset
	MaxItems         *int     // maximum item count; nil = unset
	Pattern          string   // regular expression the value must contain (string types); "" = unset
	PatternMessage   string   // what a Pattern failure tells the user, in place of the regex; "" = show the regex
	// UniqueItems rejects a list that holds the same value twice (repeatable flag / variadic
	// argument). Values compare as their type reads them, so `01` and `1` are the same int.
	UniqueItems bool
}

// Ptr returns a pointer to v, for the pointer [Constraints] bounds:
// Constraints{Minimum: rotini.Ptr(0.0)} declares an enforced >= 0. Prefer the built-in new(v);
// go fix inlines Ptr to it.
//
//go:fix inline
func Ptr[T any](v T) *T { return new(v) }

// takesValue reports whether a flag consumes a value token: every type but bool (which takes
// a value only in the inline form) and count.
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
	Defaults []string
	Enum     []string
	// EnumValues lists every Enum value, in order, with what it declares beyond its spelling (a
	// summary); nil when no value declares more than its spelling.
	EnumValues []EnumValue
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
	ImplicitValue string
	// Layout is how a time input's value is written: a Go reference-time layout
	// ("2006-01-02", "Jan 2 2006 15:04"), or "unix" / "unixmilli" for a timestamp. Empty means
	// RFC 3339. `type: date` gets "2006-01-02".
	Layout string
	// ObjectSchema is the JSON Schema of an object-valued flag's value — set when the spec's
	// schema is a named object (`$ref: '#/schemas/DB'`), or a list of them. The flag then
	// takes JSON, key=value pairs, a YAML @file, or one field per flag (--db.host=…); see
	// "Input values" in the package documentation.
	ObjectSchema          string
	Secret                bool     // when true, the value is redacted in usage/validation error output
	Hidden                bool     // omitted from completion candidates (it still parses); help omission happens at codegen
	DeprecatedIdentifiers []string // identifiers (subset of Identifiers) that [Deprecations] reports when used
	// Deprecated is the flag's deprecation message: setting it by any identifier reports a
	// [Deprecation] carrying it. Empty means the flag is not deprecated as a whole.
	Deprecated string
	// Negatable adds a "--no-<x>" form for every long identifier of a bool flag, which sets
	// it false, overriding a true default, config value or environment variable.
	Negatable bool
	// ShortCircuit marks a flag that replaces the command's normal run (--help, --version):
	// when it is set on the command line, every declared requirement of the chain is waived,
	// so [Context.Inputs] succeeds and the handler decides what to do. Errors reading the
	// command line are still reported.
	ShortCircuit bool
	// NoRepeat makes a single-value flag given more than once on the command line an error
	// (spec `repeatable: false`) instead of the last value winning.
	NoRepeat   bool
	DottedKeys bool     // map flag whose key=value keys are '.'-separated paths into nested maps (spec dotted_keys)
	KeyPaths   []string // a map flag's declared key paths (from its schema's properties), completed up to the '='
	From       []string // extra acquisition modes (spec from:): "file" resolves @path values, "stdin" resolves a bare "-"
	// Complete is the declarative shell-completion hint for this flag's value (spec
	// complete:). The zero value means no hint.
	Complete Completion
	Constraints
}

// Completion is a declarative hint about what an input's value is, for the shell to complete.
// It reaches the shell as a directive on the last line of the hidden __complete output, and
// each generated script translates it into that shell's own path completion. A dynamic
// completer ([FlagValueCompleter], [ArgValueCompleter]) wins when it answers; the hint is the
// fallback.
type Completion struct {
	// Kind is what the value is. Empty means no hint: the shell applies its own default, which
	// for bash and zsh is file completion.
	//   - "file" and "directory" complete paths; "none" completes nothing, suppressing that
	//     default, for opaque values such as resource IDs.
	//   - "command" completes command paths below the root: the words already given to the
	//     argument are the path, and the candidates are that command's visible sub-commands.
	//     Rotini answers it itself, and tells the shell "none".
	//   - "executable", "user", "group" and "host" are completed by the shell's own completer:
	//     program names (bash offers every command name it knows), local accounts and groups,
	//     and the host names the shell knows. PowerShell has no host completer and offers
	//     users and groups on Windows only.
	Kind string
	// Extensions narrows Kind "file" to these suffixes, written without a dot
	// ("yaml", "json"). Empty offers every file.
	Extensions []string
	// Message is a line shown when the input's value is being completed and there is nothing
	// to offer, written by the author in the spec (`complete.message`) or derived from its
	// summary. It is set only when completion messages are on.
	Message string
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
	// EnumValues describes the Enum values; see [FlagDef.EnumValues].
	EnumValues []EnumValue
	// IgnoreCase matches a value against Enum without regard to case and binds the declared
	// spelling; see [FlagDef.IgnoreCase].
	IgnoreCase bool
	// Separator splits each value of a variadic argument into several; see [FlagDef.Separator].
	Separator string
	// Layout is how a time argument's value is written; see [FlagDef.Layout].
	Layout string
	// Deprecated is the argument's deprecation message: supplying it reports a [Deprecation]
	// carrying it.
	Deprecated string
	// Complete is the declarative shell-completion hint for this argument's value.
	Complete Completion
	Secret   bool // when true, the value is redacted in usage/validation error output
	Hidden   bool // omitted from completion candidates (it still parses); help omission happens at codegen
	// Passthrough marks the last argument as where raw words start: flags before it parse as
	// usual, and it and every word after it are taken as typed, flag-shaped words and "--"
	// included. It is a variadic []string. See [Context.DashIndex].
	Passthrough bool
	Constraints
}
