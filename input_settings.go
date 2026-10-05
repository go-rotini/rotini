package rotini

import (
	"github.com/go-rotini/recon"
)

// InputSettings is the generated descriptor the [InputReader] consumes to fill the non-argv input
// channels. It carries the document-level concerns the dispatch-time [Definition] omits.
type InputSettings struct {
	ConfigFiles []ConfigFile // per-command config_files sources, each tagged with its Scope; the input reader scopes them to the invoked chain (cascade, nearest-wins)
	// EnvPrefix scopes every derived env-var name under "<EnvPrefix>_". Explicit variable
	// names are exempt, and with a prefix set the unprefixed names no longer bind.
	EnvPrefix string
	// Sources are custom recon sources — a secrets manager, a remote config service —
	// joined into the config precedence after the declared config_files, so explicit
	// files beat ambient services. Codegen never emits one; the program appends its own.
	// A per-input `file:` pin stays a config_files anchor and cannot name a custom
	// source, and a source name colliding with a declared file is rejected.
	Sources []recon.Source
	// StdinSchemas maps a command's stdin payload type name ("<Prefix>Stdin") to a
	// self-contained JSON Schema the input reader validates the decoded payload against.
	StdinSchemas map[string]string
}

// ConfigFile is one configuration-file source the input reader reads (reconciled by recon).
// Exactly one of Path and Discover locates the file (the spec enforces this).
type ConfigFile struct {
	Name string // logical name
	// Scope is the command path this source is declared on. Sources cascade: one is in
	// scope for the invoked chain when its Scope is one of the chain's commands. "" is
	// unscoped, in scope for every command; generated descriptors always set it.
	Scope    string
	Path     string       // fixed file path (may contain ~)
	Format   string       // "json" | "yaml" | "toml"; "" lets the input reader infer from the extension
	Discover *DiscoverDef // run-time location strategy, instead of a fixed Path
	PathFrom *PathFromDef // runtime inputs that supply/override the path (spec config_source)
	// Schema is the self-contained JSON Schema the input reader validates the loaded document
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
// searched for File; the first containing it wins, and a file found nowhere is absent.
type DiscoverDef struct {
	Strategy string // "walk-up" (working directory up to the filesystem root) | "xdg" ($XDG_CONFIG_HOME/<app>, default ~/.config/<app>)
	File     string // the file name looked for in each searched directory
	App      string // the application directory under the XDG config root (xdg only)
}
