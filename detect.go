package rotini

import "os"

// The two questions a program has to answer before it decides how to write to a stream: is
// anyone watching, and do they want color. rotini never asks them for you — nothing in the
// runtime calls either function. They are here because both answers are environment trivia that
// every CLI needs and no library should make you depend on it for.
//
// What a program does with the answers — styling, tables, spinners, prompts — is not rotini's
// business. Hand them to whatever draws your output.

// EnvNoColor reports whether the environment asks for no color, honoring the
// NO_COLOR convention and its CLICOLOR_FORCE override (a non-empty, non-"0"
// CLICOLOR_FORCE wins, meaning "color anyway").
func EnvNoColor() bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return false
	}
	return os.Getenv("NO_COLOR") != ""
}

// IsTerminal reports whether file is a character device — a terminal rather than a pipe, a
// regular file, or /dev/null. A nil file is not a terminal. It is the check behind "is anyone
// watching this?": paging, animating and prompting all become wrong when the answer is no.
func IsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
