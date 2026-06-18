package tortellini

import (
	"regexp"
	"runtime/debug"
)

var readBuildInfo = debug.ReadBuildInfo
var semanticVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

// KeyVersioner is the conventional registry key the generated main binds the
// [Versioner] under (and the version handler retrieves it by). KeyCommit and
// KeyDate carry the other two goreleaser-convention ldflag stamps — the
// generated main binds them as plain strings for any handler that wants them;
// no generated handler reads them.
const (
	KeyVersioner = "versioner"
	KeyCommit    = "commit"
	KeyDate      = "date"
)

// Versioner carries the program's version in the two forms handlers print:
// the full module/ldflag string and its leading semantic X.Y.Z. It is a bound
// service like the parser — the generated main constructs one with
// [NewVersioner] and binds it under [KeyVersioner] for the version handler.
type Versioner struct {
	Version         string // the version as reported: module build info, else the ldflag value, else "0.0.0"
	VersionSemantic string // the leading X.Y.Z of Version (any v prefix/suffix stripped); "0.0.0" when none parses
}

// NewVersioner resolves the program's version: the module's build info when it
// carries a release version, else ldflagVersion (the -ldflags "-X main.version=…"
// escape hatch for non-module builds), else "0.0.0".
func NewVersioner(ldflagVersion string) *Versioner {
	v := &Versioner{}

	info, ok := readBuildInfo()
	switch {
	case ok && info.Main.Version != "" && info.Main.Version != "(devel)":
		v.Version = info.Main.Version
	case ldflagVersion != "":
		v.Version = ldflagVersion
	default:
		v.Version = "0.0.0"
	}

	s := semanticVersionRe.FindStringSubmatch(v.Version)
	if s == nil {
		v.VersionSemantic = "0.0.0"
	} else {
		v.VersionSemantic = s[1]
	}

	return v
}
