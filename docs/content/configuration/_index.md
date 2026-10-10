---
title: "configuration"
---

<!-- Code generated from the rotini JSON Schema; DO NOT EDIT.
     Edit the schema's descriptions, or the example in docs/assets/examples, then run:
       go test ./internal/codegen -run SchemaDocs -update-schema-docs -->

# .rotini.conf.yaml

The conf controls what `rotini generate` writes and where: the Go packages, the documentation and completion features, the JSON Schemas and the contract document. It also sets how `rotini validate` reports problems.

{{< code title=".rotini.conf.yaml — every key" language="yaml" file="examples/rotini.conf.yaml" open="true" copy="true" >}}{{< /code >}}

{{< code title="the JSON Schema" language="json" file="schemas/schema-conf.json" open="false" copy="true" >}}{{< /code >}}

Below, every key. `rotini validate` checks all of them before any code is generated, and
this list is rendered from the schema, so it always matches what the tool accepts.

## Document

### `version`

`string` · **required** · default `0.0.0`

The minimum rotini version this conf requires (X.Y.Z): the feature set it was written against, not an exact pin. Any rotini of the same major version at or beyond it accepts the document, so a patch or minor upgrade never requires an edit here. A rotini older than this, or a different major version, is an error. The check is skipped for a development build of rotini, which reports no release version (0.0.0, or none at all). Works the same as the spec's `version` key.

### `$schema`

`string`

Optional URI identifying the rotini conf schema, for editor tooling only: rotini never fetches it, and the version check reads the `version` key below. Any URI is accepted: a released schema (https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/v1.3.0/schema-conf.json — note the 'v', matching the git tag), a path written into your project by `generate.schemas.conf.file`, or a fork's own URL.

### `diff`

