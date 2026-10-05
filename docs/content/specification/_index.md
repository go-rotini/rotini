---
title: "specification"
---

<!-- Code generated from the rotini JSON Schema; DO NOT EDIT.
     Edit the schema's descriptions, or the example in docs/assets/examples, then run:
       go test ./internal/codegen -run SchemaDocs -update-schema-docs -->

# .rotini.spec.yaml

The spec describes your CLI: its commands, their inputs and what they write. The document holds a top-level `version` and one root `command` (the program itself), and every sub-command below it has the same shape. `env_prefix` and `schemas` are valid only on the root command, and `$schema` is an optional key for editor tooling.

{{< code title=".rotini.spec.yaml — every key" language="yaml" file="examples/rotini.spec.yaml" open="true" copy="true" >}}{{< /code >}}

{{< code title="the JSON Schema" language="json" file="schemas/schema-spec.json" open="false" copy="true" >}}{{< /code >}}

Below, every key. `rotini validate` checks all of them before any code is generated, and
this list is rendered from the schema, so it always matches what the tool accepts.

## Document

### `version`

`string` · **required**

The minimum rotini version this spec requires (X.Y.Z): the feature set it was written against, not an exact pin. Any rotini of the same major version at or beyond it accepts the document, so a patch or minor upgrade never requires an edit here. Two cases are errors: a rotini older than this, which may not know keys the spec uses, and a different major version. The check is skipped for a development build of rotini, which reports no release version (0.0.0, or none at all). This key, not the optional `$schema` URL, is what the check reads.

### `command`

