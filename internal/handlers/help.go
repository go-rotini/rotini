package handlers

type helpKey int

const (
	helpKeyRotini helpKey = iota
	helpKeyRotiniCompletion
	helpKeyRotiniGenerate
	helpKeyRotiniHelp
	helpKeyRotiniInitialize
	helpKeyRotiniValidate
	helpKeyRotiniVersion
)

func getRotiniHelp(key helpKey) string {
	var helpStrings = map[helpKey]string{
		helpKeyRotini: `The rotini cli framework companion cli.

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
  rotini validate .rotini.yaml
  rotini generate ./path/to/.rotini.json

Use "rotini help <command>" for more information about a command.`,
		helpKeyRotiniCompletion: `Generate shell completion scripts.

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
		helpKeyRotiniGenerate: `Generate a cli program from a rotini spec file.

Usage:
  rotini generate [./path/to/.rotini.yaml] [-w | --watch] [-h | --help]

Arguments:
  file           path to the spec file (default: .rotini.yaml)

Flags:
  -w,--watch     watch a rotini spec file for changes and re-generate
  -h,--help      print help

Examples:
  rotini generate
  rotini generate ./path/to/.rotini.json --watch

Use "rotini help <command>" for more information about a command.`,
		helpKeyRotiniHelp: `Print help for a specific command.

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
		helpKeyRotiniInitialize: `Initialize a new rotini cli program spec file.

Usage:
  rotini initialize <name> [--format=yaml|json] [--force] [-h | --help]

Arguments:
  name          the root command name written to created spec file (expected binary name)

Flags:
  --format      specifiy the created rotini spec file format as yaml or json (default: yaml)
  --force       forces re-initialization if files exist that init would overwrite
  -h,--help     print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force

Use "rotini help <command>" for more information about a command.`,
		helpKeyRotiniValidate: `Validate a rotini spec file for correctness.

Usage:
  rotini validate [./path/to/.rotini.yaml] [-h | --help]

Arguments:
  file          path to the spec file (default: .rotini.yaml)

Flags:
  -h,--help     print help

Examples:
  rotini validate
  rotini val ./path/to/.rotini.yaml

Use "rotini help <command>" for more information about a command.`,
		helpKeyRotiniVersion: `Print the rotini cli version.

Usage:
  rotini version [-h | --help]

Flags:
  -h,--help     print help

Examples:
  rotini version

Use "rotini help <command>" for more information about a command.`,
	}

	if helpString, ok := helpStrings[key]; ok {
		return helpString
	}

	return helpStrings[helpKeyRotini]
}
