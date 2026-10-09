package rotini

// acSetDef is a small CLI beside the acme fixture for the cases about repeated values: a flag
// that may be given once, and lists that may not repeat a value.
func acSetDef() Definition {
	return Definition{
		Name: "set", Handler: "Set",
		Flags: []FlagDef{
			{Name: "name", Identifiers: []string{"--name"}, Type: "string", NoRepeat: true},
			{Name: "port", Identifiers: []string{"--port"}, Type: "[]int", UniqueItems: true},
		},
	}
}

type acSetInputs struct {
	Set struct {
		Flags struct {
			Name string `rotini:"name"`
			Port []int  `rotini:"port"`
		}
		Arguments struct{}
		Env       struct {
			Ports []int `rotini:"ports" recon:"ports" env:"SET_PORTS" min:"1" max:"65535" unique:"true"`
		}
	}
}
