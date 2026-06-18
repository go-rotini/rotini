package rotini

import (
	"regexp"
	"runtime/debug"
)

const (
	KeyVersioner = "versioner"
)

var (
	readBuildInfo     = debug.ReadBuildInfo
	semanticVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)
)

// Versioner carries the program's version in the two forms handlers print:
// the full module/ldflag string and its leading semantic X.Y.Z. It is a bound
// service like the parser — the generated main constructs one with
// [NewVersioner] and binds it under [KeyVersioner] for the version handler.
type Versioner struct {
	Version         string // the version as reported: the module's build info, else the ldflag value (i.e. the program's own version variable)
	VersionSemantic string // the leading X.Y.Z of Version (any v prefix stripped); "0.0.0" when Version carries none, so it is always a valid semver
}

// NewVersioner resolves the program's version: the module's build info when it
// carries a release version (a go install / go get -tool build), otherwise
// ldflagVersion — the value baked in via -ldflags "-X main.version=…", or the
// program's own default for that variable when no ldflag was applied. The
// framework imposes no fallback of its own; the ultimate default is whatever the
// program set its version variable to.
func NewVersioner(ldflagVersion string) *Versioner {
	version := ldflagVersion
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}

	v := &Versioner{Version: version, VersionSemantic: "0.0.0"}
	if match := semanticVersionRe.FindStringSubmatch(version); match != nil {
		v.VersionSemantic = match[1]
	}
	return v
}
