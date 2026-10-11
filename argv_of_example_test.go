package rotini

import (
	"fmt"
	"reflect"
)

// ArgvOf writes a typed inputs value back as the command line that supplies it. A generated
// Definition records each command's inputs type, so a test of a generated CLI passes
// p.Definition() and needs no ArgvPath.
func ExampleArgvOf() {
	type deployFlags struct {
		Env     string `rotini:"env"`
		Replica int    `rotini:"replicas"`
		Wait    bool   `rotini:"wait"`
	}
	type deployCommand struct {
		Flags     deployFlags
		Arguments struct {
			Service string `rotini:"service"`
		}
	}
	type deployInputs struct {
		App    struct{ Flags, Arguments struct{} }
		Deploy deployCommand
	}
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy", Inputs: reflect.TypeFor[deployInputs](),
			Arguments: []ArgDef{{Name: "service", Type: "string"}},
			Flags: []FlagDef{
				{Name: "env", Identifiers: []string{"--env", "-e"}, Type: "string"},
				{Name: "replicas", Identifiers: []string{"--replicas"}, Type: "int", Default: "2"},
				{Name: "wait", Identifiers: []string{"--wait"}, Type: "bool", Negatable: true, Default: "true"},
			},
		}},
	}

	var in deployInputs
	in.Deploy.Flags.Env = "prod"
	in.Deploy.Arguments.Service = "api"
	argv, _, err := ArgvOf(def, in, PresenceOf(in))
	fmt.Println(argv, err)

	// PresenceOf leaves zero values out; name them in set to write them.
	set := PresenceOf(in)
	set["Deploy.Flags.Replica"] = InputSource{}
	set["Deploy.Flags.Wait"] = InputSource{}
	argv, _, err = ArgvOf(def, in, set)
	fmt.Println(argv, err)
	// Output:
	// [deploy --env=prod -- api] <nil>
	// [deploy --env=prod --replicas=0 --no-wait -- api] <nil>
}