[`DiffConfig`](#diffconfig)

Controls `rotini diff`, which compares two versions of the cli's contract: the changes this release makes on purpose.

### `generate`

[`GenerateConfig`](#generateconfig)

Controls `rotini generate`: the generated packages and features. When omitted entirely, the defaults apply: one generated file under internal/cmd/&lt;root&gt;, and every feature off.

### `validate`

[`ValidateConfig`](#validateconfig)

Controls how `rotini validate` and `rotini generate` report problems (collect everything vs. fail fast), and which opt-in warnings they add.


## DiffConfig

Controls `rotini diff`. An acknowledgement belongs to one release, not to the interface, so it lives here rather than in the spec.

### `accept`

array of [`DiffAccept`](#diffaccept)

The changes this release makes on purpose. Each entry acknowledges one finding, by its exact rule and where: the finding is reported as accepted and doesn't fail the run. An entry that matches no finding fails the run, naming the entry, so the list doesn't outlive its release. There is no blanket ignore by rule.


## GenerateConfig

Controls `rotini generate`: where rotini's JSON Schemas are written ('schemas'), where the generated code is written ('packages'), and which derived doc/completion outputs are emitted ('features').

### `contract`

[`ContractConfig`](#contractconfig)

Optional: where to write the contract document, a JSON description of every command's inputs, output and exit statuses.

### `dry_run_env`

`string`

The name of an environment variable that makes `rotini generate` do a dry run: when it is set to 1, true, yes or on (any case), generate writes nothing, lists what it would change and exits 2 if anything would, or 0 if nothing would. Set it to CI to dry-run in most CI systems, which set CI=true, so `go generate ./...` checks every CLI in the module. --dry-run and --no-dry-run on the command line take precedence. Unset, the environment never changes what generate does. It doesn't apply to `rotini init`, which has no conf to read before it runs.

### `features`

array of [`Feature`](#feature)

The generated documentation and completion outputs, one per `type` (help, completion, man, markdown). Each is off unless enabled, with options for how its content is stored and rendered.

### `packages`

array of [`PackageConfig`](#packageconfig)

The generated code targets, one per `type` (main, cmd, models). 'main' is the program's entrypoint, created once. 'cmd' is the CLI package: the handler files you edit, plus the one generated file (the command tree, the handler wiring and, unless 'models' moves them out, the typed input and output structs). 'models' is optional: declare it to put the typed structs in their own package, which a handler package used through a command's `handler:` can import without an import cycle. The rotini runtime is not generated: it is an ordinary dependency the generated code imports (`go get github.com/go-rotini/rotini`).

### `schemas`

[`SchemasConfig`](#schemasconfig)

Optional: where to write rotini's conf and spec JSON Schemas into this project, so a document's `$schema:` key (which `rotini init` seeds) can point at a local copy instead of a URL.


## ValidateConfig

Controls how `rotini validate`, and the validation `rotini generate` runs first, report problems. The rules are fixed, apart from the opt-in `flags_first` and `posix_names` warnings. `rotini validate --fail` overrides `fail`.

### `fail`

`string` · one of `fast`, `collect` · default `collect`

fast = stop at and report the first problem; collect = run to completion and report every problem at once (default).

### `flags_first`

`boolean` · default `false`

Opt-in: warn about each env or config input that no flag can also set (through the flag's `variable:` or `key:`), so `--help` shows the whole configuration surface. Secret inputs are exempt, since a secret belongs in a file or the environment, not on the command line, and so are nested env inputs (`nesting:`), since no single flag can mirror a family of variables. Warnings never fail validation.

### `posix_names`

`boolean` · default `false`

Opt-in: warn when the root command's name, the program's name, isn't a POSIX utility name: 2 to 9 lowercase letters and digits. Sub-command names aren't checked. Warnings never fail validation.

### `release_env`

`string`

The name of an environment variable holding the release being prepared (X.Y.Z). When it is set, `rotini validate` fails for each command or input whose `removed_in` (or a `deprecated_identifiers_removed_in` entry) is at or below that release, so a planned removal is not forgotten. `rotini validate --release` takes precedence. Unset or empty, nothing is checked. Only `rotini validate` reads it: `rotini generate` never runs this check.


## DiffAccept

One acknowledged change: `{rule: COMMAND_NO_DELETE, where: "taskr compact", reason: "replaced by purge"}`.

### `rule`

`string` · **required**

The finding's rule ID, as `rotini diff` prints it.

### `where`

`string` · **required**

The finding's where, exactly as `rotini diff` prints it: the item in the old contract, written as on the command line.

### `reason`

`string` · **required**

Why the change is intended; shown beside the accepted finding.


## ContractConfig

Optional: write the contract document, one JSON file describing the whole CLI for scripts, tools and AI agents. Every command is listed, a hidden one marked `hidden: true`, with its arguments, flags (including inherited cascading flags), environment variables, configuration keys and stdin, each marked hidden where it is; a `parameters` JSON Schema combining its visible arguments and flags; its output shape where one is declared; and its exit statuses. The format is rotini's own, described by schema-contract.json in the rotini repository, and the shape of the error line rotini.StructuredReporter writes to stderr is included under `errors`. Validate a contract with the schema-contract.json from the rotini that wrote it: newer releases add fields, which an older copy of the schema rejects.

### `file`

`string`

Module-root-relative path (no leading slash) ending in '.json' the contract document is written to. Rewritten on every `generate`.

### `go`

`boolean` · default `false`

When true, the generated cmd file also holds the contract document as `var Contract string`, byte for byte what `file` holds, so the binary can print its own contract. Declare a command for it and print Contract from its handler (`fmt.Fprint(rtx.Stdout, Contract)`); rotini adds no command of its own. It adds the document's size to the binary. Works without `file`.


## Feature

One generated feature, chosen by 'type' (help, completion, man, markdown): an on/off switch ('enabled') plus two independent options. 'embed' chooses how the content is stored: a rendered file in 'embed_dir' loaded with //go:embed, or a string literal in the generated code. 'template' chooses how it is rendered: from an editable template seeded into 'template_dir', or from rotini's built-in one. The directories default to '&lt;cmd-package&gt;/renders' and '&lt;cmd-package&gt;/templates'. With embed on, embed_dir must be inside the cmd package, since //go:embed cannot reach outside it; template_dir may be anywhere. Output files never collide: help pages are 'help_\*.txt', man pages '&lt;page-name&gt;.&lt;section&gt;' (taskr-add.1), markdown pages 'markdown_\*.md' and completion scripts 'completion_&lt;shell&gt;.txt', and each feature removes only its own files.

### `type`

`string` · **required** · one of `help`, `completion`, `man`, `markdown`, `tools`, `skill`, `llms`, `permissions`, `carapace`, `config_example`, `env_example`

Which output this entry configures. help, man and markdown are per-command pages, rendered from the command's documentation fields in the spec through the template, or written verbatim when the command sets that page in the spec. Each generates a variable per page and a 'Help', 'Man' or 'Markdown(path ...string) (string, error)' function that returns the page for a command path. completion is different: one script per shell (bash, zsh, fish, powershell, nushell), generated from the program name, with no editable template and no verbatim form. It generates a 'Completion&lt;Shell&gt;' variable per shell and a 'Completion(shell string) (string, error)' function; the scripts call the program's hidden '__complete' command.

tools, skill, llms and permissions are files for AI agents and their tools, one set for the whole program, written under 'file' (relative to the module root) and never removed: tools writes tool definitions for each of 'targets' (tools/mcp.json, tools/openai.json, tools/gemini.json), skill an Agent Skills page (skills/&lt;name&gt;/SKILL.md), llms an llms.txt, and permissions permission rules for each of 'harnesses'. They add no Go code, except `go: true` on tools. skill and llms render from an editable template, as help does; tools and permissions are built from the contract and have none. 'embed' and 'embed_dir' don't apply to them.

carapace, config_example and env_example are files for the program's users, also written under 'file' and never removed. carapace writes a carapace-spec completion file (completions/carapace/&lt;name&gt;.yaml) from the spec's static facts: commands, flags, enum values and completion hints. config_example writes an example of each configuration file the spec declares (examples/config/&lt;name&gt;.example.&lt;ext&gt;), in the file's format, with every key commented out above its summary, type and default; env_example writes .env.example, every environment variable the program reads, commented out. config_example and env_example also generate 'ConfigExample(name string) (string, error)' and 'EnvExample() string', so a command can print them; 'embed' and 'embed_dir' choose how that text is stored, as for help. All three are built in Go, with no editable template.

### `base_url`

`string`

llms only: the address the markdown pages are published at. llms.txt links each command to &lt;base_url&gt;&lt;page&gt;.md (taskr-add.md). Without it, the links are relative to llms.txt and point at the markdown feature's files, which needs the markdown feature on with `embed: true`. Setting it on any other feature is an error.

### `description`

`string`

skill only: when an agent should use the skill, added after the root's summary in SKILL.md's `description` (at most 1024 characters together). Setting it on any other feature is an error.

### `descriptions_env`

`string`

completion only: the name of an environment variable your users can set to 0, false or off (any case) to hide the descriptions shown beside completion candidates, in every shell; unset or any other value leaves them on. Descriptions show by default: zsh, fish and PowerShell beside each candidate, and bash on the second TAB. It is listed in the root man page's ENVIRONMENT section, the contract document and the completion scripts' header. Program.WithCompletionDescriptions replaces this check with a rule of your own.

### `embed`

`boolean` · default `false`

How this feature's content is stored in the cmd package. true: the rendered content is written to a file under 'embed_dir' and loaded with a //go:embed directive. false (the default): no file is written, and the content is a string literal in the generated code, so the generated file is self-contained. The variable names and the lookup function are the same either way.

### `embed_dir`

`string`

Directory (relative to the module root) where this feature's rendered files (help_\*.txt, &lt;page-name&gt;.&lt;section&gt;, markdown_\*.md, completion_&lt;shell&gt;.txt) are written when embed is true, and loaded with //go:embed, so it must be inside the cmd package (//go:embed cannot reach outside it). With embed false no files are written and this is unused; rotini validation warns if you set it then. Defaults to '&lt;cmd-package&gt;/renders'.

### `enabled`

`boolean` · default `false`

When true, rotini generates this feature's outputs into the cmd package, with their variables and lookup function. Off by default.

### `file`

`string`

tools, skill, llms, permissions, carapace, config_example and env_example only: where the files are written, relative to the module root. A directory for tools (default 'tools/'), skill (default 'skills/', which gets &lt;name&gt;/SKILL.md), permissions (default 'agents/') and config_example (default 'examples/config/'); a file for llms (default 'llms.txt'), carapace (default 'completions/carapace/&lt;name&gt;.yaml', where &lt;name&gt; is the root command's name, which carapace requires as the file name) and env_example (default '.env.example'). Rewritten on every generate; turning the feature off leaves the files in place. Setting it on any other feature is an error.

### `go`

`boolean`

tools only: also put mcp.json in the generated cmd package as the string variable ToolsMCP, so the binary can serve itself over MCP (with the MCP companion module) without reading a file. Needs 'mcp' in 'targets'. Setting it on any other feature is an error.

### `harnesses`

array of `string`

permissions only: which agent harnesses to write permission rules for, from the commands' `effects`. 'claude' writes claude-settings.json, a fragment to merge into Claude Code's .claude/settings.json. 'codex' writes &lt;name&gt;.rules, Codex prefix rules. 'gemini' writes &lt;name&gt;-policy.toml, a Gemini CLI policy. Read commands are allowed and destructive ones ask first; write commands get no rule, so the harness asks as it does by default. Default: all three. Setting it on any other feature is an error.

### `install_dir`

`string`

completion and man only: a directory (relative to the module root) where `rotini generate` also writes the pages as files ready to package, named the way packages install them: completions/&lt;name&gt;.bash, completions/_&lt;name&gt; (zsh), completions/&lt;name&gt;.fish, completions/&lt;name&gt;.ps1 and completions/&lt;name&gt;.nu, and man/man&lt;section&gt;/&lt;page&gt;.&lt;section&gt;, where &lt;name&gt; is the root command's name. Each file holds what Completion(shell) or Man(path...) returns. Rewritten on every generate; a man page whose command or topic is gone is removed. Hidden commands get no man page. Works with embed on or off. Don't use GoReleaser's dist/ directory, which it deletes. Setting it on any other feature is an error.

### `mcp_revision`

`string` · one of `2026-07-28`, `2025-11-25`

tools only: the MCP revision mcp.json is written for. 2026-07-28 (the default) allows any JSON value as a tool's output; with 2025-11-25, which requires an object, an output that isn't one is described wrapped as {"result": …}, and the MCP companion wraps it the same way. Setting it on any other feature is an error.

### `messages`

`string` · one of `declared`, `all`

completion only: turns on completion messages, lines the shell shows while a value is being completed and there is nothing to offer. 'declared' shows the inputs' `complete.message` lines from the spec. 'all' also shows a line derived from the summary of every other flag and argument that has one, such as `--replicas <int>: how many instances`. Either way a completer can add its own with rtx.AddCompletionMessage, which take the place of the static line. Omitted, there are no messages. zsh and bash 4.4 or later show them; fish, PowerShell, Nushell and older bash skip them, and the plugin hosts kubectl, Docker and Flux show them their own way. Setting it on any other feature is an error.

### `messages_env`

`string`

completion only: the name of an environment variable your users can set to 0, false or off (any case) to hide completion messages; unset or any other value leaves them on. It is listed in the root man page's ENVIRONMENT section, the contract document and the completion scripts' header. Program.WithCompletionMessages replaces this check with a rule of your own. Requires `messages`.

### `name`

`string`

skill only: the skill's name, its directory under 'file' and its SKILL.md `name`: lowercase letters, digits and single hyphens, at most 64 characters. Defaults to the root command's name, lowercased, with '_' written '-'. Setting it on any other feature is an error.

### `section`

`integer` · default `1`

man only: the man page section the pages are generated for, a single digit 1-9 (default 1, user commands; 8 is administration tools and daemons). It is the section in each page's header, the extension of each page file (taskr-add.8), and the section in cross-references between pages, and the generated ManSection constant holds it. One value for the whole program. Setting it on any other feature is an error.

### `targets`

array of `string`

tools only: which tool-definition files to write. 'mcp' writes mcp.json, a Model Context Protocol tools/list result whose tools carry annotations from `effects` and the facts a server needs to run each one (the MCP companion module serves it). 'openai-strict' writes openai.json, OpenAI Responses API function tools in strict mode. 'gemini' writes gemini.json, Gemini function declarations. Default: mcp. Setting it on any other feature is an error.

### `template`

`boolean` · default `false`

Whether the editable template (help.txt.tmpl, man.txt.tmpl or markdown.md.tmpl) is seeded into 'template_dir' for you to customize. true: the template is written when missing, and pages render from it. false (the default): no template is written, and pages render from rotini's built-in one. Has no effect on completion, which has no template; rotini validation warns if you set it there. A template you have edited is never removed: setting this back to false leaves it in place, unused.

### `template_dir`

`string`

Directory (relative to the module root) where this feature's editable template (help.txt.tmpl, man.txt.tmpl or markdown.md.tmpl) is written when template is true. Templates are not embedded, so it may be anywhere. Unused when no template is seeded (template false, or completion, which has none); rotini validation warns if you set it then. Defaults to '&lt;cmd-package&gt;/templates'.


## PackageConfig

One generated code target, chosen by 'type'. 'file' is the module-root-relative path ending in '.go' that rotini writes; 'package' is the Go package name at its top; 'keep' lists package-relative paths rotini must never remove.

### `type`

`string` · **required** · one of `main`, `cmd`, `models`

Which code this target receives.

- main: the program's entrypoint (main.go), created once and never overwritten.
- cmd: the CLI package. Its directory holds the handler files you edit and the one generated file, which holds the typed input and output structs (what rtx.Inputs[T] and the per-source input methods fill), the command tree with NewProgram, ProgramHandlers and InputSettings, and the handler wiring (Handlers(), and Program, which is deprecated).
- models: only the typed input and output structs, in their own package. Optional: without it they live in the cmd file. Declare it when a command uses a handler from another package (`handler:`) and that package needs the input types: the cmd package imports the handler package, so the handler package cannot import cmd back, and with models both import it instead. The cmd package re-exports every model as a type alias, so handler code inside cmd is unaffected either way.

There is no 'runtime' target: the rotini runtime is imported from github.com/go-rotini/rotini, not generated.

### `file`

`string`

Module-root-relative path (no leading slash) ending in '.go' for the file rotini writes for this target. Its parent directory is the package directory. 'cmd' defaults to 'internal/cmd/&lt;root-command&gt;/zz_rotini.go'; 'main' has no default and is written only when 'file' is set.

### `header`

`string`

Text written at the very top of every Go file this target produces (the generated file, the handler files and the entrypoint), above rotini's own 'Code generated by rotini' line. It is written as is, so write complete comment lines yourself (each starting with '//' or wrapped in /\* \*/); a build constraint needs a blank line after it, as Go requires. Use it for a license or copyright header your repository requires on every .go file, or for a '//go:build' constraint. It is applied to the generated file on every run, and to a file created once (a handler file or the entrypoint) only when that file is first written, so editing the header later does not rewrite a file you already own.

### `keep`

array of `string`

Package-relative paths (e.g. 'helpers.go') that rotini must never remove.

You rarely need it: rotini only ever removes files it wrote that no longer match the spec, such as a handler file whose command has left the spec (identified by the generated marker it carries), and it reports each one. A handler file goes in two steps: the next generate disables it with `//go:build ignore`, and the one after deletes it. A file you wrote is never removed, whatever it is named, and neither are test files or the editable feature templates. Use 'keep' when you have kept a handler file whose command is gone and left its marker in place, or to protect a rendered output file under an embed_dir.

### `package`

`string`

Go package name written at the top of 'file'. Defaults to the file's parent-directory name (sanitized to a valid Go identifier). For type 'main' it must be 'main'. Targets that resolve to the same 'file' must declare the same 'package'.


## SchemasConfig

Where to write rotini's JSON Schemas into this project. Each entry is optional: declare 'conf' and/or 'spec' with a 'file' to have `rotini generate` write that schema there, overwriting it on every run. The files are never removed. Point a document's `$schema:` key at them.

### `conf`

[`SchemaConfig`](#schemaconfig)

Where to write rotini's conf-schema (the schema for this .rotini.conf file).

### `config`

[`ConfigSchemasConfig`](#configschemasconfig)

Where to write one JSON Schema per configuration file the spec declares, describing the keys your users may write in it, for completion and checking in their editor (`# yaml-language-server: $schema=…` in YAML, `#:schema …` in TOML, a `$schema` key in JSON).

### `output`

[`OutputSchemasConfig`](#outputschemasconfig)

Where to write one JSON Schema per command output declared in the spec, so scripts and other tools can validate what a command writes.

### `spec`

[`SchemaConfig`](#schemaconfig)

Where to write rotini's spec-schema (the schema for .rotini.spec files).


## SchemaConfig

A single schema write target: the project-relative path the embedded JSON Schema is written to.

### `file`

`string` · **required**

Module-root-relative path (no leading slash) ending in '.json' where the JSON Schema is written. Overwritten on every `generate`, and never removed. Point a document's `$schema:` key at it for completion and validation in your editor.


## ConfigSchemasConfig

A directory of JSON Schemas, '&lt;page-name&gt;.&lt;file-name&gt;.config.json' for each config_files entry (taskr.user.config.json for a file 'user' declared on the root, taskr-deploy.user.config.json for one declared on deploy). A schema lists every key a command that reads the file binds: config inputs and flags' config keys, with their types, enums and value summaries, defaults (never a secret's), bounds and deprecations. It doesn't forbid other keys, which rotini ignores. A file's own declared `schema` is included under allOf. dotenv files and files read as environment variables get none. Rewritten on every generate; a '\*.config.json' file in the directory that no entry produces any more is removed.

### `dir`

`string` · **required**

Module-root-relative directory (no leading slash) the config file schemas are written to.


## OutputSchemasConfig

A directory of JSON Schemas, one per declared output: '&lt;page-name&gt;.output.json' for a command's `output:` (taskr-list.output.json) and '&lt;page-name&gt;.exit-&lt;code&gt;.output.json' for an `exit_status` entry's `output:`. A schema is standard JSON Schema (draft-07): rotini's type names are written as JSON Schema types, and the spec's named schemas it references are included as definitions. Hidden commands get none. Rewritten on every `generate`; a '\*.output.json' file in the directory that no output produces any more is removed.

### `dir`

`string` · **required**

Module-root-relative directory (no leading slash) the output schemas are written to.

