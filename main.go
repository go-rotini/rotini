package main

import (
	"os"

	"github.com/go-rotini/rotini/internal/handlers"
	"github.com/go-rotini/rotini/internal/rotini"
)

func main() {
	os.Exit(rotini.ExitCode(handlers.Program.Execute()))
}
