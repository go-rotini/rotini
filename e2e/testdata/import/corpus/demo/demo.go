// Package demo is a representative Cobra CLI exercising every feature the importer maps.
package demo

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/spf13/cobra"
)

type format string

func (f *format) String() string { return string(*f) }
func (f *format) Set(s string) error {
	switch s {
	case "json", "yaml", "table":
		*f = format(s)
		return nil
	}
	return errors.New("bad format")
}
func (f *format) Type() string { return "format" }

func run(cmd *cobra.Command, args []string) error { fmt.Println(cmd.CommandPath(), args); return nil }

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:               "acme",
		Short:             "Acme ops tool",
		Long:              "Acme ops tool manages deployments, clusters and secrets.",
		Version:           "1.4.2",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	root.AddGroup(&cobra.Group{ID: "core", Title: "Core Commands:"}, &cobra.Group{ID: "admin", Title: "Admin Commands:"})
	root.PersistentFlags().CountP("verbose", "v", "increase verbosity (repeatable)")
	root.PersistentFlags().String("config", "", "path to `FILE` with settings")
	_ = root.MarkPersistentFlagFilename("config", "yaml", "yml")
	root.PersistentFlags().String("context", "default", "kube context")
	var out format = "table"
	root.PersistentFlags().VarP(&out, "output", "o", "output format: json|yaml|table")

	// deploy: ExactArgs(1) + ValidArgs, every flag type
	deploy := &cobra.Command{
		Use: "deploy <service>", Short: "Deploy a service", GroupID: "core",
		Aliases: []string{"d", "ship"},
		Example: `  # deploy the api
  acme deploy api --replicas 3
  acme deploy web --set image.tag=v2`,
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"api\tthe API service", "web", "worker"},
		PreRunE:   func(cmd *cobra.Command, args []string) error { return nil },
		RunE:      run,
		PostRun:   func(cmd *cobra.Command, args []string) {},
	}
	f := deploy.Flags()
	f.IntP("replicas", "r", 1, "replica count")
	f.Duration("timeout", 5*time.Minute, "rollout timeout")
	f.StringSlice("tags", []string{"a", "b"}, "tags to attach")
	f.StringArray("env", nil, "KEY=VAL env entries (repeatable)")
	f.StringToString("set", nil, "helm-style overrides")
	f.IntSlice("ports", []int{80, 443}, "ports")
	f.IP("bind", net.ParseIP("127.0.0.1"), "bind address")
	f.IPNet("subnet", net.IPNet{}, "subnet CIDR")
	f.Float64("canary", 0.1, "canary weight")
	f.Bool("dry-run", false, "print the plan only")
	f.Bool("wait", true, "wait for rollout")
	f.BytesHex("checksum", nil, "expected checksum")
	f.String("color", "auto", "colorize: auto|always|never")
	f.Lookup("color").NoOptDefVal = "always"
	f.String("strategy", "rolling", "rollout strategy")
	_ = deploy.RegisterFlagCompletionFunc("strategy", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"rolling", "bluegreen"}, cobra.ShellCompDirectiveNoFileComp
	})
	f.String("old-flag", "", "use --strategy")
	_ = f.MarkDeprecated("old-flag", "use --strategy instead")
	f.String("token", "", "API token")
	_ = f.MarkHidden("token")
	f.StringP("namespace", "n", "", "namespace")
	_ = f.MarkShorthandDeprecated("namespace", "use --namespace")
	f.String("cert", "", "TLS cert")
	f.String("key", "", "TLS key")
	deploy.MarkFlagsRequiredTogether("cert", "key")
	f.Bool("json", false, "json output")
	f.Bool("yaml", false, "yaml output")
	deploy.MarkFlagsMutuallyExclusive("json", "yaml")
	f.String("chart-dir", "", "chart directory")
	_ = deploy.MarkFlagDirname("chart-dir")
	_ = deploy.RegisterFlagCompletionFunc("token", cobra.NoFileCompletions)
	f.Time("at", time.Time{}, []string{time.RFC3339, "2006-01-02"}, "schedule the rollout")
	f.Func("hook", "run a hook", func(string) error { return nil })
	f.BoolFunc("trace", "print a trace", func(string) error { return nil })
	deploy.SuggestFor = []string{"push"}

	// rollback: RangeArgs, required flag, one_of group
	rollback := &cobra.Command{Use: "rollback <service> [revision]", Short: "Roll back a service", GroupID: "core", Args: cobra.RangeArgs(1, 2), RunE: run}
	rollback.Flags().String("reason", "", "audit reason")
	_ = rollback.MarkFlagRequired("reason")
	rollback.Flags().Bool("last", false, "previous revision")
	rollback.Flags().String("to", "", "specific revision")
	rollback.MarkFlagsOneRequired("last", "to")
	rollback.MarkFlagsMutuallyExclusive("last", "to")
	rollback.ValidArgsFunction = cobra.FixedCompletions([]string{"api", "web"}, cobra.ShellCompDirectiveNoFileComp)

	// logs: MinimumNArgs(1), unnamed in Use
	logs := &cobra.Command{Use: "logs", Short: "Tail logs of pods", GroupID: "core", Args: cobra.MinimumNArgs(1), RunE: run}
	logs.Flags().BoolP("follow", "f", false, "follow")
	logs.Flags().Int64("since-seconds", 0, "only newer logs")
	logs.ValidArgsFunction = func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// cluster group with nested commands
	cluster := &cobra.Command{Use: "cluster", Short: "Manage clusters", GroupID: "admin"}
	cluster.PersistentFlags().String("region", "us-east-1", "cloud region")
	cList := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List clusters", Args: cobra.NoArgs, RunE: run}
	cList.Flags().StringToInt("limits", nil, "per-zone limits")
	cCreate := &cobra.Command{Use: "create NAME", Short: "Create a cluster", Args: cobra.ExactArgs(1), RunE: run}
	cCreate.Flags().Uint8("nodes", 3, "node count")
	cCreate.Flags().DurationSlice("backoff", nil, "retry backoffs")
	cDelete := &cobra.Command{Use: "delete NAME...", Short: "Delete clusters", Args: cobra.MinimumNArgs(1), RunE: run, Deprecated: "use 'acme cluster rm'"}
	cluster.AddCommand(cList, cCreate, cDelete)

	// secrets: hidden, custom args validator
	secrets := &cobra.Command{Use: "secrets [name]", Short: "Show secrets", Hidden: true, GroupID: "admin",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return errors.New("at most one")
			}
			return nil
		}, RunE: run}
	secrets.Flags().BytesBase64("value", nil, "secret value")

	// exec: DisableFlagParsing -> passthrough
	exec := &cobra.Command{Use: "exec -- CMD [ARGS...]", Short: "Run a command in a pod", DisableFlagParsing: true, RunE: run}

	// ssh: SetInterspersed(false) -> options_first
	ssh := &cobra.Command{Use: "ssh <node> [command...]", Short: "Run a command on a node", Args: cobra.MinimumNArgs(1), RunE: run}
	ssh.Flags().BoolP("tty", "t", false, "allocate a terminal")
	ssh.Flags().SetInterspersed(false)

	// version: plain no-arg leaf, Args unset
	version := &cobra.Command{Use: "version", Short: "Print version", Run: func(*cobra.Command, []string) {}}

	// config: Args unset with Use args
	cfg := &cobra.Command{Use: "get <key>", Short: "Read a config key", RunE: run}

	// environment: a help topic (no Run, no sub-commands)
	topic := &cobra.Command{Use: "environment", Short: "Environment variables acme reads", Long: "ACME_TOKEN holds the API token.\nACME_CONTEXT picks the context."}

	root.AddCommand(deploy, rollback, logs, cluster, secrets, exec, ssh, version, cfg, topic)
	return root
}
