package rotini

import (
	"os"
	"strings"
)

func EnvNoColor() bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return false
	}
	return os.Getenv("NO_COLOR") != ""
}

func DetectProfile() Profile {
	if EnvNoColor() {
		return ProfileNoColor
	}
	switch colorterm := os.Getenv("COLORTERM"); colorterm {
	case "truecolor", "24bit":
		return ProfileTrueColor
	}
	term := os.Getenv("TERM")
	switch {
	case strings.Contains(term, "truecolor"):
		return ProfileTrueColor
	case strings.Contains(term, "256color"):
		return ProfileANSI256
	case term == "" || term == "dumb":
		return ProfileNoColor
	default:
		return ProfileANSI16
	}
}

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
