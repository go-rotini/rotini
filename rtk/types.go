package rtk

type RotiniSpec struct {
	Name     string
	Commands []RotiniCommand
	Flags    []RotiniFlag
}

type RotiniCommand struct {
	Name    string
	Aliases []string
	Flags   []RotiniFlag
}

type RotiniFlag struct {
	Name string
}
