# rotini validate

validate a spec file

Validate a rotini spec file for correctness.

## Usage

```
rotini validate [file] [flags]
```

## Arguments

- `[file]` — path to the spec file (default `.rotini.spec.yaml`)

## Flags

- `--fail string` — failure reporting — fast (first problem) or collect (all); defaults to the module conf's validate.fail, else collect [fast|collect]
- `-h, --help` — print help

## Examples

```
rotini validate
rotini val ./path/to/.rotini.yaml
```

Use "rotini help <command>" for more information about a command.