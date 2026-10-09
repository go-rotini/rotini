# app deploy

deploy it

## Usage

```
app deploy [flags] <command> <target> [stage]
```

## Commands

- `canary` — a canary deploy

## Arguments

- `<target>` — what to deploy; also set by `DEPLOY_TARGET` or config key `deploy.target`
- `[stage]` — which stage (default `prod`)

## Flags

- `-r, --replicas` `int` — how many (default `1`); also set by `APP_DEPLOY_REPLICAS` or config key `deploy.replicas`
- `--region` `string` — where; also set by `DEPLOY_REGION` or config key `region`

## Global Flags

- `--verbose` — say more; also set by `APP_LOG_VERBOSE` or config key `log.verbose`