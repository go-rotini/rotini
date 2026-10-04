---
title: "plugins"
---

# Plugins for kubectl, Docker and Flux

kubectl, Docker and Flux each run separate programs as their own sub-commands: `kubectl ctx`
runs a binary named `kubectl-ctx`. A rotini CLI can be one of those plugins. This page covers
what each host expects and the few lines that make a rotini CLI fit.

## How each host runs a plugin

| | kubectl | Docker | Flux |
|---|---|---|---|
| Binary name | `kubectl-<name>` | `docker-<name>` | `flux-<name>` |
| Where it is found | anywhere on `PATH` | `~/.docker/cli-plugins` (or `$DOCKER_CONFIG/cli-plugins`), plus system plugin directories | `$FLUXCD_PLUGINS`, default `~/.fluxcd/plugins` (Flux 2.9 and later) |
| Arguments it receives | everything after the plugin's name | **Docker's own arguments**: `docker -c prod where x` runs `docker-where -c prod where x` | everything after the plugin's name, plus flags typed before it |
| Before running it | nothing | runs `docker-<name> docker-cli-plugin-metadata` and reads one JSON object | nothing |

For kubectl, each `-` in the binary name is a level of sub-command: `kubectl-ctx-use` answers
`kubectl ctx use`. A `-` inside a single command name is written as `_` in the file name.

**Docker passes its own arguments through**, so a Docker plugin's spec stands in for `docker`
itself. The root accepts Docker's global options (`--config`, `-c`/`--context`, `-D`, `-H`,
`-l`, `--tls…`), marked `cascading: true`, and the plugin is a sub-command beneath it:

{{< code title="cmd/docker-where/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: docker-where
  display_name: docker
  flags:
    - name: context
      identifiers: [-c, --context]
      cascading: true
      schema: { type: string }
    # … the rest of Docker's global options
  commands:
    - name: where
      summary: Show which context and daemon endpoint docker would use
    - name: docker-cli-plugin-metadata
      summary: print the plugin's metadata for docker
      hidden: true
{{< /code >}}

The hidden `docker-cli-plugin-metadata` command answers Docker's probe. Its handler prints one
JSON object and nothing else. Docker refuses a plugin whose `SchemaVersion` is not `0.1.0` or
whose `Vendor` is empty, and lists the plugin in `docker --help` with its `ShortDescription`:

{{< code title="internal/cmd/docker-where/docker_where_docker_cli_plugin_metadata.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*dockerWhereDockerCliPluginMetadataHandler) Run(ctx context.Context, rtx *rotini.Context) {
	meta := map[string]string{
		"SchemaVersion":    "0.1.0",
		"Vendor":           "example",
		"Version":          rtx.Version(),
		"ShortDescription": "Show which context and daemon endpoint docker would use",
	}
	if err := json.NewEncoder(rtx.Stdout).Encode(meta); err != nil {
		rtx.HaltWith(err)
	}
}
{{< /code >}}

## Tab completion

Each host completes a plugin's arguments by asking the plugin, and all three read the same
completion format: one candidate per line, `value<TAB>description`, then a final `:<number>`
line with directives such as "don't fall back to file names". rotini computes the answer from
the spec, your completers and each input's `complete:` hint, and `rotini.PluginCompletion`
writes it in that format.

**kubectl** runs a separate executable, `kubectl_complete-<name>`, found on `PATH`. Install the
plugin's binary a second time under that name (a copy or a symlink), and have `main.go` answer
when it is run that way:

{{< code title="cmd/kubectl-ctx/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
func main() {
	if strings.Contains(filepath.Base(os.Args[0]), "_complete-") {
		code, _ := cmd.Program.Complete(os.Args[1:], rotini.PluginCompletion)
		os.Exit(code)
	}
	cmd.Program.WithVersion(version).Execute()
}
{{< /code >}}

**Docker and Flux** run the plugin's own hidden `__complete` command:
`docker-where __complete where <words…>`, `flux-suspended __complete <words…>`. Every rotini CLI
has that command; by default it answers in the format rotini's own generated shell scripts read.
Set the plugin format in `main.go`:

{{< code title="cmd/docker-where/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	WithVersion(version).
	WithCompletion(rotini.PluginCompletion).
	Execute()
{{< /code >}}

Both hosts treat the last line as the directive line, always. Without the setting, the last real
candidate would be read as a directive and lost. Leave it unset for a standalone CLI, whose
completion scripts come from the `completion` feature and expect rotini's format.

## Help that reads like the host

Set `display_name` on the root, and every generated help, man and markdown page shows the
command the user types instead of the binary's name:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: kubectl-ctx
  display_name: kubectl ctx
{{< /code >}}

`kubectl ctx use --help` then shows `Usage: kubectl ctx use …`. File names, the completion
target and anything you wrote verbatim keep the binary's real name.

## kubectl's connection flags

kubectl users expect a plugin to take the same connection flags kubectl does: `--kubeconfig`,
`--context`, `-n`/`--namespace`, `--as`, `--token`, `--server` and the rest. Declare them on the
plugin's root with `cascading: true`, so they work on every sub-command and its help lists them,
and hand the parsed values to client-go's `ConfigFlags` from cli-runtime. It then does everything
kubectl does with them: loading and merging kubeconfigs, choosing the context, impersonation, TLS,
and the namespace default.

{{< code title="internal/cmd/kubectl-ctx/kube.go" language="golang" open="true" collapsible="false" copy="true" >}}
func configFlags(f KubectlCtxFlags) *genericclioptions.ConfigFlags {
	cf := genericclioptions.NewConfigFlags(true)
	*cf.KubeConfig = f.Kubeconfig
	*cf.Context = f.Context
	*cf.Namespace = f.Namespace
	*cf.BearerToken = f.Token
	*cf.Impersonate = f.As
	// … one line per flag
	return cf
}
{{< /code >}}

{{< alert type="warning" title="DON'T BIND KUBECONFIG TO THE FLAG:" >}}
Leave the `--kubeconfig` flag without an environment fallback. `KUBECONFIG` is a list of files,
separated by `:`, that client-go merges. Bound to the flag it would become one path, and a user
with two kubeconfig files would get neither. Left alone, client-go reads it exactly as kubectl
does.
{{< /alert >}}

The short spellings kubectl users type from habit parse as they expect: `-nfoo`, `-lapp=api`,
`-ojson`, and `-A` grouped with other short flags.

Flux's plugins follow the same pattern with Flux's conventions: `-n` defaults to `flux-system`
and to `$FLUX_SYSTEM_NAMESPACE`, not to the kubeconfig context's namespace.
