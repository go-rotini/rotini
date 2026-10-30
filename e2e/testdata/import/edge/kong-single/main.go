// Command single is a Kong program with no sub-commands.
package main

import "github.com/alecthomas/kong"

var CLI struct {
	Words bool     `short:"w" help:"Count words instead."`
	Skip  []string `help:"Line prefixes to skip." env:"SINGLE_SKIP"`
	Mode  string   `help:"Counting mode." enum:"fast,exact" default:"exact"`
	File  string   `arg:"" help:"File to count."`
}

func main() { kong.Parse(&CLI, kong.Name("single")) }
