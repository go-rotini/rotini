package rotini

import (
	"regexp"
	"runtime/debug"
)

var readBuildInfo = debug.ReadBuildInfo
var semanticVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

type Build struct {
	Version         string
	VersionSemantic string
}

func BuildInfo(ldflagVersion string) *Build {
	b := &Build{}

	info, ok := readBuildInfo()
	switch {
	case ok && info.Main.Version != "" && info.Main.Version != "(devel)":
		b.Version = info.Main.Version
	case ldflagVersion != "":
		b.Version = ldflagVersion
	default:
		b.Version = "0.0.0"
	}

	s := semanticVersionRe.FindStringSubmatch(b.Version)
	if s == nil {
		b.VersionSemantic = "0.0.0"
	} else {
		b.VersionSemantic = s[1]
	}

	return b
}
