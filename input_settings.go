package rotini

import (
	"github.com/go-rotini/recon"
)

// InputSettings is the generated descriptor the [InputReader] consumes to fill the non-argv input
// channels. It carries the document-level concerns the dispatch-time [Definition] omits.
type InputSettings struct {
	ConfigFiles []ConfigFile // per-command config_files sources, each tagged with its Scope; the input reader scopes them to the invoked chain (cascade, nearest-wins)
	// EnvPrefix scopes every derived env-var name under "<EnvPrefix>_". Explicit variable
	// names are exempt, and with a prefix set the unprefixed names don't bind.
	EnvPrefix string
	// Sources are custom recon sources — a secrets manager, a remote config service —
	// joined into the config precedence after the declared config_files, so explicit
	// files beat ambient services. Codegen never emits one; the program appends its own.
	// A per-input `file:` pin stays a config_files anchor and cannot name a custom
	// source. A source name colliding with a declared file's or another source's, or one
	// recon reserves ("explicit", "default"), fails the read with an internal [*InputError]
	// naming the sources.
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
	Format   string       // "json", "yaml", "toml", "jsonc" or "dotenv"; "" infers it from the file extension
	Discover *DiscoverDef // run-time location strategy, instead of a fixed Path
	PathFrom *PathFromDef // runtime inputs that supply/override the path (spec config_source)
	// Schema is the self-contained JSON Schema the input reader validates the loaded document
	// against at bind time; "" is none. The file that actually resolved is the one
	// validated, and an absent optional file passes vacuously.
	Schema string
	// As is "env" for a dotenv file read as environment variables (the `.env` shape): its
	// KEY=value lines feed env inputs and flag environment fallbacks under the real
	// environment, which wins variable by variable, and never configuration inputs. "" (or
	// "config") is a configuration file.
	As string
	// Profiles selects one named section of the file per run; nil when the file has none.
	Profiles *ProfilesDef
	// Keys is every key an input reads from this file, sorted by key, one entry per declaring
	// input: what [SetConfigValue] checks a value against, and what a key completer offers.
	Keys []ConfigKey
}

// ProfilesDef selects one named section of a [ConfigFile] per run. The section's keys are read
// as if they sat at the top of the file, winning over the file's other top-level keys, which
// every profile shares. The selection is the flag set on argv, then the first variable of Env
// that is set, then Default.
type ProfilesDef struct {
	Under   string // the top-level key holding the named sections
	Flag    string // logical name of the selector flag, searched across the chain; "" when none
	Env     string // variables read directly, comma-separated, the first one set winning
	Default string // the profile used when argv and the environment select none; "" means the shared keys only
}

// PathFromDef names the runtime inputs that supply a [ConfigFile]'s path — the declarative
// two-phase parse, where argv and env are read first and the file channel then opens whatever
// they pointed at. Precedence: the flag set on argv, then the first variable of Env that is set,
// then the flag's default, then the entry's own path or discover. A path supplied this way must
// exist. A path given on argv or in a variable has a leading ~ expanded (~user too, with
// [PathExpansion]) and is otherwise used as written, so $VAR stays literal; the flag's default
// is expanded like the entry's own path. The flag given an empty path is a usage error; a
// variable set to "" counts as unset.
type PathFromDef struct {
	Flag string // logical flag name searched across the resolved chain
	// Env is the variables read directly, comma-separated, the first one set winning. Generated
	// code lists the flag's own variables, then the env input's.
	Env string
}

// DiscoverDef locates a configuration file at run time. The strategy orders the directories
// searched for File; the first containing it wins, and a file found nowhere is absent.
type DiscoverDef struct {
	// Strategy is "walk-up" (the working directory up to the filesystem root), "xdg"
	// ($XDG_CONFIG_HOME/<app> when absolute, else ~/.config/<app>), "native" (the platform's
	// config directory: %AppData% on Windows, ~/Library/Application Support on macOS, else as
	// xdg) or "xdg-system" (each absolute directory of $XDG_CONFIG_DIRS; when it is unset or
	// empty, /etc/xdg, and nothing on Windows).
	Strategy string
	File     string // the file name looked for in each searched directory
	App      string // the application directory under the config root (xdg, native and xdg-system)
}
