# examples/acme — a small, real rotini CLI

Compile-checked documentation: this program builds with the repository
(`go build ./...` — CI compiles it on every run) and demonstrates the four
pillars end to end.

**Declared** in [.rotini.spec.yaml](.rotini.spec.yaml): a root command with
`-h`/`-v`, a `deploy` command exercising every input channel — a required
positional, an enum flag with an env/config fallback (`key: deploy.env`), a
bounded int (`minimum`/`maximum`), a `count` flag, an env input scoped by
`env_prefix: ACME`, and a walk-up-discovered `.acme.yaml` config file.

**Generated** into [internal/acme](internal/acme/) by:

```
go run ./cmd/rotini generate ./examples/acme/.rotini.spec.yaml --config ./examples/acme/.rotini.conf.yaml
```

The framework file (`zz_rotini.gen.go`) and the embedded help pages are
rotini-managed; the three handler files were seeded once as stubs and are
hand-written from there.

**The handlers** show the opt-in services in their natural habitat:

- [acme.go](internal/acme/acme.go) — the root: `Parser.Parse`, the embedded
  help page, `Versioner`, and the `Suggestor` pattern (a `ParseError` carries
  the offending token and its vocabulary; the suggestor turns them into
  "did you mean").
- [acme_deploy.go](internal/acme/acme_deploy.go) — `Binder.Bind` reconciling
  argv + `$ACME_*` + the config file in one call, and (with `--explain`) the
  overlay surface (`ParseFiles`/`ParseEnv`/`ParseArgv` + `OverlayInputsP`)
  printing each field's winning layer and raw value.
- [acme_version.go](internal/acme/acme_version.go) — the `Versioner`.

**Try it:**

```
go build -o /tmp/acme ./examples/acme
printf 'deploy:\n  env: staging\n' > /tmp/.acme.yaml

cd /tmp
./acme deploy api --replicas 3            # deploy: api → staging ×3 (env from the config file)
ACME_REGION=eu-west ./acme deploy api -ll # count flag: (verbosity 2)
ACME_REGION=eu-west ./acme deploy api --explain
#   AcmeDeploy.Env.Region = eu-west (from env)
#   AcmeDeploy.Flags.Env  = staging (from files)
#   ...
./acme delpoy                             # Did you mean "deploy"?
./acme deploy api --replicas 99           # Error: --replicas must be <= 10 (got 99)
```
