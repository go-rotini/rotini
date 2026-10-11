// Package demo is a representative urfave/cli v3 CLI exercising every feature the importer
// maps.
package demo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"
)

type format struct{ v string }

func (f *format) Set(s string) error {
	switch s {
	case "json", "yaml", "table":
		f.v = s
		return nil
	}
	return errors.New("bad format")
}
func (f *format) String() string { return f.v }
func (f *format) Get() any       { return f.v }

func action(ctx context.Context, cmd *cli.Command) error { fmt.Println(cmd.FullName()); return nil }

var verbosity int

// Root builds the tree.
func Root() *cli.Command {
	stop := 1
	return &cli.Command{
		Name:                  "acme",
		Usage:                 "Acme ops tool",
		Description:           "Acme ops tool manages deployments, clusters and secrets.",
		Version:               "1.4.2",
		Suggest:               true,
		EnableShellCompletion: true,
		Before:                func(ctx context.Context, cmd *cli.Command) (context.Context, error) { return ctx, nil },
		After:                 func(ctx context.Context, cmd *cli.Command) error { return nil },
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"V"}, Usage: "increase verbosity (repeatable)", Config: cli.BoolConfig{Count: &verbosity}},
			&cli.StringFlag{Name: "config", Usage: "path to `FILE` with settings", TakesFile: true, Sources: cli.EnvVars("ACME_CONFIG")},
			&cli.StringFlag{Name: "context", Value: "default", Usage: "kube context", Sources: cli.NewValueSourceChain(cli.EnvVar("ACME_CONTEXT"), cli.File("/etc/acme/context"))},
			&cli.GenericFlag{Name: "output", Aliases: []string{"o"}, Value: &format{"table"}, Usage: "output format: json|yaml|table"},
			&cli.StringFlag{Name: "log-file", Usage: "log file", Local: true, Category: "Logging"},
		},
		Commands: []*cli.Command{
			{
				Name:     "deploy",
				Aliases:  []string{"d", "ship"},
				Usage:    "Deploy a service",
				Category: "Core",
				Arguments: []cli.Argument{
					&cli.StringArg{Name: "service", UsageText: "the service"},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) { return ctx, nil },
				Action: action,
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "replicas", Aliases: []string{"r"}, Value: 1, Usage: "replica count", Sources: cli.EnvVars("ACME_REPLICAS", "REPLICAS")},
					&cli.DurationFlag{Name: "timeout", Value: 5 * time.Minute, Usage: "rollout timeout"},
					&cli.StringSliceFlag{Name: "tags", Value: []string{"a", "b"}, Usage: "tags to attach"},
					&cli.IntSliceFlag{Name: "ports", Value: []int{80, 443}, Usage: "ports"},
					&cli.StringMapFlag{Name: "set", Usage: "helm-style overrides"},
					&cli.FloatFlag{Name: "canary", Value: 0.1, Usage: "canary weight"},
					&cli.BoolWithInverseFlag{Name: "wait", Value: true, Usage: "wait for rollout"},
					&cli.BoolWithInverseFlag{Name: "color", Usage: "colorize", InversePrefix: "plain-"},
					&cli.StringFlag{Name: "strategy", Value: "rolling", Usage: "rollout strategy", OnlyOnce: true,
						Validator: func(s string) error { return nil }},
					&cli.StringFlag{Name: "token", Usage: "API token", Hidden: true, Sources: cli.EnvVars("acme_token")},
					&cli.StringFlag{Name: "old-flag", Usage: "use --strategy", Deprecated: "use --strategy instead"},
					&cli.StringFlag{Name: "reason", Usage: "audit reason", Required: true, Value: "none"},
					&cli.TimestampFlag{Name: "at", Usage: "schedule the rollout", Config: cli.TimestampConfig{Layouts: []string{"2006-01-02"}}},
					&cli.IntFlag{Name: "mode", Usage: "file mode", Config: cli.IntegerConfig{Base: 8}},
					&cli.BoolFlag{Name: "trace", Usage: "print a trace", Action: func(ctx context.Context, cmd *cli.Command, b bool) error { return nil }},
					&cli.StringFlag{Name: "db.host", Usage: "database host"},
				},
				MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
					{Flags: [][]cli.Flag{{&cli.BoolFlag{Name: "json"}}, {&cli.BoolFlag{Name: "yaml"}}}, Category: "Output"},
					{Required: true, Flags: [][]cli.Flag{{&cli.BoolFlag{Name: "last"}}, {&cli.StringFlag{Name: "to"}}}},
					{Flags: [][]cli.Flag{{&cli.StringFlag{Name: "cert"}, &cli.StringFlag{Name: "key"}}, {&cli.BoolFlag{Name: "insecure"}}}},
				},
			},
			{
				Name:     "rollback",
				Usage:    "Roll back a service",
				Category: "Core",
				Arguments: []cli.Argument{
					&cli.StringArg{Name: "service"},
					&cli.IntArg{Name: "revision", Value: 1},
				},
				Action: action,
			},
			{
				Name:      "logs",
				Usage:     "Tail logs of pods",
				ArgsUsage: "<pod>... ",
				Action:    action,
				Flags:     []cli.Flag{&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "follow"}},
			},
			{
				Name:     "cluster",
				Usage:    "Manage clusters",
				Category: "Admin",
				Flags:    []cli.Flag{&cli.StringFlag{Name: "region", Value: "us-east-1", Usage: "cloud region"}},
				Commands: []*cli.Command{
					{Name: "list", Aliases: []string{"ls"}, Usage: "List clusters", Action: action},
					{Name: "create", Usage: "Create a cluster", Action: action, Arguments: []cli.Argument{&cli.StringArgs{Name: "name", Min: 1, Max: 1}},
						Flags: []cli.Flag{&cli.Uint8Flag{Name: "nodes", Value: 3, Usage: "node count"}}},
					{Name: "delete", Usage: "Delete clusters", Action: action, Deprecated: "use 'acme cluster rm'",
						Arguments: []cli.Argument{&cli.StringArgs{Name: "names", Min: 1, Max: -1}}},
				},
			},
			{
				Name:      "secrets",
				Usage:     "Show secrets",
				Hidden:    true,
				Category:  "Admin",
				ArgsUsage: "[name]",
				Action:    action,
			},
			{
				Name:            "exec",
				Usage:           "Run a command in a pod",
				SkipFlagParsing: true,
				Action:          action,
			},
			{
				Name:         "ssh",
				Usage:        "Run a command on a node",
				StopOnNthArg: &stop,
				Arguments:    []cli.Argument{&cli.StringArg{Name: "node"}, &cli.StringArgs{Name: "command", Max: -1}},
				Flags:        []cli.Flag{&cli.BoolFlag{Name: "tty", Aliases: []string{"t"}, Usage: "allocate a terminal"}},
				Action:       action,
			},
			{
				Name:           "config",
				Usage:          "Read and write configuration",
				DefaultCommand: "get",
				UsageText:      "acme config get|set KEY [VALUE]",
				Commands: []*cli.Command{
					{Name: "get", Usage: "Read a key", ArgsUsage: "<key>", Action: action},
					{Name: "set", Usage: "Write a key", ArgsUsage: "[key] <value>", Action: action},
				},
			},
			{
				Name:                "version",
				Usage:               "Print version",
				Action:              action,
				ShellComplete:       func(ctx context.Context, cmd *cli.Command) {},
				PrefixMatchCommands: true,
			},
		},
	}
}
