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
	Name           string
	Aliases        []string
	Handler        string // ProgramHandlers method for the root command, e.g. "Rotini"
	Version        string // value of the program's version metadata var (ldflag-settable)
	Flags          []FlagDef
	Arguments      []ArgDef
	Commands       []CommandDef
	RemoteCommands []RemoteDef         // co-located plugin sub-commands (Model 3)
	Discovery      *RemoteDiscoveryDef // plugin auto-discovery on the root command (nil = off)
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
	Name      string
	Aliases   []string
	Handler   string // ProgramHandlers method, e.g. "RotiniGenerate"
	Flags     []FlagDef
	Arguments []ArgDef
	Commands  []CommandDef
	Discovery *RemoteDiscoveryDef // plugin auto-discovery on this command (nil = off)
}

// FlagDef describes a single flag of a command. Name is the logical name and
// matches the `rotini:"<name>"` tag on the corresponding generated input field.
type FlagDef struct {
	Name        string
	Identifiers []string // CLI forms, e.g. {"--loud", "-l"}
	Type        string   // resolved Go type, e.g. "bool", "string", "[]string", "int", "time.Duration"
	Required    bool
	Default     string
	Enum        []string
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
}
