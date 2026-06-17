package rotini

// Runnable godoc Examples for the opt-in services a handler reaches for most:
// Parser, Binder, the registry (Get/MustGet), and the category
// taxonomy. Each builds its own small Definition the way the generated
// framework file would, so the snippets read like handler code.

import (
	"errors"
	"fmt"
	"os"
)

// Parser.Parse fills the generated input struct from argv alone: typed
// coercion, defaults, enum and constraint checks — failing with a
// data-shaped *ParseError.
func ExampleParser_Parse() {
	def := Definition{
		Name: "app", Handler: "App",
		Commands: []CommandDef{{
			Name: "deploy", Handler: "AppDeploy",
			Arguments: []ArgDef{{Name: "service", Type: "string", Required: true}},
			Flags: []FlagDef{
				{Name: "env", Identifiers: []string{"--env"}, Type: "string", Default: "dev", Enum: []string{"dev", "prod"}},
				{Name: "loud", Identifiers: []string{"--loud", "-l"}, Type: "count"},
			},
		}},
	}
	// The shape codegen emits: one field per command on the resolved path.
	var inputs struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
		}
		Deploy struct {
			Flags struct {
				Env  string `rotini:"env"`
				Loud int    `rotini:"loud"`
			}
			Arguments struct {
				Service string `rotini:"service"`
			}
		}
	}

	rtx := NewContextFor(def, []string{"deploy", "api", "--env", "prod", "-ll"})
	if err := NewParser().Parse(rtx, &inputs); err != nil {
		fmt.Println("usage:", err)
		return
	}
	fmt.Printf("%s → %s (verbosity %d)\n",
		inputs.Deploy.Arguments.Service, inputs.Deploy.Flags.Env, inputs.Deploy.Flags.Loud)
	// Output: api → prod (verbosity 2)
}

// Binder.Bind reconciles every declared channel in one call — here a flag
// satisfied from its environment fallback because argv didn't set it.
func ExampleBinder_Bind() {
	def := Definition{
		Name: "app", Handler: "App",
		Flags: []FlagDef{
			// The recon key gives the flag fallbacks: $SERVER_PORT, then any
			// declared config files, then the default.
			{Name: "port", Identifiers: []string{"--port"}, Type: "int", Default: "8080"},
		},
	}
	var inputs struct {
		App struct {
			Flags struct {
				Port int `rotini:"port" recon:"server.port"`
			}
			Arguments struct{}
		}
	}

	os.Setenv("SERVER_PORT", "9090")
	defer os.Unsetenv("SERVER_PORT")

	rtx := NewContextFor(def, nil) // --port absent from argv
	if err := NewBinder(BindMeta{}).Bind(rtx, &inputs); err != nil {
		fmt.Println("bind:", err)
		return
	}
	fmt.Println("port:", inputs.App.Flags.Port)
	// Output: port: 9090
}

// The registry: anything bound on the Program (or Context) is fetched typed.
// Get reports absence; MustGet panics — and that panic reaches the
// Program.WithOnErrorFn funnel as a *PanicError, teardown already done.
func ExampleMustGet() {
	type apiClient struct{ baseURL string }

	rtx := NewContextFor(Definition{Name: "app", Handler: "App"}, nil)
	rtx.Bind("api", &apiClient{baseURL: "https://api.example"})

	client := MustGet[*apiClient](rtx, "api")
	fmt.Println(client.baseURL)

	if _, ok := Get[*apiClient](rtx, "other"); !ok {
		fmt.Println("nothing bound under \"other\"")
	}
	// Output:
	// https://api.example
	// nothing bound under "other"
}

// The category taxonomy: tag errors at the source, map them to conventional
// exit codes in one switch — typically inside Program.WithOnErrorFn.
func ExampleCategoryOf() {
	classify := func(err error) int {
		switch CategoryOf(err) {
		case CategoryUsage:
			return 1
		case CategoryInternal:
			return 70
		default:
			return 1
		}
	}

	fmt.Println(classify(UsageError(errors.New("unknown flag"))))
	fmt.Println(classify(InternalError(errors.New("wiring mismatch"))))
	fmt.Println(classify(errors.New("untagged")))
	// Output:
	// 1
	// 70
	// 1
}
