package rotini_test

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-rotini/rotini"
)

// keyValueParser reads `name=value` words as flags and any other word after the command path
// as an argument.
type keyValueParser struct{}

func (keyValueParser) ParseArgv(rtx *rotini.Context) (rotini.ArgvValues, error) {
	var out rotini.ArgvValues
	for _, word := range rtx.Argv[len(rtx.CommandChain())-1:] {
		if name, value, ok := strings.Cut(word, "="); ok {
			out.Flags = append(out.Flags, rotini.ArgvFlag{Name: name, Value: value, Typed: name})
			continue
		}
		out.Args = append(out.Args, word)
	}
	return out, nil
}

type shipInputs struct {
	Ship struct {
		Flags struct {
			Replicas int    `rotini:"replicas"`
			Region   string `rotini:"region" recon:"region" env:"SHIP_REGION"`
		}
		Arguments struct {
			Service string `rotini:"service"`
		}
	}
}

// An ArgvParser gives a CLI its own command-line syntax. It reads only the command line: the
// environment still fills what it leaves out, and every declared check still applies.
func ExampleArgvParser() {
	def := rotini.Definition{
		Name: "ship", Handler: "Ship", Inputs: reflect.TypeFor[shipInputs](),
		Flags: []rotini.FlagDef{
			{Name: "replicas", Identifiers: []string{"--replicas"}, Type: "int", Default: "1"},
			{Name: "region", Identifiers: []string{"--region"}, Type: "string", Enum: []string{"eu", "us"}},
		},
		Arguments: []rotini.ArgDef{{Name: "service", Type: "string"}},
	}
	read := func(argv ...string) {
		rtx := rotini.NewContextFor(def, argv).WithParser(keyValueParser{}).WithEnviron([]string{"SHIP_REGION=eu"})
		in, report, err := rtx.InputsWithReport[shipInputs]()
		if err != nil {
			fmt.Println(err)
			return
		}
		region, _ := report.Winner("Ship.Flags.Region")
		fmt.Printf("%s: %d replicas in %s (from %s)\n", in.Ship.Arguments.Service, in.Ship.Flags.Replicas, in.Ship.Flags.Region, region.Origin)
	}
	read("replicas=3", "web")
	read("replicas=3", "region=us", "web")
	read("region=mars", "web")
	// Output:
	// web: 3 replicas in eu (from env:SHIP_REGION)
	// web: 3 replicas in us (from argv:region)
	// ship: invalid value "mars" for region (one of: eu, us)
}
