package main

import (
	"fmt"

	"github.com/go-rotini/rotini"
)

func main() {
	styler := rotini.NewStyler()

	// 1. Build a style in place; the chain writes through to the registry.
	styler.Define("warning").ForegroundHex("#fcba03").Bold()

	// 2. Add a style you built yourself.
	styler.Set("error", rotini.NewStyle().ForegroundHex("#ff3333").Bold())
	styler.Set("panic", rotini.NewStyle().ForegroundHex("#ff4dee").Blink())

	fmt.Println(styler.Render("warning", "Warning: low disk space"))
	fmt.Println(styler.Render("error", "Error: connection refused"))
	fmt.Println(styler.Render("panic", "Fatal: big broke"))
}
