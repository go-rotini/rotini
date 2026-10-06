# app deploy

deploy it

## Usage

```
app deploy <command> [flags]
```

## Commands

- `canary` — a canary deploy

## Flags

- `-r, --replicas` `int` — how many (default `1`); env `APP_DEPLOY_REPLICAS`, config `deploy.replicas`
- `--region` `string` — where; env `DEPLOY_REGION`, config `region`

## Inherited Flags

- `--verbose` — say more; env `APP_LOG_VERBOSE`, config `log.verbose`