[`Command`](#command) · **required**

The CLI's root command (the binary itself): its name, doc-fields, inputs (flags/arguments/env/config/config_files/stdin) and sub-commands. The root must use 'name' (not '$ref'). The root-command-level keys `env_prefix` and `schemas` live here.

### `$schema`

`string`

Optional URI identifying the rotini spec schema, for editor tooling only: rotini never fetches it, and the version check reads the top-level `version` key, not this. Any URI is accepted: a released schema (https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/v1.2.0/schema-spec.json — note the 'v', matching the git tag), a path written into your project by the conf's `generate.schemas.spec.file`, or a fork's own URL. A relative path is resolved by your editor, not by rotini. `rotini init` seeds this key (`$schema: ./.rotini-schema.spec.json`), and it works in every format; YAML editors also accept a `# yaml-language-server: $schema=<path>` comment in its place.


## Command

A command node in the CLI command tree — the root command (under the document's 'command' key) and every sub-command share this recursive shape. Declared inline (with 'name') or composed from another spec file (with '$ref'). The root must use 'name' (not '$ref'). The two root-command-level keys (env_prefix, schemas) are accepted on this shape but are valid only on the root command — rotini validation rejects them on a sub-command.

### Identity and visibility

#### `name`

`string`

Command name used in routing. As the root command (the document itself) this is the binary name and must be set — the root cannot use '$ref'.

#### `aliases`

array of `string`

Additional names that invoke this command. Command aliases affect dispatch routing; use identifiers on flags for flag aliases. Sub-commands only: the root command is reached by invoking the binary (argv[0] is not a routing token), so rotini validation rejects aliases there.

#### `hidden`

`boolean` · default `false`

When true, the command is omitted from its parent's generated Commands list (it still dispatches on the command line).

#### `deprecated`

`string`

Deprecation message. The command is annotated as deprecated in its parent's generated Commands list, and invoking it is reported at run time by rotini.Deprecations with this message (a data feed for the handler; rotini itself prints nothing). With `deprecated_identifiers`, only those aliases report; without, every name does.

#### `deprecated_identifiers`

array of `string`

Aliases of this command that are deprecated (a subset of 'aliases'). When the command is invoked via one of these, rotini's Deprecations surfaces it for the handler to act on; invoking via the name or a non-listed alias is unaffected. Sub-commands only, like 'aliases' — rejected on the root by rotini validation.

### Inputs

#### `flags`

array of [`FlagInput`](#flaginput)

Flag inputs for this command

#### `arguments`

array of [`ArgumentInput`](#argumentinput)

Positional argument inputs for this command

#### `env`

array of [`EnvInput`](#envinput)

Environment-variable inputs for this command

#### `config`

array of [`ConfigInput`](#configinput)

Config-value inputs for this command, bound by key from an in-scope config_files source (declared on this command or any ancestor — see config_files).

#### `stdin`

[`StdinSpec`](#stdinspec)

Declares expected stdin format and schema for this command

#### `config_files`

array of [`ConfigurationFile`](#configurationfile)

Configuration files this command reads values from, each at a fixed 'path' or found with 'discover'. Files cascade: a command can read from the files declared on it and on every command above it, so a 'config' input here or on any sub-command may pin itself (schema 'file:') to a file declared here or on any ancestor. Only the files along the invoked command's path are loaded; files declared on other branches are never read. File names must be unique along a path (a collision is an error), and declaring the same physical file at two levels is a warning. When two files in scope define the same key, the one declared nearest the invoked command wins, and within one command's list the first declared wins, so list the more specific file (a project's) before the more general one (the user's). In a composed CLI, a `$ref`'d child's files travel with its commands, so `parent child cmd` reads what `child cmd` reads without the parent declaring anything.

#### `env_prefix`

`string`

Root only: a prefix for every environment-variable name rotini derives. Derived names are the UPPER_SNAKE forms of plain env inputs without 'variable:' (input 'home' → ACME_HOME), of nested env families without 'variable:' (the family's base name), and of flags' environment fallbacks (key 'server.port' → ACME_SERVER_PORT). A name set explicitly with 'variable:' is used exactly as written and is never prefixed. With a prefix declared, an unprefixed name no longer binds: input 'home' reads ACME_HOME, not HOME. Write it in UPPER_SNAKE with no trailing underscore (rotini adds the '_'). The derived name is written into the generated field's `env:` tag when you generate, so the name is fixed in the code. Help lists env: inputs by name under its Environment section; a flag's environment fallback is not listed, so mention the variable in the flag's summary when users need to know it.

In a composed CLI, a `$ref`'d child's env_prefix travels with its commands: a parent that declares none adopts the child's, a parent that declares one wins, and two children with different prefixes are rejected.

#### `flag_groups`

array of [`FlagGroup`](#flaggroup)

Cross-flag presence rules validated at parse time (e.g. mutually exclusive output formats, a required-together credential pair).

#### `flag_dependencies`

array of [`FlagDependency`](#flagdependency)

Conditional cross-flag requirements validated at parse time: when one flag is set, others become required (e.g. when --tls is set, --cert and --key are required).

### Sub-commands and composition

#### `commands`

array of [`Command`](#command)

Sub-commands of this command, declared inline or composed with $ref. On a $ref command these are added to the composed child's own sub-commands (see '$ref'); a name or alias that collides across the combined list is an error.

#### `$ref`

`string`

The spec file whose root command is mounted here as this sub-command. Not valid on the root command. Two forms are accepted:

- A relative path, resolved against this spec file's directory (`$ref: ../db/.rotini.spec.yaml`).
- `mod://<module>@<version>/<path>`, a spec inside another Go module (`$ref: mod://github.com/acme/db@v1.4.0/cmd/db/.rotini.spec.yaml`). The version is required, and &lt;path&gt; is the spec file's path inside that module. The module is read from the module cache with `go mod download`, so it is verified against go.sum; add it to go.mod first (`go get github.com/acme/db@v1.4.0`). A relative $ref inside that spec resolves within the same module and cannot leave it.

Git and https URLs are not accepted. Either way, the mounted command uses the handlers of the composed spec's own generated package unless `handler:` names another.

The composed spec is the base, and the parent can adjust it where it is mounted:

- Identity and presentation keys declared next to the $ref (name, aliases, summary, description, usage, header, footer, examples, headings, help, man, markdown, exit_status, see_also, group, hidden, deprecated, deprecated_identifiers, filename, plugin_path) replace the child's, for that one mounted command only. The child's own sub-commands keep theirs, so a parent can tailor the child for its tree without forking it.
- A 'commands:' list next to the $ref is added to the child's own sub-commands: its inline entries get their own handler files, and its $ref entries are mounted as further children.
- `handler:` on a $ref command points it at a different handler package.
- Keys the handler depends on (flags, arguments, env, config, config_files, stdin, flag_groups, flag_dependencies, output, plugins, plugin_discovery, passthrough) cannot be changed here: the mounted command runs the child's handler, built against the child's own inputs and output, so validation rejects them. Declare them in the child spec.

The child's own plugins, plugin_discovery and passthrough travel with it. `rotini validate` and `generate` on the parent also check every spec composed by a relative path as its own document, reporting problems at their position in that file. A spec composed with mod:// is not checked that way: it belongs to its own module, which validates it.

#### `handler`

[`HandlerSource`](#handlersource)

Use a handler from another Go package for this command instead of a generated handler file. Valid on any sub-command, not the root.

On a '$ref' command it replaces the default: a composed local or mod:// spec normally uses the handlers of its own generated package, and this points the command at a different package. On an inline command, the command's tree and typed inputs are still generated here, but its handler comes from the package and no handler file is written. It applies to this command only: an inline sub-command without its own 'handler:' still gets a generated handler file.

The package must export a constructor '&lt;convention&gt;() rotini.Handler' for each command it serves (the usual five-hook handler; hooks it does not implement default to no-ops), and the generated code calls 'pkg.&lt;Convention&gt;()'. The compiler enforces this, since rotini cannot type-check another package.

#### `passthrough`

`boolean`

When true, every token after this command's own name binds as a raw positional — no flag parsing, no unknown-flag errors, no '--' needed (the wrapper-CLI case: `mytool exec ls -la` forwards '-la' verbatim, and a literal '--' passes through too). Tokens BEFORE the command (ancestor flags) parse normally. A passthrough command declares no flags, no sub-commands, no declared plugins or discovery, and its last argument must be a variadic '[]string' — the receiver of the raw tokens (validation enforces all of this). Shell completion offers nothing past the boundary, falling back to file completion.

#### `plugins`

array of [`PluginSpec`](#pluginspec)

Declared plugins: separate executables dispatched as first-class sub-commands of this command.

#### `plugin_discovery`

[`PluginDiscovery`](#plugindiscovery)

Auto-expose external '&lt;prefix&gt;\*' executables as plugin sub-commands of this command (kubectl/git/gh plugin discovery), in addition to any declared plugins. Presence enables discovery.

#### `plugin_path`

`string`

Extra directory to search for this command's plugin binaries, in addition to the host binary's own directory and PATH. Relative to the working directory at run time; a leading ~ and $VAR references are expanded when the program runs, and a directory that does not exist yet is simply empty. On a `$ref` node it overrides the composed child's own. It applies to BOTH kinds of plugin: the 'plugins' this spec declares and anything 'plugin_discovery' finds — they are the same binaries in the same place, so they are configured once here rather than per-mechanism. Without it, a declared plugin could only ever be installed next to the host binary or on PATH, which is the git/kubectl convention and not always the right one for a vendored or bundled plugin. Search order is fixed and the same for both: next to the host binary, then this directory, then PATH — so a plugin shipped beside the binary always wins over one found here, and a failure names the locations it actually searched.

#### `timeout`

`string`

Not supported on a local command and rejected by rotini validation: a timeout is a plugin-only, host-side bound on a dispatched binary, so it has no effect on local execution. Set it on a plugins[] entry's 'timeout' instead. (Recognized here only so validation can give that targeted error rather than a generic 'unknown property'.)

### Documentation

#### `summary`

`string`

Short one-liner describing this command. It is shown next to the command in its parent's generated Commands list (so it applies even when a verbatim 'help' string is set), and it is also the NAME line of the man page, the opening line of the markdown page, and the lead of the command's own help page when no 'description' is set.

#### `description`

`string`

Long description block shown atop this command's generated help page. Ignored when 'help' (verbatim) is set.

#### `usage`

`string`

Usage-line override. When omitted, rotini derives one from the command's shape. Ignored when 'help' is set.

#### `display_name`

`string`

Root only: the name the generated help, man and markdown pages show for the program, in place of the root's 'name'. For a plugin, which a host runs as `<host>-<name>` but the user types as `<host> <name>`: with name: kubectl-ctx and display_name: "kubectl ctx", every derived usage line reads `kubectl ctx use <name> [flags]`, the man page's SYNOPSIS `kubectl ctx use …` and the markdown title `# kubectl ctx use`. It may contain spaces. It changes presentation only: 'name' still matches the binary and names the generated page files and man pages (kubectl-ctx-use.1), completion scripts still register for 'name' (the shell completes the binary), and routing, handler names and Context.CommandPath() are unaffected. Text the author writes verbatim (usage, footer, examples, a verbatim help/man/markdown page) is not rewritten. A composed child's display_name is ignored; the composing parent's root decides.

#### `examples`

array of `string`

Example command-line invocations, rendered one per line. Ignored when 'help' is set.

#### `exit_status`

array of [`ExitStatusEntry`](#exitstatusentry)

Exit codes this command documents, rendered as an EXIT STATUS section in the man and markdown pages. This is documentation only, and rotini does not check it: the runtime sets no exit code of its own except two. A recorded error or a recovered panic exits 1 when no handler set a code, and the default signal handling exits 128+n on signal n (130 for Ctrl-C). So a command that documents `2: invalid input` here and only calls rtx.RecordError will exit 1. Set the code in the handler with rtx.Exit or rtx.HaltWithCode to make the program agree with this section. Ignored when 'man' (verbatim) is set.

#### `see_also`

array of `string`

Cross-references rendered as a SEE ALSO section in the man page (e.g. related commands or man pages like 'rotini-generate(1)', or URLs). Ignored when 'man' (verbatim) is set.

#### `group`

`string`

Group label for organizing this command under a heading in its parent's generated Commands list. Commands sharing a group are bucketed together; groups appear in the order their first member is declared. Ungrouped commands fall under the default Commands heading. Presentation-only.

#### `header`

`string`

Text rendered above the description block. Ignored when 'help' is set.

#### `footer`

`string`

Text rendered at the bottom of the page. Ignored when 'help' is set.

#### `headings`

[`HelpHeadings`](#helpheadings)

Section heading overrides for the generated page; sane defaults fill any unset heading. Ignored when 'help' is set.

#### `help`

`string`

Exact, verbatim help page for this command. When set, rotini writes it byte-for-byte (no rendering; terminal styling kept) and ignores the structured help fields (description/usage/header/footer/examples/headings); 'summary' is still used in the parent's Commands list. When unset, rotini generates the page from the structured fields.

#### `man`

`string`

Exact, verbatim man page for this command, written in roff, the markup the man program reads (the man feature's per-command escape, mirroring 'help'). When set, rotini writes it as given — byte-for-byte except that ANSI styling is removed, since a man page carries none — and ignores the structured doc-fields for the man page; when unset, the page is rendered as roff from those fields through the man template. Either way the page is named after the command path joined with '-' and lowercased, with the man section as its extension (deploy-status.1).

#### `markdown`

`string`

Exact, verbatim markdown reference page for this command (the markdown feature's per-command escape, mirroring 'help'/'man'). When set, rotini writes it as given — byte-for-byte except that ANSI styling is removed; when unset, the page is rendered from the structured doc-fields through the markdown template.

### Output and shared types

#### `output`

[`Schema`](#schema)

The shape of what this command writes to stdout when it succeeds, as a schema. Rotini generates a typed '&lt;Prefix&gt;Output' Go type (when the shape is a '$ref' to a document-level schema, a new named type defined on that schema's type, such as 'type &lt;Prefix&gt;Output Task', so convert a value with &lt;Prefix&gt;Output(v)), documents the shape in an OUTPUT section of the help, man and markdown pages, and describes it in the output schema files and the contract document. It describes the shape only: it adds no flag and wires no format. How the output is written, and in which format, is the handler's own code — rtx.WriteOutput is an optional helper that writes json, yaml or toml, hands any other format to a renderer, and checks the value is this type. A command that writes a stream of items declares the shape of one item.

#### `schemas`

`object`

Document-level (root only): reusable named schema definitions. Referenced elsewhere by name, `$ref: <Name>`, or as a pointer, `$ref: "#/schemas/<Name>"`. Each may carry a `description`, which becomes the generated type's doc comment. Names must be PascalCase Go-exportable identifiers — each becomes a generated Go type in the cmd package (or in the models package, when the conf declares one), which other packages may import.

### Generated code

#### `filename`

`string`

Override the name of this command's generated handler-stub .go file (in the cli package). Defaults to a name derived from the command path ('&lt;root&gt;_&lt;path&gt;.go', every '-' written '_': config_get_contexts.go), reserved-name-escaped so a command named 'test'/'&lt;GOOS&gt;'/'&lt;GOARCH&gt;' does not collide with Go's filename rules. Must end in '.go', must not itself be a name Go reads specially ('_test.go', '_&lt;GOOS&gt;.go', '_&lt;GOARCH&gt;.go'), and must be unique among the commands generated into the same package. Renaming it orphans (and prunes) the previous stub file — move your handler code first.


## FlagInput

### `name`

`string` · **required**

Logical name for the flag

### `cascading`

`boolean` · default `false`

When true, this flag is advertised in the generated help of every descendant command (under the 'Global Flags' section), not only on its own command. Display-only: at run time a flag may be written anywhere after the name of the command that declares it — after its sub-commands' names too — regardless of this setting, but never before that name (a flag written before a sub-command's name belongs to an ancestor, which is how two commands may declare the same flag). cascading controls whether descendants document it.

### `deprecated`

`string`

Deprecation message. The flag is annotated as deprecated in generated help, and using it is reported at run time by rotini.Deprecations with this message (a data feed for the handler; rotini itself prints nothing). With `deprecated_identifiers`, only those spellings report — the others are the ones to move to (`identifiers: [--db, --database]`, `deprecated_identifiers: [--database]`, `deprecated: use --db`); without, the whole flag is deprecated and every spelling reports.

### `deprecated_identifiers`

array of `string`

CLI tokens for this input that are deprecated — a subset of its identifiers (flags) or aliases (commands). When one of these is used on the command line, rotini's Deprecations surfaces it as a data point for the handler to act on (warn, emit telemetry, etc.); the framework itself does nothing. Tokens not listed here are unaffected. Pair it with `deprecated:` to give the report a message; `deprecated:` alone deprecates every spelling

### `group`

`string`

Group label that puts this flag under its own heading in generated help, the way a command's 'group' does in the Commands list: flags sharing a group appear together, groups appear in the order their first member is declared, and ungrouped flags fall under the default Flags heading.

Presentation only: parsing, precedence and the generated field are unchanged. Use it on a command with many flags, so its help page is easy to scan. Not to be confused with 'flag_groups', which validates combinations of flags.

### `hidden`

`boolean` · default `false`

When true, the flag is omitted from generated help (it still parses on the command line).

### `identifiers`

array of `string`

CLI flag identifiers (e.g., '--force', '-f'). When absent, '--&lt;name&gt;' is derived from the flag's name, with '_' written as '-' ('dry_run' → --dry-run).

### `schema`

[`InputSchema`](#inputschema)

Type definition and input-level metadata (type, required, default, enum, nullable, constraints)

### `summary`

`string`

Short one-liner shown next to this flag in the Flags section of generated help.


## ArgumentInput

### `name`

`string` · **required**

Logical name for the argument

### `deprecated`

`string`

Deprecation message. The argument is annotated as deprecated in generated help, and supplying it is reported at run time by rotini.Deprecations with this message (a data feed for the handler; rotini itself prints nothing).

### `hidden`

`boolean` · default `false`

When true, the argument is omitted from generated help (it still parses on the command line).

### `schema`

[`InputSchema`](#inputschema)

Type definition and input-level metadata (type, required, default, enum, nullable, constraints)

### `summary`

`string`

Short one-liner shown next to this argument in the Arguments section of generated help.


## EnvInput

### `name`

`string` · **required**

Logical name for this env var input

### `deprecated`

`string`

Deprecation message; the input is annotated as deprecated in generated help. (Run-time deprecation reporting covers what argv carries — commands, flags and arguments.)

### `hidden`

`boolean` · default `false`

When true, the input is omitted from generated help (it is still bound).

### `schema`

[`InputSchema`](#inputschema)

Type definition and input-level metadata (required, default, variable)

### `summary`

`string`

Short one-liner shown next to this input in the generated Environment/Configuration help section.


## ConfigInput

### `name`

`string` · **required**

Logical name for this config value

### `deprecated`

`string`

Deprecation message; the input is annotated as deprecated in generated help. (Run-time deprecation reporting covers what argv carries — commands, flags and arguments.)

### `hidden`

`boolean` · default `false`

When true, the input is omitted from generated help (it is still bound).

### `schema`

[`InputSchema`](#inputschema)

Type definition and input-level metadata (required, default, file, key)

### `summary`

`string`

Short one-liner shown next to this input in the generated Environment/Configuration help section.


## StdinSpec

### `format`

`string` · one of `json`, `yaml`, `jsonc`, `toml`, `text`, `lines` · default `json`

How the piped stdin payload is read.

The four document formats (json, yaml, jsonc, toml) decode it into the generated &lt;Prefix&gt;Stdin struct, validated against the declared schema. The default is json.

The two raw formats are for commands whose stdin is not a document, such as text filters: 'text' binds the whole payload as a single string, and 'lines' binds it as []string split on newlines (a trailing newline adds no empty element). The schema's type must match ('string' for text, '[]string' or 'array' for lines), and neither generates a &lt;Prefix&gt;Stdin struct, because there is nothing to shape. Declaring stdin this way, rather than reading rtx.Stdin directly, puts it in the command's help page and completion.

### `schema`

[`InputSchema`](#inputschema)

Type definition for stdin content. Set required: true in schema to error when stdin is empty.


## ConfigurationFile

### `name`

`string` · **required**

Logical name for the config file (e.g. 'app-config'). It anchors per-input pins (schema 'file:') and config_source claims, so it must be unique within its chain (this command and its ancestors) — a name collision in scope is an error.

### `discover`

[`ConfigurationFileDiscover`](#configurationfilediscover)

Locate this file at run time instead of a fixed 'path'. Exactly one of 'path' or 'discover' must be set.

### `format`

`string` · one of `json`, `yaml`, `toml`, `jsonc`, `dotenv`

The file's format. When omitted, the format is inferred from the file extension; declare it when the extension is missing or misleading. 'jsonc' is JSON with comments and trailing commas. 'dotenv' reads KEY=value lines whose keys are used as written: a config input reading one declares `key: API_ENDPOINT`, not a dotted path.

### `path`

`string`

File path (supports ~ for home dir). Exactly one of 'path' or 'discover' must be set.

### `schema`

[`Schema`](#schema)

Optional load-time validation: the loaded document is validated against this schema at bind time, before any value is read from it — a non-conforming file is a loud error naming the file and the violation (the same gate the stdin channel applies to its payload). The file that actually resolved — fixed path, discovered, or config_source-supplied — is the file validated; an absent file passes vacuously (absence is the per-input required's concern). Document-level named schemas resolve via "$ref": "#/schemas/&lt;Name&gt;". No typed struct is generated from this — typed access to config values is the config: inputs channel.


## FlagGroup

A constraint on which of this command's flags may (or must) be set together. 'flags' references flag logical names; 'set' means explicitly provided on the command line (a default or env/config fallback does not count).

### `kind`

`string` · **required** · one of `mutually_exclusive`, `required_together`, `one_of`, `at_least_one`

mutually_exclusive: at most one set. required_together: all or none. one_of: exactly one. at_least_one: one or more.

### `flags`

array of `string` · **required**

The logical flag names the rule covers (at least two). Each must be a flag declared on the same command — validation rejects unknown names. "Set" means explicitly set on argv: defaults and env/config fallbacks neither trip nor satisfy a group.


## FlagDependency

A conditional requirement: when the 'when' flag is explicitly set on the command line, every flag in 'requires' must also be set. Both reference flag logical names; 'set' means explicitly provided (a default or env/config fallback does not count).

### `when`

`string` · **required**

The flag whose presence triggers the requirement.

### `requires`

array of `string` · **required**

Flags that must also be set when 'when' is set.


## HandlerSource

Where a command's handlers come from when they are not a generated stub: a Go package (handler delegation). The package's typed inputs live with it; this spec contributes only the command tree.

### `import`

`string` · **required**

Go import path of the handler package, in the same 'alias path' form an input type's 'import' uses (e.g. 'deployhandlers github.com/acme/clis/deploy/handlers'); identical imports are merged. A bare path takes its alias from the last path segment. The generated code calls '&lt;alias&gt;.&lt;convention&gt;()'.

### `convention`

`string` · **required**

Function-name prefix the package exports per command: codegen delegates this command to '&lt;alias&gt;.&lt;convention&gt;()' and each sub-command to '&lt;alias&gt;.&lt;convention&gt;&lt;SubPath&gt;()', each returning a rotini.Handler. PascalCase Go-exportable identifier.


## PluginSpec

### `name`

`string` · **required**

Name of the declared plugin. The dispatched binary is named &lt;program&gt;-&lt;name&gt;, and is searched for next to the host binary, then in the command's plugin_path, then on PATH. Inside a $ref-composed subtree &lt;program&gt; is the composed spec's own name, so one installed plugin serves both that spec's own binary and a parent that composes it.

### `aliases`

array of `string`

Additional names that invoke this plugin.

### `summary`

`string`

Short one-liner shown next to this plugin in its parent's generated Commands list.

### `timeout`

`string`

Host-side timeout for running the plugin. Uses Go duration format (e.g. "10s", "1m30s"). Empty or omitted means no timeout.


## PluginDiscovery

Auto-expose external '&lt;prefix&gt;\*' executables as plugin sub-commands (kubectl/git/gh plugin style), alongside any declared plugins. Presence enables discovery; a discovered name that collides with a declared command or plugin is skipped.

### `hidden`

`boolean` · default `false`

When true, discovered plugins still dispatch but are omitted from completion listings.

### `prefix`

`string`

Executable-name prefix to discover. Default: the host binary name followed by '-' (e.g. 'acme-').


## ExitStatusEntry

### `code`

`integer` · **required**

The exit status code being documented (0-255 — the range a process can actually return).

### `output`

[`Schema`](#schema)

The shape stdout still carries when the command exits with this code, for an outcome that is not plain success but prints data anyway (`3: some tasks failed; stdout lists what succeeded`). Documented in the EXIT STATUS section and described in the output schema files and the contract document.

### `summary`

`string`

What this exit code means.


## HelpHeadings

Section heading overrides for generated help pages.

### `arguments`

`string`

Heading rendered above the arguments section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Arguments:".

### `cascading`

`string`

Heading rendered above the cascading-flags section on descendant commands' generated help pages. Rendered verbatim — include any trailing ':' you want. Default: "Global Flags:".

### `commands`

`string`

Heading rendered above the commands section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Commands:".

### `configuration`

`string`

Heading rendered above the configuration section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Configuration:".

### `environment`

`string`

Heading rendered above the environment section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Environment:".

### `examples`

`string`

Heading rendered above the examples section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Examples:".

### `flags`

`string`

Heading rendered above the flags section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Flags:".

### `output`

`string`

Heading rendered above the Output section, which describes what the command writes when it declares `output:`. Rendered verbatim — include any trailing ':' you want. Default: "Output:".

### `usage`

`string`

Heading rendered above the usage section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Usage:".


## Schema

JSON Schema-inspired type definition used for output/response and object property schemas. The 'required' field is a string array of required property names (JSON Schema object semantics). For input schemas where 'required' means 'must be provided', use InputSchema instead.

### `description`

`string`

What this shape or property is, in a sentence. Rotini writes it as the Go doc comment on the generated type or struct field (a named schema, an output or stdin shape, and each of their properties), so the code a handler reads explains itself. Documentation only: it changes no validation and no field. Accepted on object schemas and their properties; an input's own schema uses `summary:` on the input instead.

### `required`

array of `string`

Required property names for object schemas (standard JSON Schema semantics).


## InputSchema

Extended schema for input definitions (flags, arguments, env vars, config values, stdin). Inherits all BaseSchema fields and adds input-level metadata. The 'required' field here is a boolean indicating whether this input must be provided — unlike Schema where 'required' is a string array of property names.

### Which keys each kind of input accepts

An input's `schema:` block takes the keys below, but not every key means something on every
kind of input — a flag's `negatable` has no meaning for an environment variable, and
`rotini validate` rejects it there. This table is produced by validating each key on each
kind of input, so it is what the validator actually accepts. `stdin:` takes a JSON Schema
document instead; see [StdinSpec](#stdinspec).

| Key | flag | argument | env | config |
|---|:-:|:-:|:-:|:-:|
| `$ref` | ✓ | ✓ | ✓ | ✓ |
| `complete` | ✓ | ✓ | — | — |
| `config_source` | ✓ | — | ✓ | — |
| `default` | ✓ | ✓ | ✓ | ✓ |
| `default_text` | ✓ | ✓ | ✓ | ✓ |
| `dotted_keys` | ✓ | — | — | — |
| `enum` | ✓ | ✓ | ✓ | ✓ |
| `exclusiveMaximum` | ✓ | ✓ | ✓ | ✓ |
| `exclusiveMinimum` | ✓ | ✓ | ✓ | ✓ |
| `file` | — | — | — | ✓ |
| `from` | ✓ | — | — | — |
| `ignore_case` | ✓ | ✓ | ✓ | ✓ |
| `implicit_value` | ✓ | — | — | — |
| `import` | ✓ | ✓ | ✓ | ✓ |
| `items` | ✓ | ✓ | ✓ | ✓ |
| `key` | ✓ | — | — | ✓ |
| `layout` | ✓ | ✓ | ✓ | ✓ |
| `maxItems` | ✓ | ✓ | ✓ | ✓ |
| `maxLength` | ✓ | ✓ | ✓ | ✓ |
| `maximum` | ✓ | ✓ | ✓ | ✓ |
| `minItems` | ✓ | ✓ | ✓ | ✓ |
| `minLength` | ✓ | ✓ | ✓ | ✓ |
| `minimum` | ✓ | ✓ | ✓ | ✓ |
| `multipleOf` | ✓ | ✓ | ✓ | ✓ |
| `negatable` | ✓ | — | — | — |
| `nesting` | — | — | ✓ | — |
| `nullable` | ✓ | ✓ | ✓ | ✓ |
| `pattern` | ✓ | ✓ | ✓ | ✓ |
| `pattern_message` | ✓ | ✓ | ✓ | ✓ |
| `placeholder` | ✓ | ✓ | ✓ | ✓ |
| `properties` | ✓ | — | — | — |
| `required` | ✓ | ✓ | ✓ | ✓ |
| `secret` | ✓ | ✓ | ✓ | ✓ |
| `separator` | ✓ | ✓ | — | — |
| `type` | ✓ | ✓ | ✓ | ✓ |
| `variable` | ✓ | — | ✓ | — |

### `complete`

`object`

Shell-completion hint for this input's value: for the common case of a file or directory, between a fixed `enum` and a completer written in Go (FlagValueCompleter).

Flags and arguments only. Each generated completion script turns the hint into that shell's own path completion. A completer written in Go still wins when it answers; the hint is the fallback.

### `config_source`

`string`

Flag and env inputs only: names a config_files entry whose file path this input supplies, so a user can point the program at a config file (`--config ./other.yaml`). The command line and environment are read first, then the file they name is opened. The path comes from the flag if set on the command line, then the env input's variable, then the flag's default, then the entry's own path or discover. A path supplied this way must exist: unlike a declared path, a missing file is then an error, because the user asked for it. The input's type must be string. At most one flag and one env input may name the same entry.

### `default`

The value an input takes when nothing supplies one. A scalar for a single value; a list for a repeatable input, each element added as one occurrence (as if the flag were repeated); a mapping for a map input, added as key=value pairs; and for an object-valued flag a mapping of the named schema's keys (a list of mappings for a list of objects). Write it the way a user would type the value (a duration as 30s, a date as 2026-09-29 or in the input's `layout:`). `rotini validate` checks it against the input's type, enum, bounds and schema, so a default that could never be accepted fails there rather than on every run that uses it. A supplied value replaces the default; the two never merge. `default_text:` changes only how help shows it.

### `default_text`

`string`

How help, man and markdown pages show the default, in place of the value itself. Use it for a default that reads badly as written (`default_text: 'the number of CPUs'`, `default_text: '$HOME/.cache/app'`), or one the handler computes when the input is unset, so there is no literal to show. Display only: the value the input takes is still `default`, or nothing.

### `dotted_keys`

`boolean` · default `false`

Map-typed flags only, and only with 'any' values ('map'/'object' → map[string]any). When true, a '.'-separated key in a key=value pair assigns into nested maps, helm-style: --set image.tag=v2 → map[image][tag]=v2. Opt-in because '.' is a legal character in plain map keys — without it, --label a.b=c stores the literal key 'a.b'. Each assignment overwrites whatever is at its path (creating intermediate maps as needed), so later pairs win and --set a=1 --set a.b=2 leaves a nested map under 'a'. A value is read as its JSON spelling would be — true/false, null and JSON numbers are booleans, null and numbers; anything else is text — so --set replicas=3 stores the number 3, as a config file's replicas: 3 does. Declare 'properties' on the flag's schema to give shell completion the known key paths (offered up to the '=').

### `file`

`string`

Config inputs only: reads this input from one named config_files entry only, never from the other files, so a key present in another file does not satisfy it (or its 'required'). Omit it to read through the declared order, where the first file with the key wins.

### `from`

array of `string`

Flag inputs only: where this flag's value may come from, besides the text on the command line.

- 'file' — a value starting with '@' is replaced by the named file's contents (`--token @/run/secret`; pair with `secret: true` for a token file)
- 'stdin' — a value of exactly '-' is replaced by what is piped on stdin (`-f -`); empty stdin is then a usage error, and a command cannot combine a from:stdin flag with a declared stdin: input, since stdin can be read only once
- 'value' — always allowed; listing it is documentation only. Any value that does not match an enabled marker stays literal

Text read from a file or stdin has one trailing line ending removed (leading and interior whitespace is kept), then goes through the normal type, enum and constraint checks: the flag's value is that text. On an object-valued flag the text is decoded as the object (JSON, or YAML when it spans lines); a structured payload for the whole command belongs in the command's stdin: input.

Without 'from', '@' and '-' are ordinary characters. Defaults and environment and config fallbacks are always literal: the markers apply only to values given on the command line.

### `ignore_case`

`boolean` · default `false`

With 'enum' only: match a value against the enum without regard to case, so `--mode FAST` is accepted against [fast, slow]. The value is stored in its declared spelling (fast), so a handler compares against one form. Applies wherever the input reads a value: the command line, a flag's environment and config fallbacks, and env and config inputs.

### `implicit_value`

`string` or `number` or `boolean`

Flags only: the value a flag takes when it is given without one, which makes its value optional (the `--color[=when]` shape). With `implicit_value: always`, a bare `--color` means always, `--color=never` sets never, and a flag left out takes its `default` as usual. Because the value is optional it must be attached: in `--color never`, `never` is the next argument, not the flag's value (a short flag attaches too: `-cnever`, `-c=never`). Help shows the flag as `--color[=<type>]` with `(implicit: always)`. For a scalar flag that is not a bool (a bool already works this way, with true), and the value must satisfy the flag's type, enum and constraints.

### `key`

`string`

Dotted key path the value is read from (config inputs and flag config-fallbacks; e.g. 'server.port'). Segments of letters/digits/_/-, joined by dots; rotini resolves it through the configuration files (and SNAKE_UPPER of it names a flag's env fallback variable).

### `layout`

`string`

Time inputs only (time, datetime, date, time.Time, and lists of them): how the value is written. A Go reference-time layout (the reference time Mon Jan 2 15:04:05 MST 2006 written the way yours is: `2006-01-02`, `02/01/2006`, `Jan 2 2006 15:04`), or `unix` (seconds since the epoch, fractions allowed) or `unixmilli` (milliseconds). A layout with no zone parses as UTC. Without it, `date` reads `2006-01-02` (that day's UTC midnight) and `time`/`datetime` read RFC 3339 (`2026-09-29T14:00:00Z`). Applies wherever the input reads a value, and a `default` must parse under it.

### `negatable`

`boolean` · default `false`

Bool flags only: also accept a `--no-<name>` form for every long identifier, which sets the flag false. `--color` with negatable declares `--no-color` too.

Use it to turn something off for one run when a default, a config file or an environment variable already turned it on, which a plain bool flag cannot do. Short identifiers get no negated form.

A declared identifier always wins over a derived negated one, so a flag you declare as `--no-cache` keeps that name. The negated form takes no value: `--no-color=true` is a parse error. The generated field is the same single bool either way, and help shows it as `--[no-]color`.

### `nesting`

`string`

Env inputs only, map-typed ('map'/'object' → map[string]any): the separator that collects a family of environment variables into this one nested input. The variable prefix is 'variable:' when set, else the UPPER_SNAKE form of the input's name. With name: http, variable: ACME_HTTP, nesting: "__", ACME_HTTP__TIMEOUT=30 and ACME_HTTP__RETRY__MAX=9 bind as http = {timeout: "30", retry: {max: "9"}} (segments lowercased; values are strings). 'required: true' is an error when no matching variables exist. 'default:' is rejected: set defaults in code or in a config file instead.

### `placeholder`

`string`

Display name for this input's value in generated help, man pages and usage lines: `--file <PATH>` instead of the type, `<PATH>` instead of the argument's name. Presentation only: parsing, completion and the generated field are unchanged. Conventionally UPPERCASE or &lt;angle-bracketed&gt;.

### `required`

`boolean` · default `false`

When true, the input must be provided (or stdin must not be empty for stdin inputs). Note: this is a boolean — unlike the string-array 'required' on Schema.

### `secret`

`boolean` · default `false`

When true, this input's value is treated as a secret: redacted in provenance/error output by the default input reader. It does not prompt: a handler that wants to ask for the value interactively reads it without echo itself (golang.org/x/term's ReadPassword, for one); for non-interactive supply, pair secret with from: [file] (token file) or an env input.

### `separator`

`string`

List and map flags, and a variadic argument: split each value on this character, so `--tags a,b,c` is three tags and `--label a=1,b=2` two entries. Splitting is CSV-style: an item in double quotes keeps the separator (`--tags '"a,b",c'`), leading spaces are trimmed, and an empty value (`--tags ""`) is an empty list. Repeating the flag still appends, so `--tags a,b --tags c` is three tags. Items are split before validation, so enum, item constraints and minItems/maxItems see each one. A flag's environment-variable fallback splits the same way (TAGS=a,b); a configuration file's list binds item by item whether or not a separator is declared. Without a separator, each occurrence is one value, used as is. Not valid on env and config inputs: an env input's list is always split on commas (TAGS=a,b), and a configuration file writes a list as a list.

### `variable`

`string` or `array`

The exact environment variable this input reads, instead of the name rotini would derive. It may be a list, first preferred: `variable: [GH_TOKEN, GITHUB_TOKEN]` reads the first one that is set, for a value other tools already know under more than one name. Help lists every name. A nested env input (`nesting:`) takes one name, since it is the prefix of a family of variables. Valid on env inputs and on flags (as a flag's environment fallback); rejected on arguments, config inputs and stdin, which have no environment variable.

A variable named here is never given the `env_prefix`: it is already exact, and prefixing it would silently make it a different variable.

On a flag it also turns on the fallback chain (command line, then environment, then config file, then default), keyed by the flag's own name unless `key:` names one. So `--token` with `variable: GITHUB_TOKEN` reads that variable directly, with no need for a config `key:` whose UPPER_SNAKE form happens to match. Because the flag now has a key, a configuration file that defines that key supplies it too; declare `key:` to control what that key is.


## ConfigurationFileDiscover

A run-time location strategy for a configuration file, instead of a fixed 'path'. The first directory (in the strategy's order) containing 'file' wins; a file found nowhere is simply absent, the same as a missing fixed path. Among one command's config_files the first declared still wins, wherever each was found.

### `strategy`

`string` · **required** · one of `walk-up`, `xdg`

'walk-up': search from the working directory upward, one parent at a time, until a directory containing 'file' is found or the root is reached. Use it for project-local config. On Windows the search stops at the drive root.

'xdg': search $XDG_CONFIG_HOME/&lt;app&gt;, defaulting to ~/.config/&lt;app&gt;, on every platform, Windows and macOS included. Rotini does not substitute %APPDATA% or ~/Library/Application Support, so a CLI documented as reading ~/.config/&lt;app&gt; reads the same path everywhere, and a dotfiles repository works unchanged across machines. For the platform's native location on each OS, declare a fixed 'path' instead.

### `file`

`string` · **required**

The file name to look for in each searched directory (e.g. '.acme.toml', 'config.yaml').

### `app`

`string`

The application directory under the XDG config root: the '&lt;app&gt;' in $XDG_CONFIG_HOME/&lt;app&gt;. Required by the 'xdg' strategy and rejected by 'walk-up', which has no such directory. `rotini validate` enforces both, so its message can say which strategy needs it and what it is for.


## BaseSchema

Shared fields for Schema and InputSchema. JSON Schema Draft 7 cannot combine allOf inheritance with additionalProperties: false, so an unknown key inside a schema block is rejected by `rotini validate` itself rather than by the schema — with the same positioned "unknown key" message as anywhere else in the spec. A JSON Schema keyword rotini does not implement (uniqueItems, format, title, …) is rejected too, since it would otherwise do nothing. `description` is accepted on object schemas and their properties, where it becomes a Go doc comment.

### `$ref`

`string`

Reference to a named schema in the root command's "schemas" map, by its name (`$ref: DB`) or as a JSON pointer (`$ref: '#/schemas/DB'`). The two mean the same; the pointer is what JSON Schema tooling reads. Resolved when you generate. A reference to a named scalar schema brings that schema's enum, pattern (and pattern_message), lengths and bounds with it, wherever the input leaves them unset.

On a flag, a named object schema makes the flag object-valued: its generated field is the schema's struct, and it takes any of:

- JSON — `--db '{"host":"h","port":5}'`
- key=value pairs — `--db host=h,port=5`; dotted keys nest (`pool.max=9`), a repeated key appends to a list field, and quotes keep a comma (`host="a,b"`)
- a JSON or YAML file, with `from: [file]` — `--db @db.yaml`
- one field per flag — `--db.host=h --db.port=5`

Occurrences merge in the order they appear, a later key winning. Declared as `type: array, items: {$ref: …}`, the flag is a list of objects and each occurrence is one element (per-field flags do not apply). A flag's environment fallback takes the same spellings (DB='{"host":"h"}'), and its configuration-file fallback is the object as a mapping (or a list of mappings).

Every spelling is validated against the named schema, with the same validator a stdin payload of that shape meets, and errors name the flag and the key (`--db: port: value must be >= 1`, `unknown key "bogus"`). A `default` is written as a mapping (a list of mappings for a list) and is checked against the schema by `rotini validate`; a supplied value replaces it rather than merging. Keys that shape a scalar value (enum, separator, ignore_case, implicit_value, negatable, dotted_keys, bounds, pattern) do not apply: an object's rules live in its schema. Arguments cannot be object-valued.

### `enum`

array of `string`

Allowed values. The check applies to the final value, wherever it came from, so a flag value supplied by an environment variable or a config file is checked too. At least one member: an empty list would mean the same as no enum.

### `exclusiveMaximum`

`number` or `string` or `null`

Exclusive upper bound: the value must be strictly less. Same applicability rules as 'maximum'.

### `exclusiveMinimum`

`number` or `string` or `null`

Exclusive lower bound: the value must be strictly greater. Same applicability rules as 'minimum' (numbers, durations and sizes; per-element for arrays; rejected elsewhere). exclusiveMinimum: 0 expresses "positive" exactly.

### `import`

`string`

Optional Go import path backing 'type'. Set it when 'type' references a stdlib or third-party package whose name rotini does not already know (e.g. 'github.com/google/uuid' for uuid.UUID). Omit (or leave empty) for builtins and rotini's own type names (string, int, duration, url, ip, bytesize, …) — codegen treats omitted/empty as 'no import'. The aliased form 'alias path' renames the import to avoid a clash (e.g. 'urlx github.com/me/url'). Codegen dedupes identical entries across the spec.

### `items`

[`Schema`](#schema)

Element schema for an array type: 'array' + items int generates []int, and items $ref a slice of the named type; with no items, elements are strings. Per-value constraints (enum, pattern, minimum/maximum, exclusiveMinimum/exclusiveMaximum, multipleOf, minLength/maxLength) apply to every element and may be written either here, JSON Schema style, or on the list itself. The two spellings mean the same thing, and declaring the same constraint in both places with different values is an error. minItems/maxItems belong on the list (they count elements) and are rejected here on flags, arguments, env and config. The TextUnmarshaler rule also applies to each element (see 'type').

### `maxItems`

`integer`

Maximum number of values for a repeatable (array or map) input — rejected on scalar types.

### `maxLength`

`integer`

Maximum string length in runes (string types only; for []string, each element) — rejected on non-string types.

### `maximum`

`number` or `string` or `null`

Maximum allowed value (inclusive). Same applicability rules as 'minimum' (numbers, durations and sizes, each in its own spelling; per-element for arrays; rejected elsewhere); maximum: 0 is a real, enforced bound.

### `minItems`

`integer`

Minimum number of values for a repeatable (array or map) input — rejected on scalar types.

### `minLength`

`integer`

Minimum string length in runes (string types only; for []string, each element) — rejected on non-string types.

### `minimum`

`number` or `string` or `null`

Minimum allowed value (inclusive). Number types (the int, uint and float types and the integer and number aliases) take a number; a duration takes a duration (`minimum: 1s`) and a bytesize a size (`minimum: 1Mi`, or a number of bytes), read by the same parser as the value and printed back that way in errors (`must be >= 1s`). Rejected on any other type, where it would have no effect. For a repeatable input the bound applies to each element. minimum: 0 is a real, enforced bound.

### `multipleOf`

`number` or `string` or `null`

The value must be an integer multiple of this (JSON Schema semantics: the division yields an integer). Numbers, durations and sizes, in their own spelling (`multipleOf: 1s`); per-element for arrays; must be strictly positive.

### `nullable`

`boolean` · default `false`

Generate the field as a pointer (\*T): nil means the input was not provided, distinguishable from its zero value. Defaults/values coerce through the pointer.

### `pattern`

`string`

Regular expression the value must match (string types only; for a list, each element). As in JSON Schema, the pattern matches anywhere in the value unless anchored: use ^…$ for a full match. A failure shows the user the regex itself unless `pattern_message:` says it in words.

### `pattern_message`

`string`

With 'pattern' only: what the user is told when a value does not match, in place of the regex — which is written for the program, not the person typing. Phrase it to follow the input's name: `pattern_message: must be json, yaml, wide, name or custom-columns=<spec>` reports `-o must be json, yaml, wide, name or custom-columns=<spec> (got "bogus")`, where the default is `-o must match ^(json|yaml|…)$ (got "bogus")`. Applies wherever the pattern is checked: an input on every channel it reads, each element of a list, a named schema an input refers to, and a property of an object-valued flag or a stdin payload.

### `properties`

`object`

Property schemas for an object shape. Used by document shapes (output, stdin payloads, named schemas) — and, on a map-typed FLAG, the declared property names feed shell completion's key vocabulary (dotted paths when dotted_keys is set, offered up to the '=').

### `type`

`string`

The type used to parse and store the value. Go type names (bool, int, float64, []string, duration, map) and JSON Schema names (boolean, integer, number, array, object) are equivalent. A bool accepts true/false, yes/no, on/off, y/n, t/f and 1/0, in any case, so `--cache=off` and CACHE=yes both work.

Value types parse a kind of value and generate the matching Go field:

- 'duration' — time.Duration; Go units plus 'd' days and 'w' weeks (7d, 2w3d)
- 'time' / 'datetime' — time.Time, RFC 3339 (2026-09-29T14:00:00Z)
- 'date' — time.Time, a calendar date (2026-09-29, that day's UTC midnight). All three time types take `layout:` for another format, Unix timestamps included
- 'url' — \*url.URL; needs a scheme and host
- 'email' — mail.Address; 'Name &lt;a@b.c&gt;' or a bare address
- 'timezone' — \*time.Location; an IANA name such as Europe/Berlin
- 'mac' (net.HardwareAddr), 'ip' (netip.Addr), 'cidr' (netip.Prefix), 'hostport' (netip.AddrPort)
- 'bytesize' — rotini.ByteSize; 512Mi, 10MB, 1.5GiB (an 'i' makes the unit binary)
- 'hexbytes' — rotini.HexBytes; optional 0x
- 'base64bytes' — rotini.Base64Bytes; standard or URL-safe, padded or not
- 'existingfile' / 'existingdir' — a plain string field, checked at parse time to exist and be that kind of thing, so a bad path is a usage error naming the flag the user typed. The check is existence and kind only: expanding '~', cleaning, following symlinks and creating a missing file are the handler's policy

A value type works inside Go spellings too ('[]bytesize', 'map[string]duration'), and help shows the name as written ('--limit bytesize'), not the Go type. A lowercase name that is neither a Go builtin nor one of these is rejected as a typo, with a suggestion.

Shapes that change how a flag is written:

- 'count' (flags only) — a presence counter: the flag takes no value, each occurrence increments the generated int field (-vvv → 3, clustering included), an inline value (--verbose=3) is a parse error, and every value-shaped key (default, enum, constraints, from, key, …) is rejected
- '[]…' / 'array' — repeatable (--tag a --tag b → a slice); 'items' declares the element type ('array' + items int → []int), defaulting to string
- a map ('map[string]string', or 'map'/'object' → map[string]any) — repeatable too, taking 'key=value' pairs (--label k=v --label a=b; split on the first '='; the value coerced to the element type)

Any other Go type (time.Time, uuid.UUID, your own): set 'import' to the backing package path. It must implement encoding.TextUnmarshaler — that method is its parser and validator, applied to each element of a list too. Rotini refuses a type without it at parse time with a loud error, never a silently zeroed field; validate cannot check it, since that would mean type-checking foreign packages.

A NAMED OBJECT schema is different: a flag whose schema is `$ref: '#/schemas/DB'` (or `type: array, items: {$ref: '#/schemas/DB'}` for a list) takes a structured value — see `$ref`.

