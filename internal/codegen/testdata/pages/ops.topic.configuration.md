# ops help configuration

the configuration files and keys ops reads

## Configuration

- `deploy.registry` `string` — the registry to push to (in `.ops.yaml`) (used by: ops deploy)
- `deploy.replicas` `int` — how many to run (default `1`) (used by: ops deploy)
- `deploy.retries` `int` — how many times to retry (default `1`) (used by: ops deploy); between 0 and 9
- `profile` `string` — the output profile (one of `short`, `long`) (used by: ops)
  - `short` — one line each
  - `long` — every field
- `region` `string` — the default region (beta) (used by: ops); must look like eu-1

  The region commands use when none is given.

## Files

The first file listed that sets a key supplies it.

- `~/.ops.yaml`
- `.ops.yaml` — in the working directory or the nearest parent holding one
- `./secrets.json` — or the file --secrets names; profiles under "stages", chosen by --stage (default dev)

## See Also

- [ops](ops.md)