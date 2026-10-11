// Package demo is a representative Kong CLI exercising every feature the importer maps.
package demo

import (
	"errors"
	"fmt"
	"time"

	"github.com/alecthomas/kong"
)

// Level is a custom type with its own Kong decoding.
type Level int

func (l *Level) Decode(ctx *kong.DecodeContext) error {
	var s string
	if err := ctx.Scan.PopValueInto("level", &s); err != nil {
		return err
	}
	switch s {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return errors.New("bad level")
	}
	return nil
}

// Trace is a flag type with a hook Kong calls when the flag is given.
type Trace bool

func (t Trace) BeforeApply() error { fmt.Println("tracing"); return nil }

type DBFlags struct {
	Host string `help:"Database host." default:"localhost"`
	Port int    `help:"Database port." default:"5432"`
}

type Globals struct {
	Verbose int              `short:"v" type:"counter" help:"Increase verbosity (repeatable)."`
	Config  kong.ConfigFlag  `help:"Path to a config file." placeholder:"FILE"`
	Context string           `help:"Kube context." default:"default" env:"ACME_CONTEXT"`
	Output  string           `short:"o" help:"Output format." enum:"json,yaml,table" default:"table"`
	Version kong.VersionFlag `help:"Print the version."`
}

type DeployCmd struct {
	Service  string            `arg:"" help:"Service to deploy." enum:"api,web,worker"`
	Replicas int               `short:"r" help:"Replica count." default:"1" env:"ACME_REPLICAS,REPLICAS"`
	Timeout  time.Duration     `help:"Rollout timeout." default:"5m"`
	Tags     []string          `help:"Tags to attach." default:"a,b"`
	Ports    []int             `help:"Ports." sep:";" default:"80;443"`
	Set      map[string]string `help:"Helm-style overrides."`
	Limits   map[string]int    `help:"Per-zone limits." mapsep:","`
	DryRun   bool              `help:"Print the plan only." negatable:""`
	Color    bool              `help:"Colorize output." default:"true" negatable:"plain"`
	Strategy string            `help:"Rollout strategy." enum:"rolling,bluegreen" default:"rolling" group:"Rollout"`
	Canary   float64           `help:"Canary weight." default:"0.1" group:"Rollout"`
	Token    string            `help:"API token." hidden:"" env:"acme-token"`
	Cert     string            `help:"TLS cert." and:"tls" type:"existingfile"`
	Key      string            `help:"TLS key." and:"tls" type:"existingfile"`
	JSON     bool              `help:"JSON output." xor:"format"`
	YAML     bool              `help:"YAML output." xor:"format"`
	ChartDir string            `help:"Chart directory." type:"existingdir"`
	Out      string            `help:"Where to write the plan." type:"path"`
	At       time.Time         `help:"Schedule the rollout." format:"2006-01-02"`
	Level    Level             `help:"Alert level."`
	Trace    Trace             `help:"Print a trace."`
	Region   string            `help:"Cloud region (${default_region})." default:"${default_region}"`
	Zone     string            `help:"Zone in ${stage}." short:"1"`
	DB       DBFlags           `embed:"" prefix:"db."`
}

func (d *DeployCmd) Help() string { return "Deploy rolls out a service to the current context." }

func (d *DeployCmd) BeforeApply() error { return nil }

func (d *DeployCmd) Run(g *Globals) error { fmt.Println("deploy", d.Service); return nil }

func (d *DeployCmd) AfterRun() error { return nil }

type RollbackCmd struct {
	Service  string `arg:"" help:"Service to roll back."`
	Revision int    `arg:"" optional:"" help:"Revision to roll back to."`
	Reason   string `help:"Audit reason." required:"" default:"none"`
	Last     bool   `help:"Previous revision." xor:"rev" required:""`
	To       string `help:"Specific revision." xor:"rev" required:""`
}

func (r *RollbackCmd) Run() error { return nil }

type ClusterCmd struct {
	Region string `help:"Cloud region." default:"us-east-1"`

	List   struct{} `cmd:"" aliases:"ls" help:"List clusters."`
	Create struct {
		Name  string `arg:"" help:"Cluster name."`
		Nodes uint8  `help:"Node count." default:"3"`
	} `cmd:"" help:"Create a cluster."`
	Delete struct {
		Names []string `arg:"" help:"Clusters to delete."`
	} `cmd:"" help:"Delete clusters."`
}

func (c *ClusterCmd) AfterApply() error { return nil }

type CLI struct {
	Globals

	Deploy   DeployCmd   `cmd:"" aliases:"d,ship" help:"Deploy a service." group:"Core"`
	Rollback RollbackCmd `cmd:"" help:"Roll back a service.\nRolls back to the previous or a given revision." group:"Core"`
	Cluster  ClusterCmd  `cmd:"" help:"Manage clusters." group:"Admin"`
	Secrets  struct {
		Name string `arg:"" optional:"" default:"all" help:"Secret to show."`
	} `cmd:"" hidden:"" help:"Show secrets." group:"Admin"`
	Exec struct {
		Args []string `arg:"" help:"Command to run."`
	} `cmd:"" passthrough:"" help:"Run a command in a pod."`
	SSH struct {
		Node    string   `arg:"" help:"Node to connect to."`
		Command []string `arg:"" optional:"" passthrough:"" help:"Command to run."`
		TTY     bool     `short:"t" help:"Allocate a terminal."`
	} `cmd:"" name:"ssh" help:"Run a command on a node."`
	Status struct{} `cmd:"" default:"1" help:"Show status."`
	User   struct {
		User   string   `arg:"" help:"User name."`
		Delete struct{} `cmd:"" help:"Delete the user."`
	} `arg:"" help:"Manage a user."`
}
