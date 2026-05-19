package handlers

// helpKey identifies one of the per-command help strings. The enum is
// internal to the package; callers reach the strings via
// [getHelp] keyed by command path.
type helpKey int

const (
	helpKeyRoot helpKey = iota
	helpKeyCompletion
	helpKeyGenerate
	helpKeyHelp
	helpKeyInitialize
	helpKeyValidate
	helpKeyVersion
)

// getHelp returns the help string for key. Unknown keys fall back to
// the root help text — surprising callers with an empty string would
// be worse than echoing the program overview.
//
//nolint:funlen // body is a string-literal table, not control flow
func getHelp(key helpKey) string {
	strings := map[helpKey]string{
		helpKeyRoot: `The rotini cli framework companion cli.

Find more information at: https://rotini.dev

Usage:
  rotini <command> [flags]
         [-v | --version] [-h | --help]

Commands:
  initialize;init     initialize a cli program
  generate;gen        generate a cli program
  validate;val        validate a spec file
  completion          generate shell completion
  version             print version
  help                print help

Flags:
  -v,--version        print version
  -h,--help           print help

Examples:
  rotini init mycli
  rotini validate .rotini.spec.yaml
  rotini generate ./path/to/.rotini.spec.yaml

Use "rotini help <command>" for more information about a command.`,

		helpKeyCompletion: `Generate shell completion scripts.

Usage:
  rotini completion <shell> [-h | --help]

Arguments:
  shell         the shell to generate completions for (zsh, bash, fish, powershell, nushell, elvish)

Flags:
  -h,--help     print help

Examples:
  rotini completion bash
  rotini completion zsh >> ~/.zshrc (eval "$(rotini completion zsh)")
  rotini completion fish > ~/.config/fish/completions/rotini.fish
  rotini completion powershell | Out-String | Invoke-Expression
  rotini completion nushell | save -f ~/.config/nushell/rotini.nu
  rotini completion elvish | save -f ~/.config/elvish/lib/rotini.elv

Use "rotini help <command>" for more information about a command.`,

		helpKeyGenerate: `Generate a cli program from a rotini spec file.

Usage:
  rotini generate [./path/to/.rotini.spec.yaml] [-w | --watch] [-h | --help]

Arguments:
  file           path to the spec file (default: .rotini.spec.yaml)

Flags:
  -c,--config    path to the conf file (default: .rotini.conf.yaml next to spec)
  -w,--watch     watch a rotini spec file for changes and re-generate
  -h,--help      print help

Examples:
  rotini generate
  rotini generate ./path/to/.rotini.spec.yaml --watch

Use "rotini help <command>" for more information about a command.`,

		helpKeyHelp: `Print help for a specific command.

Usage:
  rotini help [command] [-h | --help]

Arguments:
  command       name of the command to print help for

Flags:
  -h,--help     print help

Examples:
  rotini help
  rotini help generate
  rotini help init

Use "rotini help <command>" for more information about a command.`,

		helpKeyInitialize: `Initialize a new rotini cli program spec file.

Usage:
  rotini initialize <name> [--format=yaml|json] [--force] [-h | --help]

Arguments:
  name          the root command name written to created spec file (expected binary name)

Flags:
  --format      specify the created rotini spec file format as yaml or json (default: yaml)
  --force       forces re-initialization if files exist that init would overwrite
  -h,--help     print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force

Use "rotini help <command>" for more information about a command.`,

		helpKeyValidate: `Validate a rotini spec file for correctness.

Usage:
  rotini validate [./path/to/.rotini.spec.yaml] [-h | --help]

Arguments:
  file          path to the spec file (default: .rotini.spec.yaml)

Flags:
  -h,--help     print help

Examples:
  rotini validate
  rotini val ./path/to/.rotini.spec.yaml

Use "rotini help <command>" for more information about a command.`,

		helpKeyVersion: `Print the rotini cli version.

Usage:
  rotini version [-h | --help]

Flags:
  -h,--help     print help

Examples:
  rotini version

Use "rotini help <command>" for more information about a command.`,
	}

	if s, ok := strings[key]; ok {
		return s
	}
	return strings[helpKeyRoot]
}

// Version is the rotini binary version. Overridden at build time via:
//
//	go build -ldflags "-X github.com/go-rotini/rotini/internal/handlers.Version=1.2.3"
//
// Defaults to "dev" when unset.
var Version = "dev"
