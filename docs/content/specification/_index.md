---
title: "specification"
---

<!-- Code generated from the rotini JSON Schema; DO NOT EDIT.
     Edit the schema's descriptions, or the example in docs/assets/examples, then run:
       go test ./internal/codegen -run SchemaDocs -update-schema-docs -->

# .rotini.spec.yaml

Schema for a Rotini CLI definition spec file. The document wraps a top-level `version` and a single root `command` (the CLI's root command); from there the tree is commands all the way down. `env_prefix` and `schemas` are root-command-level keys valid only on the root command (under `command`), and `$schema` is an optional document-level editor-tooling key.

{{< code title=".rotini.spec.yaml — every key" language="yaml" file="examples/rotini.spec.yaml" open="true" copy="true" >}}{{< /code >}}

{{< code title="the JSON Schema" language="json" file="schemas/schema-spec.json" open="false" copy="true" >}}{{< /code >}}

Below, every key. `rotini validate` checks all of them before any code is generated, and
this list is rendered from the schema, so it always matches what the tool accepts.

## Document

### `version`

`string` · **required**

The MINIMUM rotini this spec requires (X.Y.Z) — the feature set it was written against, not an exact pin. Any rotini of the same major at or beyond it accepts the document, so a patch or minor upgrade never forces an edit here. Two cases are errors: a rotini OLDER than this (it may not know keys the spec uses) and a different MAJOR (an incompatible feature set). The check is skipped for a development build of rotini, which reports no release version (0.0.0, or none at all). This — not the optional `$schema` URL — is the source of the check.

### `command`

[`Command`](#command) · **required**

The CLI's root command (the binary itself): its name, doc-fields, inputs (flags/arguments/env/config/config_files/stdin) and sub-commands. The root must use 'name' (not '$ref'). The root-command-level keys `env_prefix` and `schemas` live here.

### `$schema`

`string`

Optional URI identifying the rotini spec schema, for editor tooling ONLY — rotini itself never fetches it, and the binary-version check reads the top-level `version` key, not this. Any URI is accepted: a released schema (https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/v1.0.0/schema-spec.json — note the 'v', matching the git tag), a path written into your project by the conf's `generate.schemas.spec.file`, or a fork's own URL. A relative path is resolved by your editor, not by rotini. This key is what `rotini init` seeds (`$schema: ./.rotini-schema.spec.json`) and works in every format; YAML editors also accept a `# yaml-language-server: $schema=<path>` comment in its place.


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

Config-file SOURCES this command contributes — where config values come from (a fixed 'path' or 'discover'). CASCADING: a command's effective sources are the union along the resolved chain (root → leaf), so a 'config' input on this command or any descendant may pin (schema 'file:') to a source declared here or on any ancestor. Only sources along the INVOKED chain are loaded — off-branch files are never read. Source names must be unique within a chain (a collision is an error); declaring the same physical file ('path'/'discover' target) at two levels is a warning. Precedence when two in-scope files define the same key: nearest-to-the-invoked-command wins, and within one command's list the FIRST declared wins — so list the more specific file (a project's) before the more general one (the user's). COMPOSITION: a $ref'd child's sources travel with its command tree, re-scoped to the path the graft occupies, so `parent child cmd` reads what `child cmd` reads without the parent re-declaring anything.

#### `env_prefix`

`string`

Document-level (root only): prefix for every DERIVED environment-variable name — the SNAKE_UPPER projections rotini computes: plain env inputs without 'variable:' (input 'home' → ACME_HOME), nested env families without 'variable:' (the family's base name), and flags' env fallbacks (key 'server.port' → ACME_SERVER_PORT). Explicitly named 'variable:' values are exempt — they are already exact. With a prefix declared the program's derived env namespace is SCOPED to it: an unprefixed conventional name (HOME for input 'home') no longer binds. UPPER_SNAKE, no trailing underscore (rotini adds the '_' separator). The derived name is written into the generated field's `env:` tag at codegen time, so what generated help prints is exactly what the binder reads — a name is never re-derived at run time. COMPOSITION: a $ref'd child's env_prefix travels with its command tree, so a parent that declares none adopts the child's; a parent that declares one wins, and two children that disagree are rejected (one descriptor carries one prefix).

#### `flag_groups`

array of [`FlagGroup`](#flaggroup)

Cross-flag presence rules validated at parse time (e.g. mutually exclusive output formats, a required-together credential pair).

#### `flag_dependencies`

array of [`FlagDependency`](#flagdependency)

Conditional cross-flag requirements validated at parse time: when one flag is set, others become required (e.g. when --tls is set, --cert and --key are required).

### Sub-commands and composition

#### `commands`

array of [`Command`](#command)

Sub-commands of this command (inline or composed via $ref). On a $ref node these are MERGED additively onto the composed child's own subtree (the overlay model — see '$ref'); a name/alias collision across the merged set is an error.

#### `$ref`

`string`

Path to another rotini spec file whose root command is statically composed in as this sub-command. Relative to this spec file. Not valid on the root command.

The composed child is the base, and the parent can adjust it at the point where it is mounted:

- Identity and presentation keys declared alongside the $ref (name, aliases, summary, description, usage, header, footer, examples, headings, help, man, markdown, exit_status, see_also, group, hidden, deprecated, deprecated_identifiers, filename, plugin_path) WIN over the child's, for that one composed node — the child's own sub-commands keep theirs. A parent tailors the child for its tree without forking it.
- A 'commands:' authored next to the $ref is MERGED onto the child's own subtree: its inline entries get their own stubs, and its $ref entries compose as further children.
- `handler:` on a $ref node points the composed command at a different handler package.
- Handler-coupled keys (flags, arguments, env, config, config_files, stdin, flag_groups, flag_dependencies, output, remote_commands, remote_discovery, passthrough) CANNOT be overlaid: the composed command runs the child's handler, built against the child's own inputs and output, so validation rejects them here. Declare them in the child spec.

The child's own remote_commands, remote_discovery and passthrough travel with it. The child is validated with the parent: `rotini validate` and `generate` on the parent check every locally composed spec as its own document, positioned in its own file.

#### `handler`

[`HandlerSource`](#handlersource)

Source this command's handlers from an external Go PACKAGE instead of a generated stub (handler delegation). Valid on any SUB-command, not the root. On a '$ref' node it OVERRIDES the auto-derived child cli: a composed local or mod:// child normally delegates to its own generated package, and this points the command at a different one instead. On an INLINE command it is the own-types + delegated-handler hybrid: the command's structure and typed inputs are still generated locally, but its handler delegates to the package (no stub file is seeded). It applies per-command — there is no subtree cascade, so an inline sub-command without its own 'handler:' still gets a normal generated stub. The package must export a constructor '<convention>() rotini.Handlers' per command (the normal five-hook handler type; unimplemented hooks default to no-op); codegen delegates 'pkg.<Convention>()'. The contract is enforced at COMPILE time — rotini cannot type-check a foreign package.

#### `passthrough`

`boolean`

When true, every token after this command's own name binds as a raw positional — no flag parsing, no unknown-flag errors, no '--' needed (the wrapper-CLI case: `mytool exec ls -la` forwards '-la' verbatim, and a literal '--' passes through too). Tokens BEFORE the command (ancestor flags) parse normally. A passthrough command declares no flags, no sub-commands, no remote commands or discovery, and its last argument must be a variadic '[]string' — the receiver of the raw tokens (validation enforces all of this). Shell completion offers nothing past the boundary, falling back to file completion.

#### `remote_commands`

array of [`RemoteCommandSpec`](#remotecommandspec)

Co-located remote binaries dispatched as first-class sub-commands of this command.

#### `remote_discovery`

[`RemoteDiscovery`](#remotediscovery)

Auto-expose external '<prefix>*' executables as remote sub-commands of this command (kubectl/git/gh plugin discovery), in addition to any declared remote_commands. Presence enables discovery.

#### `plugin_path`

`string`

Extra directory to search for this command's plugin binaries, in addition to the host binary's own directory and PATH. Relative to the working directory at run time; a leading ~ and $VAR references are expanded when the program runs, and a directory that does not exist yet is simply empty. On a `$ref` node it overrides the composed child's own. It applies to BOTH kinds of plugin: the 'remote_commands' this spec declares and anything 'remote_discovery' finds — they are the same binaries in the same place, so they are configured once here rather than per-mechanism. Without it, a declared remote could only ever be installed next to the host binary or on PATH, which is the git/kubectl convention and not always the right one for a vendored or bundled plugin. Search order is fixed and the same for both: next to the host binary, then this directory, then PATH — so a plugin shipped beside the binary always wins over one found here, and a failure names the locations it actually searched.

#### `timeout`

`string`

Not supported on a local command and rejected by rotini validation: a timeout is a remote-only, host-side bound on a dispatched binary, so it has no effect on local execution. Set it on a remote_commands[] entry's 'timeout' instead. (Recognized here only so validation can give that targeted error rather than a generic 'unknown property'.)

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

#### `examples`

array of `string`

Example command-line invocations, rendered one per line. Ignored when 'help' is set.

#### `exit_status`

array of [`ExitStatusEntry`](#exitstatusentry)

Exit codes this command documents, rendered as an EXIT STATUS section in the man and markdown pages. DATA ONLY, and rotini does not check it: the runtime sets no exit code of its own except the outcome funnel's floor, which exits 1 for a recorded error or a recovered panic when no handler set a deliberate code, and the default signal handling, which exits 128+n on signal n (130 for Ctrl-C). So a command that documents `2: invalid input` here and only calls RecordError will actually exit 1 — set the code explicitly with rtx.Exit (or rtx.HaltWithCode) in the handler to make the binary agree with this section. Ignored when 'man' (verbatim) is set.

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

Exact, verbatim man page for this command (the man feature's per-command escape, mirroring 'help'). When set, rotini writes it as given — byte-for-byte except that ANSI styling is removed, since a man page carries none — and ignores the structured doc-fields for the man page; when unset, the man page is rendered from those fields through the man template.

#### `markdown`

`string`

Exact, verbatim markdown reference page for this command (the markdown feature's per-command escape, mirroring 'help'/'man'). When set, rotini writes it as given — byte-for-byte except that ANSI styling is removed; when unset, the page is rendered from the structured doc-fields through the markdown template.

### Output and shared types

#### `output`

[`Schema`](#schema)

This command's output shape, as a JSON-schema type. rotini generates a typed '<Prefix>Output' Go struct (or a named-type alias when it is a '$ref' to a document-level schema) for the handler to use however it likes — it wires NO flag and triggers NO rendering. Handlers have no return type by design, so 'output' is an opt-in building block, never a framework-enforced contract.

#### `schemas`

`object`

Document-level (root only): reusable named schema definitions. Referenced elsewhere by name, `$ref: <Name>`, or as a pointer, `$ref: "#/schemas/<Name>"`. Each may carry a `description`, which becomes the generated type's doc comment. Names must be PascalCase Go-exportable identifiers — each becomes a generated Go type in the cmd package (or in the models package, when the conf declares one), which other packages may import.

### Generated code

#### `filename`

`string`

Override the name of this command's generated handler-stub .go file (in the cli package). Defaults to a name derived from the command path ('<root>_<path>.go', every '-' written '_': config_get_contexts.go), reserved-name-escaped so a command named 'test'/'<GOOS>'/'<GOARCH>' does not collide with Go's filename rules. Must end in '.go', must not itself be a name Go reads specially ('_test.go', '_<GOOS>.go', '_<GOARCH>.go'), and must be unique among the commands generated into the same package. Renaming it orphans (and prunes) the previous stub file — move your handler code first.


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

Group label that buckets this flag under its own heading in generated help, exactly as a command's 'group' buckets it in the Commands list: flags sharing a group appear together, groups appear in the order their first member is declared, and ungrouped flags fall under the default Flags heading.

PRESENTATION ONLY — parsing, precedence and the generated field are untouched. It is for the command with twenty flags, where one undifferentiated wall is the difference between a help page someone reads and one they skim past. Not to be confused with 'flag_groups', which is cross-flag VALIDATION and shares nothing but the word.

### `hidden`

`boolean` · default `false`

When true, the flag is omitted from generated help (it still parses on the command line).

### `identifiers`

array of `string`

CLI flag identifiers (e.g., '--force', '-f'). When absent, '--<name>' is derived from the flag's name, with '_' written as '-' ('dry_run' → --dry-run).

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

The four DOCUMENT formats — json, yaml, jsonc, toml — decode it into the generated <Prefix>Stdin struct, validated against the declared schema. Defaults to json.

The two RAW formats are for the grep/jq/fmt family, whose stdin is not a document: 'text' binds the whole payload as a single string, and 'lines' binds it as []string split on newlines (a trailing newline adds no empty element). Both require the schema's type to match — 'string' for text, '[]string' or 'array' for lines — and neither generates a <Prefix>Stdin struct, because there is nothing to shape. Declaring the channel this way, rather than reading rtx.Stdin directly, puts it in the command's help page and completion.

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

Decode format for the file. OMITTED means the format is inferred from the file extension — declare it when the extension is absent or misleading. 'jsonc' is JSON with comments and trailing commas. 'dotenv' reads KEY=value lines whose keys stay VERBATIM: a config input reading one declares `key: API_ENDPOINT`, not a dotted path.

### `path`

`string`

File path (supports ~ for home dir). Exactly one of 'path' or 'discover' must be set.

### `schema`

[`Schema`](#schema)

Optional load-time validation: the loaded document is validated against this schema at bind time, before any value is read from it — a non-conforming file is a loud error naming the file and the violation (the same gate the stdin channel applies to its payload). The file that actually resolved — fixed path, discovered, or config_source-supplied — is the file validated; an absent file passes vacuously (absence is the per-input required's concern). Document-level named schemas resolve via "$ref": "#/schemas/<Name>". No typed struct is generated from this — typed access to config values is the config: inputs channel.


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

Go import path of the handler package, in the same 'alias path' form an input type's 'import' uses (e.g. 'deploycli github.com/acme/clis/deploy/rth'); deduped with other imports. A bare path derives its alias from the last segment. Codegen calls '<alias>.<convention>()'.

### `convention`

`string` · **required**

Function-name prefix the package exports per command: codegen delegates this command to '<alias>.<convention>()' and each sub-command to '<alias>.<convention><SubPath>()', each returning a rotini.Handlers. PascalCase Go-exportable identifier.


## RemoteCommandSpec

### `name`

`string` · **required**

Name of the remote command. The dispatched binary is named <program>-<name>, and is searched for next to the host binary, then in the command's plugin_path, then on PATH. Inside a $ref-composed subtree <program> is the composed spec's own name, so one installed plugin serves both that spec's own binary and a parent that composes it.

### `aliases`

array of `string`

Additional names that invoke this remote command.

### `summary`

`string`

Short one-liner shown next to this remote command in its parent's generated Commands list.

### `timeout`

`string`

Host-side timeout for the remote binary execution. Uses Go duration format (e.g. "10s", "1m30s"). Empty or omitted means no timeout.


## RemoteDiscovery

Auto-expose external '<prefix>*' executables as remote sub-commands (kubectl/git/gh plugin style), alongside any declared remote_commands. Presence enables discovery; a discovered name that collides with a declared command or remote is skipped.

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

### `usage`

`string`

Heading rendered above the usage section of the generated help page. Rendered verbatim — include any trailing ':' you want. Default: "Usage:".


## Schema

JSON Schema-inspired type definition used for output/response and object property schemas. The 'required' field is a string array of required property names (JSON Schema object semantics). For input schemas where 'required' means 'must be provided', use InputSchema instead.

### `description`

`string`

What this shape or property IS, in a sentence. rotini writes it as the Go doc comment on the generated type or struct field (a named schema, an output or stdin shape, and each of their properties), so the code a handler reads explains itself. Documentation only: it changes no validation and no field. Accepted on object schemas and their properties; an input's own schema has `summary:` on the input instead.

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

Declarative shell-completion hint for this input's VALUE — the case between a static `enum` and writing a Go FlagValueCompleter, which is 'this is a file': the commonest value shape there is.

Flags and arguments only. The hint reaches the shell as a directive on the last line of the hidden __complete output, and each generated script translates it into that shell's own path completion. A dynamic completer still wins when it answers — this is the fallback, not a ceiling.

### `config_source`

`string`

Flag and env inputs only: names a config_files entry whose file PATH this input supplies — the declarative two-phase parse (CLI bootstrap): argv and env are read first, then the file channel opens whatever they pointed at. Precedence for the path: the flag explicitly set on argv, then the env input's variable, then the flag's declared default, then the entry's own path/discover. A path supplied through this input must exist — unlike a declared path, a missing file is then an error, because the user explicitly asked for it. The input's type must be string. At most one flag and one env input may claim the same entry.

### `default`

The value an input takes when no channel supplies one. A scalar for a single value; a list for a repeatable input, each element seeded as one occurrence (as if the flag were repeated); a mapping for a map input, seeded as key=value pairs; and for an object-valued flag a mapping of the named schema's keys (a list of mappings for a list of objects). It is written the way a user would write the value (a duration as 30s, a date as 2026-09-29 or under `layout:`), and `rotini validate` checks it against the input's type, enum, bounds and schema, so a default that could never be accepted fails there rather than on every run that takes it. A supplied value REPLACES the default, never merges with it. `default_text:` changes only how help shows it.

### `default_text`

`string`

The default as help, man and markdown SHOW it, in place of the value itself. For a default that reads badly verbatim (`default_text: 'the number of CPUs'`, `default_text: '$HOME/.cache/app'`) or that the handler computes when the input is unset, so there is no literal to show. Display only: the value the input takes is still `default`, or nothing.

### `dotted_keys`

`boolean` · default `false`

Map-typed flags only, and only with 'any' values ('map'/'object' → map[string]any). When true, a '.'-separated key in a key=value pair assigns into nested maps, helm-style: --set image.tag=v2 → map[image][tag]=v2. Opt-in because '.' is a legal character in plain map keys — without it, --label a.b=c stores the literal key 'a.b'. Each assignment overwrites whatever is at its path (creating intermediate maps as needed), so later pairs win and --set a=1 --set a.b=2 leaves a nested map under 'a'. A value is read as its JSON spelling would be — true/false, null and JSON numbers are booleans, null and numbers; anything else is text — so --set replicas=3 stores the number 3, as a config file's replicas: 3 does. Declare 'properties' on the flag's schema to give shell completion the known key paths (offered up to the '=').

### `file`

`string`

Config inputs only: pins this input to ONE named config_files entry — the value (and its 'required') is read from that file ONLY, never from the merged precedence chain, so a key present in another file does not satisfy it. Omit to read through the declared precedence order (first file with the key wins).

### `from`

array of `string`

Flag inputs only: where this flag's value may be acquired from, beyond the literal argv text.

- 'file' — a value starting with '@' is replaced by the named file's contents (`--token @/run/secret`; pair with `secret: true` for the token-file idiom)
- 'stdin' — a value of exactly '-' is replaced by the piped stdin (`-f -`); empty stdin is then a usage error, and a command cannot combine a from:stdin flag with a declared stdin: channel, since stdin has one consumer
- 'value' — implicit and always allowed; listing it is documentation only. Any value not matching an enabled sentinel stays literal

Resolved file or stdin text has one trailing line ending removed (leading and interior whitespace is content), then flows through the normal type, enum and constraint checks — the flag's value IS the resolved text. On an object-valued flag the resolved text is decoded as the object (JSON, or YAML when it spans lines); a structured payload for the command as a whole is the stdin: channel's job.

Without 'from', '@' and '-' are ordinary characters. Declared defaults and env/config fallbacks are always literal — the sentinels apply to argv-supplied values only.

### `ignore_case`

`boolean` · default `false`

With 'enum' only: match a value against the enum without regard to case, so `--mode FAST` is accepted against [fast, slow]. The value binds as the DECLARED spelling (fast), so a handler compares against one form. Applies on every channel the input reads: argv, a flag's env and config fallbacks, and env and config inputs.

### `implicit_value`

`string` or `number` or `boolean`

FLAGS only: the value a flag takes when it is given WITHOUT one, which makes its value optional — the `--color[=when]` shape. With `implicit_value: always`, a bare `--color` means always, `--color=never` sets never, and a flag left out takes its `default` as usual. Because the value is optional it must be attached: `--color never` leaves `never` as the next argument, not the flag's value (a short flag attaches too: `-cnever`, `-c=never`). Help shows the flag as `--color[=<type>]` with `(implicit: always)`. For a scalar, non-bool flag — a bool already works this way, with true — and the value must satisfy the flag's type, enum and constraints.

### `key`

`string`

Dotted key path the value is read from (config inputs and flag config-fallbacks; e.g. 'server.port'). Segments of letters/digits/_/-, joined by dots; rotini resolves it through the configuration files (and SNAKE_UPPER of it names a flag's env fallback variable).

### `layout`

`string`

TIME inputs only (time, datetime, date, time.Time, and lists of them): how the value is written. A Go reference-time layout — the reference time Mon Jan 2 15:04:05 MST 2006 written the way yours is (`2006-01-02`, `02/01/2006`, `Jan 2 2006 15:04`) — or `unix` (seconds since the epoch, fractions allowed) or `unixmilli` (milliseconds). A layout with no zone parses as UTC. Without it, `date` reads `2006-01-02` (that day's UTC midnight) and `time`/`datetime` read RFC 3339 (`2026-09-29T14:00:00Z`). Applies on every channel the input reads, and a `default` must parse under it.

### `negatable`

`boolean` · default `false`

BOOL FLAGS only: also accept a `--no-<name>` form for every LONG identifier, which sets the flag false. `--color` with negatable declares `--no-color` too.

It exists for the direction a plain bool cannot express: turning something OFF for one run when a default, a config file or an environment variable already turned it on. Short identifiers get no negated form — `-no-c` is not a thing, and inventing `-C` is not rotini's call.

A declared identifier always wins over a derived negated one, so an author who genuinely declares `--no-cache` on another flag keeps it. The negated form takes no value: `--no-color=true` is a parse error rather than a riddle. The generated field is the same single bool either way, and `--[no-]color` is how it renders in help.

### `nesting`

`string`

Env inputs only, map-typed ('map'/'object' → map[string]any): the separator that aggregates a FAMILY of environment variables into this one nested input. The variable prefix is 'variable:' when set, else the SNAKE_UPPER of the input's name. With name: http, variable: ACME_HTTP, nesting: "__" — ACME_HTTP__TIMEOUT=30 and ACME_HTTP__RETRY__MAX=9 bind as http = {timeout: "30", retry: {max: "9"}} (segments lowercased; values are strings). 'required: true' errors when no matching variables exist. 'default:' is rejected — seed defaults in code or config instead.

### `placeholder`

`string`

Display name for this input's VALUE in generated help/man/usage — `--file <PATH>` instead of the Go type token, `<PATH>` instead of the argument's name. Pure presentation: parsing, completion, and the generated field are untouched. Conventionally UPPERCASE or <angle-bracketed>.

### `required`

`boolean` · default `false`

When true, the input must be provided (or stdin must not be empty for stdin inputs). Note: this is a boolean — unlike the string-array 'required' on Schema.

### `secret`

`boolean` · default `false`

When true, this input's value is treated as a secret: redacted in provenance/error output by the default binder. It does not prompt: a handler that wants to ask for the value interactively calls rotini.ReadSecret(rtx.Stdin), which reads a line without echoing it; for non-interactive supply, pair secret with from: [file] (token file) or an env input.

### `separator`

`string`

LIST and MAP flags, and a variadic argument: split each value on this character, so `--tags a,b,c` is three tags and `--label a=1,b=2` two entries. Splitting is CSV-style — an item in double quotes keeps the separator (`--tags '"a,b",c'`), leading spaces are trimmed, and an empty value (`--tags ""`) is an empty list. Repeating the flag still appends, so `--tags a,b --tags c` is three tags. Items are split before validation, so enum, item constraints and minItems/maxItems see each one. A flag's environment-variable fallback splits the same way (TAGS=a,b); a configuration file's list binds item by item whether or not a separator is declared. Omitted: one value per occurrence, the value untouched. Not on env and config INPUTS: an env input's list is split on commas by rotini's configuration reader (TAGS=a,b), and a configuration file writes a list as a list.

### `variable`

`string` or `array`

The EXACT environment variable this input reads, instead of the name rotini would derive — or a LIST of names, first preferred: `variable: [GH_TOKEN, GITHUB_TOKEN]` reads the first one that is set, for a value other tools already know under more than one name. Help lists every name. A nested env input (`nesting:`) takes one name, since it is the prefix of a family of variables. Valid on env inputs and on FLAGS (as a flag's env fallback); rejected on arguments, config inputs and stdin, which have no environment channel.

Exempt from `env_prefix` either way: an explicitly named variable is already exact, and prefixing it would silently make it a different variable.

On a flag it also opts the flag into the fallback chain (argv > env > config > default), keyed by the flag's own name unless `key:` names one — so `--token` with `variable: GITHUB_TOKEN` reads that variable directly, with no config `key:` whose SNAKE_UPPER must happen to match. Because the flag now has a key, a configuration file defining that key supplies it too; declare `key:` to control what that key is.


## ConfigurationFileDiscover

A run-time location strategy for a configuration file, instead of a fixed 'path'. The first directory (in the strategy's order) containing 'file' wins; a file found nowhere is simply absent, the same as a missing fixed path. Among one command's config_files the first declared still wins, wherever each was found.

### `strategy`

`string` · **required** · one of `walk-up`, `xdg`

'walk-up': search from the working directory upward, one parent at a time, until a directory containing 'file' is found or the root is reached — project-local config, git-style. On Windows the walk stops at the drive root.

'xdg': search $XDG_CONFIG_HOME/<app>, defaulting to ~/.config/<app>. XDG-LITERAL ON EVERY PLATFORM, Windows and macOS included: rotini deliberately does NOT substitute %APPDATA% or ~/Library/Application Support, so a CLI documented as reading ~/.config/<app> reads the same path everywhere and a dotfiles repository works unchanged across machines. A tool that wants the platform-native location per OS declares a fixed 'path' instead.

### `file`

`string` · **required**

The file name to look for in each searched directory (e.g. '.acme.toml', 'config.yaml').

### `app`

`string`

The application directory under the XDG config root — the '<app>' in $XDG_CONFIG_HOME/<app>. REQUIRED by the 'xdg' strategy and rejected by 'walk-up', which has no such directory. Both are enforced by rotini validation rather than by this schema, deliberately: a JSON Schema if/then can only say "missing required property", where `rotini validate` says which strategy needs it and what it is for.


## BaseSchema

Shared fields for Schema and InputSchema. JSON Schema Draft 7 cannot combine allOf inheritance with additionalProperties: false, so an unknown key inside a schema block is rejected by `rotini validate` itself rather than by the schema — with the same positioned "unknown key" message as anywhere else in the spec. A JSON Schema keyword rotini does not implement (uniqueItems, format, title, …) is rejected too, since it would otherwise do nothing. `description` is accepted on object schemas and their properties, where it becomes a Go doc comment.

### `$ref`

`string`

Reference to a named schema in the root command's "schemas" map, by its name (`$ref: DB`) or as a JSON pointer (`$ref: '#/schemas/DB'`) — the two mean the same; the pointer is what JSON Schema tooling reads. Resolved at codegen time. A reference to a named SCALAR schema brings that schema's enum, pattern (and pattern_message), lengths and bounds with it, wherever the input leaves them unset.

On a FLAG, a named OBJECT schema makes the flag object-valued: its generated field is the schema's struct, and it takes any of:

- JSON — `--db '{"host":"h","port":5}'`
- key=value pairs — `--db host=h,port=5`; dotted keys nest (`pool.max=9`), a repeated key appends to a list field, and quotes keep a comma (`host="a,b"`)
- a JSON or YAML file, with `from: [file]` — `--db @db.yaml`
- one field per flag — `--db.host=h --db.port=5`

Occurrences MERGE in argv order, a later key winning. Declared as `type: array, items: {$ref: …}`, the flag is a list of objects and each occurrence is one element (per-field flags do not apply). A flag's environment fallback takes the same spellings (DB='{"host":"h"}'), and its configuration-file fallback is the object as a mapping (or a list of mappings).

Every spelling is validated against the named schema — the same schema, and validator, a stdin payload of that shape meets — and errors name the flag and the key (`--db: port: value must be >= 1`, `unknown key "bogus"`). A `default` is written as a mapping (a list of mappings for a list) and is checked against the schema at validate time; a supplied value replaces it rather than merging. Scalar-shaping keys (enum, separator, ignore_case, implicit_value, negatable, dotted_keys, bounds, pattern) do not apply — an object's rules live in its schema. Arguments cannot be object-valued.

### `enum`

array of `string`

Allowed values, validated over the FULLY reconciled value (an env/config-supplied flag value is enum-checked too). At least one member — an empty list would mean the same as absent.

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

Element schema for an array type: 'array' + items int generates []int, items $ref a named-type slice; omitted items default to string elements. Per-value constraints (enum, pattern, minimum/maximum, exclusiveMinimum/exclusiveMaximum, multipleOf, minLength/maxLength) apply to EVERY element and may be written either here, JSON-Schema style, or on the list itself — the two spellings mean the same thing, and declaring the same constraint in both places with different values is an error. minItems/maxItems belong on the list (they count elements) and are rejected here on flags, arguments, env and config. The TextUnmarshaler contract also applies per element (see 'type').

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

Minimum allowed value (inclusive). Numeric-family types (int/uint/float variants and the integer/number aliases) take a number; a duration takes a duration (`minimum: 1s`) and a bytesize a size (`minimum: 1Mi`, or a number of bytes) — read by the same parser as the value, and printed back that way in errors (`must be >= 1s`). Rejected on any other type, where it would be silently ignored. For a repeatable input the bound applies to each ELEMENT. minimum: 0 is a real, enforced bound.

### `multipleOf`

`number` or `string` or `null`

The value must be an integer multiple of this (JSON Schema semantics: the division yields an integer). Numbers, durations and sizes, in their own spelling (`multipleOf: 1s`); per-element for arrays; must be strictly positive.

### `nullable`

`boolean` · default `false`

Generate the field as a pointer (*T): nil means the input was not provided, distinguishable from its zero value. Defaults/values coerce through the pointer.

### `pattern`

`string`

Regular expression the value must match (string types only; for arrays, each element). JSON-Schema SUBSTRING semantics: the pattern matches anywhere in the value unless anchored — use ^…$ for a full match. A failure shows the user the regex itself unless `pattern_message:` says it in words.

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
- 'url' — *url.URL; needs a scheme and host
- 'email' — mail.Address; 'Name <a@b.c>' or a bare address
- 'timezone' — *time.Location; an IANA name such as Europe/Berlin
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

Any other Go type (time.Time, uuid.UUID, your own): set 'import' to the backing package path. It must implement encoding.TextUnmarshaler — that method is its parser and validator, applied to each element of a list too. rotini refuses a type without it at parse time with a loud error, never a silently zeroed field; validate cannot check it, since that would mean type-checking foreign packages.

A NAMED OBJECT schema is different: a flag whose schema is `$ref: '#/schemas/DB'` (or `type: array, items: {$ref: '#/schemas/DB'}` for a list) takes a structured value — see `$ref`.

